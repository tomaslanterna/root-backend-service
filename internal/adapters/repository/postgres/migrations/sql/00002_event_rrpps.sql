-- RRPPs oficiales por evento. EventRRPPRepository.InitSchema define esta tabla pero nunca se
-- llamaba al arrancar; si la tabla ya existe (creada a mano), esta migración no cambia nada.

-- +goose Up
CREATE TABLE IF NOT EXISTS event_rrpps (
	event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
	user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	position   SMALLINT NOT NULL DEFAULT 0 CHECK (position >= 0),
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (event_id, user_id)
);
-- La PK cubre las búsquedas por evento; este índice cubre el FK a users (borrado en cascada).
CREATE INDEX IF NOT EXISTS event_rrpps_user_idx ON event_rrpps (user_id);

-- +goose Down
-- Sin bajada: borrar la tabla perdería datos de producción.
