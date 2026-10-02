package station

import (
	"context"

	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

// ListEventsForSpecimen 按提交顺序返回一份标本的全部业务操作事件。
// 标本不存在时返回 not found，避免对未知对象编造记录。
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

// ListEventsForPlan 按提交顺序返回一份方案的全部业务操作事件。
func (s *Service) ListEventsForPlan(
	ctx context.Context,
	planID string,
) ([]domain.OperationEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var values []domain.OperationEvent
	err := s.repo.Read(func(m *repository.Mutation) error {
		if _, ok := m.GetPlan(planID); !ok {
			return notFoundPlan(planID)
		}
		values = m.EventsForPlan(planID)
		return nil
	})
	return values, err
}
