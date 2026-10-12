package ai

import (
	"context"
	"errors"
	"os"
	"testing"

	"root-backend-service/internal/core/domain"
)

func TestGeminiContentModeratorRioplatense(t *testing.T) {
	if os.Getenv("GEMINI_API_KEY") == "" {
		t.Skip("GEMINI_API_KEY no configurada, omitiendo test de integración con Gemini")
	}

	moderator := NewGeminiContentModerator()

	// "evento de mierda" debe ser bloqueado
	err := moderator.ValidateContent(context.Background(), "evento de mierda")
	if !errors.Is(err, domain.ErrContentPolicyViolation) {
		t.Fatalf("se esperaba ErrContentPolicyViolation para 'evento de mierda', se obtuvo: %v", err)
	}

	// Comentario normal debe pasar
	errSafe := moderator.ValidateContent(context.Background(), "tremenda fiesta la rompieron todos")
	if errSafe != nil {
		t.Fatalf("comentario positivo no debería fallar, se obtuvo: %v", errSafe)
	}
}
