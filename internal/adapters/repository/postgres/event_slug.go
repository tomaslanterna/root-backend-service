package postgres

import (
	"strings"

	"github.com/google/uuid"
)

// URLs legibles de eventos: /events/danzeria-presenta-greggio en lugar del UUID.
//
// El slug lo arma la base (trigger BEFORE INSERT), así sirve para todas las formas de crear eventos: el admin,
// el CSV, el import semanal, "Mandanos tu evento" y los scripts de cmd/. Se fija una sola vez: si después se edita
// el título, el slug no cambia y los links que ya se compartieron siguen andando.
//
// Reglas:
//   - Minúsculas, sin acentos, lo que no es letra o número pasa a "-", hasta 60 caracteres. Vacío: "evento".
//   - Si ya existe (u ocupa una ruta, ver eventSlugReserved), se le suma la fecha: "-28-nov". Si igual choca, "-2", "-3"…
//   - El UUID sigue funcionando: GET /v1/events/{id} acepta los dos (ver eventLookupClause).

// eventSlugReserved: rutas que viven bajo /events/ en el front o en la API. Un slug nunca puede ser una de estas.
var eventSlugReserved = []string{
	"featured", "live-status", "similar", "image", "bulk-csv", "submit", "submissions", "new", "create", "edit",
}

func eventSlugReservedSQL() string {
	quoted := make([]string, len(eventSlugReserved))
	for i, word := range eventSlugReserved {
		quoted[i] = "'" + word + "'"
	}
	return "ARRAY[" + strings.Join(quoted, ", ") + "]"
}

// eventSlugSchema: idempotente. Lo corre EventRepository.InitSchema dentro de su transacción.
func eventSlugSchema() []string {
	return []string{
		// Un solo arranque a la vez completa slugs (dos instancias no generan el mismo para eventos distintos).
		`SELECT pg_advisory_xact_lock(hashtext('root_event_slug'))`,
		`ALTER TABLE events ADD COLUMN IF NOT EXISTS slug TEXT`,
		// Único entre los que tienen slug (NULL se permite repetido mientras se completa).
		`CREATE UNIQUE INDEX IF NOT EXISTS events_slug_uidx ON events (slug)`,
		`CREATE OR REPLACE FUNCTION root_slugify(input TEXT) RETURNS TEXT LANGUAGE sql IMMUTABLE AS $fn$
			SELECT RTRIM(LEFT(BTRIM(regexp_replace(
				translate(LOWER(COALESCE(input, '')), 'áàäâãåéèëêíìïîóòöôõúùüûñçý', 'aaaaaaeeeeiiiiooooouuuuncy'),
				'[^a-z0-9]+', '-', 'g'), '-'), 60), '-')
		$fn$`,
		// p_date es events.date: la hora de Montevideo sin zona, así que el día y el mes salen directo.
		`CREATE OR REPLACE FUNCTION root_event_slug(p_title TEXT, p_date TIMESTAMP, p_id UUID) RETURNS TEXT
		LANGUAGE plpgsql VOLATILE AS $fn$
		DECLARE
			reserved CONSTANT TEXT[] := ` + eventSlugReservedSQL() + `;
			months CONSTANT TEXT[] := ARRAY['ene','feb','mar','abr','may','jun','jul','ago','sep','oct','nov','dic'];
			base TEXT := root_slugify(p_title);
			candidate TEXT;
			n INT := 2;
		BEGIN
			IF base = '' THEN
				base := 'evento';
			END IF;
			candidate := base;
			IF candidate = ANY(reserved) OR EXISTS (SELECT 1 FROM events WHERE slug = candidate AND id IS DISTINCT FROM p_id) THEN
				IF p_date IS NOT NULL THEN
					base := base || '-' || EXTRACT(DAY FROM p_date)::INT || '-' || months[EXTRACT(MONTH FROM p_date)::INT];
				END IF;
				candidate := base;
				WHILE candidate = ANY(reserved) OR EXISTS (SELECT 1 FROM events WHERE slug = candidate AND id IS DISTINCT FROM p_id) LOOP
					candidate := base || '-' || n;
					n := n + 1;
				END LOOP;
			END IF;
			RETURN candidate;
		END
		$fn$`,
		`CREATE OR REPLACE FUNCTION root_events_set_slug() RETURNS TRIGGER LANGUAGE plpgsql AS $fn$
		BEGIN
			IF NEW.slug IS NULL OR BTRIM(NEW.slug) = '' THEN
				-- Dos altas simultáneas con el mismo título no pueden elegir el mismo slug.
				PERFORM pg_advisory_xact_lock(hashtext('root_event_slug'));
				NEW.slug := root_event_slug(NEW.title, NEW.date, NEW.id);
			END IF;
			RETURN NEW;
		END
		$fn$`,
		`DROP TRIGGER IF EXISTS events_set_slug ON events`,
		`CREATE TRIGGER events_set_slug BEFORE INSERT ON events FOR EACH ROW EXECUTE FUNCTION root_events_set_slug()`,
		// Eventos que ya existían: de a uno, del más viejo al más nuevo, así el primero se queda con el slug limpio.
		`DO $do$
		DECLARE rec RECORD;
		BEGIN
			FOR rec IN SELECT id, title, date FROM events WHERE slug IS NULL ORDER BY created_at NULLS FIRST, date, id LOOP
				UPDATE events SET slug = root_event_slug(rec.title, rec.date, rec.id) WHERE id = rec.id;
			END LOOP;
		END
		$do$`,
		`ALTER TABLE events ALTER COLUMN slug SET NOT NULL`,
	}
}

// eventLookupClause: condición para buscar un evento por UUID o por slug. Con UUID usa la PK; si no, el índice
// único del slug (los slugs se guardan en minúsculas).
func eventLookupClause(alias, placeholder, idOrSlug string) (string, string) {
	if parsed, err := uuid.Parse(idOrSlug); err == nil {
		return alias + ".id = " + placeholder + "::uuid", parsed.String()
	}
	return alias + ".slug = " + placeholder, strings.ToLower(strings.TrimSpace(idOrSlug))
}
