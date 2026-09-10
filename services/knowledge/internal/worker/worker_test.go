package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HappyLadySauce/Knowledge-Core/services/knowledge/internal/domain"
)

type publicationRepositoryStub struct{ staged, promoted, completed, retried, parked int }

func (s *publicationRepositoryStub) MarkPublicationReferencesStaged(context.Context, string) error {
	s.staged++
	return nil
}
func (s *publicationRepositoryStub) PromotePublication(context.Context, domain.PublicationReferenceJob) error {
	s.promoted++
	return nil
}
func (s *publicationRepositoryStub) CompletePublicationReferenceJob(context.Context, string, bool) error {
	s.completed++
	return nil
}
func (s *publicationRepositoryStub) RetryPublicationReferenceJob(context.Context, string, string, int) error {
	s.retried++
	return nil
}
func (s *publicationRepositoryStub) ParkPublicationReferenceJob(context.Context, string, string, bool) error {
	s.parked++
	return nil
}

type publicationReferencesStub struct {
	stageErr                   error
	staged, finalized, cleared int
}

func (s *publicationReferencesStub) Stage(context.Context, domain.PublicationReferenceJob) error {
	s.staged++
	return s.stageErr
}
func (s *publicationReferencesStub) Finalize(context.Context, domain.PublicationReferenceJob) error {
	s.finalized++
	return nil
}
func (s *publicationReferencesStub) Clear(context.Context, domain.PublicationReferenceJob) error {
	s.cleared++
	return nil
}

func TestPublicationReferenceStagesAreRestartSafe(t *testing.T) {
	repository := &publicationRepositoryStub{}
	references := &publicationReferencesStub{}
	w := &Worker{attachments: references}
	for _, job := range []domain.PublicationReferenceJob{
		{ID: "1", Action: "publish", State: "pending", Attempts: 1},
		{ID: "2", Action: "clear", State: "pending", Attempts: 1},
	} {
		if err := w.handlePublicationReferenceJobForTest(context.Background(), repository, job); err != nil {
			t.Fatal(err)
		}
	}
	if references.staged != 1 || references.finalized != 1 || references.cleared != 1 || repository.staged != 1 || repository.promoted != 1 || repository.completed != 2 {
		t.Fatalf("unexpected calls: refs=%+v repo=%+v", references, repository)
	}
}

func TestPublicationReferenceFailureRetriesThenParks(t *testing.T) {
	repository := &publicationRepositoryStub{}
	references := &publicationReferencesStub{stageErr: errors.New("unavailable")}
	w := &Worker{attachments: references}
	if err := w.handlePublicationReferenceJobForTest(context.Background(), repository, domain.PublicationReferenceJob{ID: "1", Action: "publish", State: "pending", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if repository.retried != 1 {
		t.Fatalf("retries = %d", repository.retried)
	}
	if err := w.handlePublicationReferenceJobForTest(context.Background(), repository, domain.PublicationReferenceJob{ID: "1", Action: "publish", State: "pending", Attempts: 8}); err != nil {
		t.Fatal(err)
	}
	if repository.parked != 1 {
		t.Fatalf("parks = %d", repository.parked)
	}
}

func TestBoundedBackoff(t *testing.T) {
	if boundedBackoff(1) != time.Second || boundedBackoff(99) != 128*time.Second {
		t.Fatal("unexpected backoff")
	}
}
