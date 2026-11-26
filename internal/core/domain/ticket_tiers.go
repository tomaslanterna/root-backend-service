package domain

import "strings"

// tierNameFolder pasa el nombre de un lote a minúsculas sin tildes para compararlo.
var tierNameFolder = strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u")

func foldTierName(name string) string {
	return tierNameFolder.Replace(strings.ToLower(name))
}

// IsCourtesyTier indica si el lote es una cortesía (invitaciones): no se venden, así que no se muestran.
func IsCourtesyTier(name string) bool {
	return strings.Contains(foldTierName(name), "cortesia")
}

// IsExchangeTier indica si el lote es un canje (por ejemplo, "CANJE SANTANDER" o canjes de cualquier banco):
// se muestra, pero sin precio, porque no se paga con dinero.
func IsExchangeTier(name string) bool {
	return strings.Contains(foldTierName(name), "canje")
}

// CleanTicketTiers saca las cortesías y deja sin precio los canjes. Devuelve una lista nueva (no modifica la
// original) y conserva nil cuando la entrada es nil, porque nil y [] significan cosas distintas para el detalle.
func CleanTicketTiers(tiers []TicketTier) []TicketTier {
	if tiers == nil {
		return nil
	}
	clean := make([]TicketTier, 0, len(tiers))
	for _, tier := range tiers {
		if IsCourtesyTier(tier.Name) {
			continue
		}
		if IsExchangeTier(tier.Name) {
			tier.Price = nil
		}
		clean = append(clean, tier)
	}
	return clean
}
