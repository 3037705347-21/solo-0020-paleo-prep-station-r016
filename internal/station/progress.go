package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) LogProgress(
	ctx context.Context,
	planID string,
	request contracts.ProgressRequest,
) (domain.ProgressEntry, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProgressEntry{}, err
	}

	var created domain.ProgressEntry
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		stamp := s.now()
		plan, ok := m.GetPlan(planID)
		if !ok {
			return notFoundPlan(planID)
		}
		specimen, ok := m.GetSpecimen(plan.SpecimenID)
		if !ok {
			return notFoundSpecimen(plan.SpecimenID)
		}
		previous := m.ProgressForPlan(planID)
		entry, err := domain.NewProgressEntry(
			s.nextID("progress"),
			plan,
			previous,
			request.Stage,
			request.Percent,
			request.Note,
			request.RecordedBy,
			stamp,
		)
		if err != nil {
			return err
		}
		if plan.Status == domain.PlanApproved {
			if err := plan.Begin(stamp); err != nil {
				return err
			}
		}
		if request.Percent == 100 {
			if err := plan.Complete(stamp); err != nil {
				return err
			}
			if err := specimen.MarkStabilized(stamp); err != nil {
				return err
			}
		}
		percent := entry.Percent
		sequence := entry.Sequence
		event, err := domain.NewOperationEvent(
			s.nextID("event"),
			domain.EventProgressLogged,
			specimen.ID,
			plan.ID,
			stamp,
			domain.EventResult{
				SpecimenStatus: specimen.Status,
				PlanStatus:     plan.Status,
				Percent:        &percent,
				Sequence:       &sequence,
			},
		)
		if err != nil {
			return err
		}
		m.AppendProgress(entry)
		m.PutPlan(plan)
		m.PutSpecimen(specimen)
		m.AppendEvent(event)
		created = entry
		return nil
	})
	return created, err
}
