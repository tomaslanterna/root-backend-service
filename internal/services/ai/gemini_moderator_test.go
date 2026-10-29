package ai

import (
	"context"
	"errors"
	"testing"

	"root-backend-service/internal/core/domain"
)

func TestGeminiContentModeratorRioplatense(t *testing.T) {
	moderator := NewGeminiContentModerator()

	violatingSamples := []string{
		"evento de mierda",
		"son unos pelotudos",
		"la concha de tu madre",
		"son unos garcas y forros",
		"alta cagada de fiesta hdp",
	}

	for _, sample := range violatingSamples {
		err := moderator.ValidateContent(context.Background(), sample)
		if !errors.Is(err, domain.ErrContentPolicyViolation) {
			t.Fatalf("se esperaba ErrContentPolicyViolation para %q, se obtuvo: %v", sample, err)
		}
	}

	// Comentario normal debe pasar
	errSafe := moderator.ValidateContent(context.Background(), "tremenda fiesta la rompieron todos")
	if errSafe != nil {
		t.Fatalf("comentario positivo no debería fallar, se obtuvo: %v", errSafe)
	}
}

