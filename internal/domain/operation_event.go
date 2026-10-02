package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

// OperationAction names a successful business change that can be recorded as an event.
type OperationAction string

const (
	ActionSpecimenRegistered OperationAction = "specimen_registered"
	ActionPlanCreated        OperationAction = "plan_created"
	ActionPlanApproved       OperationAction = "plan_approved"
	ActionProgressLogged     OperationAction = "progress_logged"
	ActionSpecimenReleased   OperationAction = "specimen_released"
)

// EventResult carries the key state produced by the change in the same commit.
// Pointers are used for optional facts so that a zero percent completion is
// distinguishable from "percent not involved in this action".
type EventResult struct {
	SpecimenStatus SpecimenStatus  `json:"specimen_status"`
	PlanStatus     PlanStatus      `json:"plan_status,omitempty"`
	Progress       *ProgressResult `json:"progress,omitempty"`
	ReleaseID      string          `json:"release_id,omitempty"`
}

// ProgressResult is the progress-specific part of a progress_logged event.
type ProgressResult struct {
	EntryID  string `json:"entry_id"`
	Sequence int    `json:"sequence"`
	Percent  int    `json:"percent"`
}

// OperationEvent is an independently readable, ordered business change record.
// Events are append-only: once committed they are never overwritten or deleted.
type OperationEvent struct {
	Sequence   int             `json:"sequence"`
	ID         string          `json:"id"`
	SpecimenID string          `json:"specimen_id"`
	PlanID     string          `json:"plan_id,omitempty"`
	Action     OperationAction `json:"action"`
	OccurredAt time.Time       `json:"occurred_at"`
	Result     EventResult     `json:"result"`
}

// NewOperationEvent builds the change record for a business mutation. The
// result is derived from the entities as they stand at commit time, so the
// event always reflects what the same commit actually persisted.
func NewOperationEvent(
	id string,
	specimen Specimen,
	action OperationAction,
	plan *PrepPlan,
	progress *ProgressEntry,
	release *ReleaseRecord,
	occurredAt time.Time,
) (OperationEvent, error) {
	if id == "" {
		return OperationEvent{}, contracts.Internal("missing_generated_id", "操作记录标识未生成", nil)
	}
	if specimen.ID == "" {
		return OperationEvent{}, contracts.Internal("missing_event_subject", "操作记录缺少标本", nil)
	}
	if !isKnownAction(action) {
		return OperationEvent{}, contracts.Internal("unknown_operation_action", "未知的业务动作", nil)
	}

	result := EventResult{SpecimenStatus: specimen.Status}
	switch action {
	case ActionSpecimenRegistered:
		// No plan or auxiliary fact.
	case ActionPlanCreated, ActionPlanApproved:
		if plan == nil || plan.SpecimenID != specimen.ID {
			return OperationEvent{}, contracts.Internal("missing_event_plan", "方案动作缺少匹配的修复方案", nil)
		}
		result.PlanStatus = plan.Status
	case ActionProgressLogged:
		if plan == nil || plan.SpecimenID != specimen.ID {
			return OperationEvent{}, contracts.Internal("missing_event_plan", "进度动作缺少匹配的修复方案", nil)
		}
		if progress == nil || progress.PlanID != plan.ID {
			return OperationEvent{}, contracts.Internal("missing_event_progress", "进度动作缺少匹配的进度记录", nil)
		}
		result.PlanStatus = plan.Status
		result.Progress = &ProgressResult{
			EntryID:  progress.ID,
			Sequence: progress.Sequence,
			Percent:  progress.Percent,
		}
	case ActionSpecimenReleased:
		if release == nil || release.SpecimenID != specimen.ID {
			return OperationEvent{}, contracts.Internal("missing_event_release", "放行动作缺少匹配的放行记录", nil)
		}
		result.ReleaseID = release.ID
	}

	planID := ""
	if plan != nil {
		planID = plan.ID
	}
	return OperationEvent{
		ID:         id,
		SpecimenID: specimen.ID,
		PlanID:     planID,
		Action:     action,
		OccurredAt: occurredAt.UTC(),
		Result:     result,
	}, nil
}

func isKnownAction(action OperationAction) bool {
	switch action {
	case ActionSpecimenRegistered,
		ActionPlanCreated,
		ActionPlanApproved,
		ActionProgressLogged,
		ActionSpecimenReleased:
		return true
	default:
		return false
	}
}
