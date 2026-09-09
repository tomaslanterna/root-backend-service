package survey

import (
	"context"
	"root-backend-service/internal/core/domain"
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
	return s.repo.CreateSurvey(ctx, survey, artistRatings)
}
