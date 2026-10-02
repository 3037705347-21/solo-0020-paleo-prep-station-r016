package station

import (
	"context"
	"time"

	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// recordEvent appends the change record for a successful business mutation.
// It is invoked inside the same transaction as the entity changes, so a
// construction failure aborts the commit: the event and the facts it describes
// always appear or disappear together.
func (s *Service) recordEvent(
	m *repository.Mutation,
	specimen domain.Specimen,
	action domain.OperationAction,
	plan *domain.PrepPlan,
	progress *domain.ProgressEntry,
	release *domain.ReleaseRecord,
	committedAt time.Time,
) error {
	event, err := domain.NewOperationEvent(
		s.nextID("event"),
		specimen,
		action,
		plan,
		progress,
		release,
		committedAt,
	)
	if err != nil {
		return err
	}
	m.AppendEvent(event)
	return nil
}

func (s *Service) ListEventsForSpecimen(
	ctx context.Context,
	specimenID string,
) ([]domain.OperationEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.OperationEvent
	err := s.repo.Read(func(m *repository.Mutation) error {
		if _, ok := m.GetSpecimen(specimenID); !ok {
			return notFoundSpecimen(specimenID)
		}
		values = m.EventsForSpecimen(specimenID)
		return nil
	})
	return values, err
}

func (s *Service) ListEventsForPlan(
	ctx context.Context,
	planID string,
) ([]domain.OperationEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.OperationEvent
	err := s.repo.Read(func(m *repository.Mutation) error {
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		values = m.EventsForPlan(plan.ID)
		return nil
	})
	return values, err
}
