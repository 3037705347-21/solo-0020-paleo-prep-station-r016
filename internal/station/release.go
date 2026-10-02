package station

import (
	"context"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func (s *Service) ReleaseSpecimen(
	ctx context.Context,
	specimenID string,
	request contracts.ReleaseRequest,
) (domain.ReleaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReleaseRecord{}, err
	}

	var created domain.ReleaseRecord
	err := s.repo.Mutate(func(m *repository.Mutation) error {
		stamp := s.now()
		specimen, ok := m.GetSpecimen(specimenID)
		if !ok {
			return notFoundSpecimen(specimenID)
		}
		if m.HasOpenPlan(specimenID) {
			return contracts.Conflict("open_plan_blocks_release", "存在未结束方案，不能办理研究放行")
		}
		if _, exists := m.ReleaseForSpecimen(specimenID); exists {
			return contracts.Conflict("release_exists", "该标本已经存在放行记录")
		}
		record, err := domain.NewReleaseRecord(
			s.nextID("release"),
			specimen,
			request.Purpose,
			request.Recipient,
			request.ApprovedBy,
			stamp,
		)
		if err != nil {
			return err
		}
		if err := specimen.MarkAvailable(stamp); err != nil {
			return err
		}
		event, err := domain.NewOperationEvent(
			s.nextID("event"),
			domain.EventSpecimenReleased,
			specimen.ID,
			"",
			stamp,
			domain.EventResult{
				SpecimenStatus: specimen.Status,
				ReleaseID:      record.ID,
			},
		)
		if err != nil {
			return err
		}
		m.PutRelease(record)
		m.PutSpecimen(specimen)
		m.AppendEvent(event)
		created = record
		return nil
	})
	return created, err
}
