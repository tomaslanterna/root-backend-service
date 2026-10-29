package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	"root-backend-service/internal/core/domain"
	"root-backend-service/internal/core/ports"
)

type geminiContentModerator struct{}

func NewGeminiContentModerator() ports.ContentModerationService {
	return &geminiContentModerator{}
}

type moderationResponse struct {
	IsViolating bool   `json:"is_violating"`
	Reason      string `json:"reason"`
}

var rioplatenseInsultRegex = regexp.MustCompile(`(?i)\b(` + strings.Join([]string{
	`mierda+s?`,
	`pelotud[oa]s?`,
	`pelotudeces`,
	`bolud[oa]s?`,
	`forr[oa]s?`,
	`orti[bv]as?`,
	`mogolic[oa]s?`,
	`garcas?`,
	`soretes?`,
	`hdp`,
	`hij[oa]s?\s+de\s+(mil\s+|re\s*mil\s+)?putas?`,
	`concha\s+de\s+(tu|su|la)\s+(madre|hermana|lora)`,
	`conchud[oa]s?`,
	`put[oa]s?`,
	`trol[oa]s?`,
	`chupapijas?`,
	`pijas?`,
	`porongas?`,
	`vergas?`,
	`chot[oa]s?`,
	`cagada`,
	`cagar(\s+a\s+(palos|trompadas|piñas|tiros))?`,
	`hacer\s+mierda`,
	`manga\s+de\s+(giles|ladrones|chorros|forros|garcas|pelotudos)`,
	`gil(es)?\s+de\s+mierda`,
	`una\s+bosta`,
	`estafadores?`,
	`chorr[oa]s?`,
	`cornud[oa]s?`,
	`tarad[oa]s?`,
	`imbecil(es)?`,
	`idiotas?`,
	`estupid[oa]s?`,
	`negr[oa]s?\s+de\s+mierda`,
	`ojala\s+se\s+mueran`,
	`matar(los|las|te)?`,
}, "|") + `)\b`)

var accentReplacer = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u",
	"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u",
	"ü", "u", "Ü", "u",
	"0", "o", "1", "i", "3", "e", "4", "a", "@", "a", "$", "s",
)

func normalizeForModeration(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	normalized := accentReplacer.Replace(lower)
	var b strings.Builder
	b.Grow(len(normalized))
	var prev rune
	count := 0
	for _, r := range normalized {
		if r == prev {
			count++
			if count >= 2 && r != 'l' && r != 'r' && r != 'c' && r != 's' {
				continue
			}
		} else {
			prev = r
			count = 1
		}
		b.WriteRune(r)
	}
	return b.String()
}

func containsRioplatenseProfanity(content string) (bool, string) {
	normalized := normalizeForModeration(content)
	if match := rioplatenseInsultRegex.FindString(normalized); match != "" {
		return true, fmt.Sprintf("lenguaje ofensivo rioplatense detectado (%s)", match)
	}
	return false, ""
}

func (m *geminiContentModerator) ValidateContent(ctx context.Context, content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	if violated, reason := containsRioplatenseProfanity(trimmed); violated {
		log.Printf("[Moderation] Comentario bloqueado por filtro local rioplatense. Razón: %s | Contenido: %s\n", reason, trimmed)
		return domain.ErrContentPolicyViolation
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("[Moderation] GEMINI_API_KEY no configurada, usando validación local rioplatense")
		return nil
	}

	// Timeout de 5s para permitir que Gemini Flash procese el prompt de clasificación
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := genai.NewClient(ctxTimeout, option.WithAPIKey(apiKey))
	if err != nil {
		log.Printf("[Moderation] Error creando cliente Gemini: %v\n", err)
		return nil
	}
	defer client.Close()

	model := client.GenerativeModel("gemini-2.5-flash")
	model.SetTemperature(0)
	model.ResponseMIMEType = "application/json"
	model.SetMaxOutputTokens(1000)

	prompt := fmt.Sprintf(`Eres un sistema estricto de moderación de contenido para una plataforma de eventos y vida nocturna en Argentina y Uruguay (dialecto rioplatense).
Tu objetivo es identificar si un comentario contiene lenguaje ofensivo, despectivo, insultos, agresiones o lenguaje fuera de lugar que infrinja las normas de convivencia y respeto.

Debe ser clasificado como INFRACCIÓN ("is_violating": true) si contiene:
- Insultos, groserías ofensivas o despectivas en dialecto rioplatense (ejemplos: "de mierda", "pelotudo", "la concha de tu madre", "forro", "ortiba", "mogólico", "garca", "hdp", "hijos de puta", "cagar a palos", "hacer mierda", "manga de giles", etc.).
- Ataques destructivos, descalificaciones agresivas u ofensivas hacia el evento, la organización, los artistas o personas (ejemplo: "evento de mierda", "fiesta de mierda", "son un desastre de mierda", "música de mierda", etc.).
- Amenazas, violencia física, incitación al odio, acoso o discriminación.

Reglas de excepción ("is_violating": false):
- Solo expresiones positivas o jerga de fiesta en sentido festivo/buena onda (ejemplos: "vamos a romper la pista", "estuvo mortal", "alta fiesta", "la rompieron", "terrible fecha").

Responde ÚNICAMENTE con un JSON con esta estructura exacta:
{"is_violating": boolean, "reason": string}

Comentario a evaluar:
"""%s"""`, trimmed)

	resp, err := model.GenerateContent(ctxTimeout, genai.Text(prompt))
	if err != nil {
		log.Printf("[Moderation] Error llamando a Gemini para moderación: %v\n", err)
		return nil // Fallback permisivo ante error de red o timeout
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil || len(resp.Candidates[0].Content.Parts) == 0 {
		return nil
	}

	rawText, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return nil
	}

	jsonRegex := regexp.MustCompile(`\{[\s\S]*\}`)
	match := jsonRegex.FindString(string(rawText))
	if match == "" {
		return nil
	}

	var parsed moderationResponse
	if err := json.Unmarshal([]byte(match), &parsed); err != nil {
		log.Printf("[Moderation] Error decodificando JSON de moderación: %v, raw: %s\n", err, match)
		return nil
	}

	if parsed.IsViolating {
		log.Printf("[Moderation] Comentario bloqueado por infracción de políticas. Razón: %s | Contenido: %s\n", parsed.Reason, trimmed)
		return domain.ErrContentPolicyViolation
	}

	return nil
}
