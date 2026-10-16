package survey

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"root-backend-service/internal/core/domain"
	"strings"
	"unicode/utf8"
)

type SurveyRepository interface {
	CreateSurvey(ctx context.Context, survey *domain.EventSurvey, artistRatings []domain.SurveyArtistRating) error
}

type SurveyService struct {
	repo SurveyRepository
}

func NewSurveyService(repo SurveyRepository) *SurveyService {
	return &SurveyService{repo: repo}
}

func (s *SurveyService) SubmitSurvey(ctx context.Context, survey *domain.EventSurvey, artistRatings []domain.SurveyArtistRating) error {
	if survey.GeneralRating < 0 || survey.GeneralRating > 5 {
		return domain.ErrInvalidSurvey
	}
	for _, value := range []*int{survey.OrganizationRating, survey.VibeRating, survey.SoundVisualRating, survey.PricingRating} {
		if value != nil && (*value < 0 || *value > 5) {
			return domain.ErrInvalidSurvey
		}
	}
	if survey.Comment != nil {
		trimmed := strings.TrimSpace(*survey.Comment)
		if utf8.RuneCountInString(trimmed) > 1000 {
			return fmt.Errorf("%w: maximum 1000 characters", domain.ErrInvalidSurvey)
		}
		survey.Comment = &trimmed
	}
	if survey.SpaceRating != nil && *survey.SpaceRating != "crowded" && *survey.SpaceRating != "good" && *survey.SpaceRating != "spacious" {
		return domain.ErrInvalidSurvey
	}
	seen := map[string]bool{}
	for _, rating := range artistRatings {
		if _, err := uuid.Parse(rating.ArtistID); err != nil || rating.Rating < 0 || rating.Rating > 5 || seen[rating.ArtistID] {
			return domain.ErrInvalidSurvey
		}
		seen[rating.ArtistID] = true
	}
	return s.repo.CreateSurvey(ctx, survey, artistRatings)
}
