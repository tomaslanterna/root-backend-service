package survey

import (
	"context"
	"errors"
	"root-backend-service/internal/core/domain"
	"strings"
	"testing"
)

type surveyRepoStub struct {
	calls int
	err   error
}

func (r *surveyRepoStub) CreateSurvey(context.Context, *domain.EventSurvey, []domain.SurveyArtistRating) error {
	r.calls++
	return r.err
}
func TestSurveyValidation(t *testing.T) {
	for _, score := range []int{0, 1, 5, -1, 6} {
		r := &surveyRepoStub{}
		s := NewSurveyService(r)
		err := s.SubmitSurvey(context.Background(), &domain.EventSurvey{GeneralRating: score}, nil)
		if score >= 0 && score <= 5 {
			if err != nil || r.calls != 1 {
				t.Fatalf("valid score %d: %v", score, err)
			}
		} else if !errors.Is(err, domain.ErrInvalidSurvey) || r.calls != 0 {
			t.Fatalf("invalid score %d: %v", score, err)
		}
	}
	r := &surveyRepoStub{}
	s := NewSurveyService(r)
	comment := "  buena noche  "
	record := &domain.EventSurvey{GeneralRating: 0, Comment: &comment}
	if err := s.SubmitSurvey(context.Background(), record, nil); err != nil || *record.Comment != "buena noche" {
		t.Fatal("comment not trimmed")
	}
	comment = strings.Repeat("á", 1001)
	record.Comment = &comment
	if err := s.SubmitSurvey(context.Background(), record, nil); !errors.Is(err, domain.ErrInvalidSurvey) {
		t.Fatal("oversized Unicode review accepted")
	}
	r.err = domain.ErrAttendanceRequired
	if err := s.SubmitSurvey(context.Background(), &domain.EventSurvey{GeneralRating: 5}, nil); !errors.Is(err, domain.ErrAttendanceRequired) {
		t.Fatal("attendance error swallowed")
	}
}
