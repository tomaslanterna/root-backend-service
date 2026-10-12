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

func (m *geminiContentModerator) ValidateContent(ctx context.Context, content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("[Moderation] GEMINI_API_KEY no configurada, omitiendo moderación por IA")
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
