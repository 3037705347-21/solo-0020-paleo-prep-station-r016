package domain

import (
	"time"

	"example.com/paleo-prep-station/internal/contracts"
)

// EventAction 标识一次成功业务变更所执行的动作。
type EventAction string

const (
	EventSpecimenRegistered EventAction = "specimen_registered"
	EventPlanCreated        EventAction = "plan_created"
	EventPlanApproved       EventAction = "plan_approved"
	EventProgressLogged     EventAction = "progress_logged"
	EventSpecimenReleased   EventAction = "specimen_released"
)

// EventResult 记录一次提交后与动作相关的关键状态结果。
// 指针类型字段在与该动作无关时省略，便于区分“零值”与“未产生该结果”。
type EventResult struct {
	SpecimenStatus SpecimenStatus `json:"specimen_status"`
	PlanStatus     PlanStatus     `json:"plan_status,omitempty"`
	Percent        *int           `json:"percent,omitempty"`
	Sequence       *int           `json:"progress_sequence,omitempty"`
	ReleaseID      string         `json:"release_id,omitempty"`
}

// OperationEvent 是一次成功业务变更的不可覆盖记录。
// 事件只能追加，顺序由仓储在提交时分配的全局 Sequence 保证。
type OperationEvent struct {
	ID         string      `json:"id"`
	Sequence   int64       `json:"sequence"`
	SpecimenID string      `json:"specimen_id"`
	PlanID     string      `json:"plan_id,omitempty"`
	Action     EventAction `json:"action"`
	OccurredAt time.Time   `json:"occurred_at"`
	Result     EventResult `json:"result"`
}

// NewOperationEvent 构造一条业务操作事件。事件必须定位到标本，
// 方案相关动作还要携带方案标识，并反映提交时刻与关键状态结果。
func NewOperationEvent(
	id string,
	action EventAction,
	specimenID string,
	planID string,
	occurredAt time.Time,
	result EventResult,
) (OperationEvent, error) {
	if id == "" {
		return OperationEvent{}, contracts.Internal("missing_generated_id", "操作事件标识未生成", nil)
	}
	if !validEventAction(action) {
		return OperationEvent{}, contracts.Internal("unknown_event_action", "未知的操作事件动作", nil)
	}
	specimen, err := requiredText("标本标识", specimenID, 64)
	if err != nil {
		return OperationEvent{}, err
	}
	if action != EventSpecimenRegistered && action != EventSpecimenReleased {
		if _, err := requiredText("方案标识", planID, 64); err != nil {
			return OperationEvent{}, err
		}
	}
	if string(result.SpecimenStatus) == "" {
		return OperationEvent{}, contracts.Internal("missing_event_result", "操作事件缺少标本状态结果", nil)
	}
	if action == EventProgressLogged {
		if result.Percent == nil || result.Sequence == nil {
			return OperationEvent{}, contracts.Internal("missing_event_result", "进度事件缺少进度结果", nil)
		}
		if result.PlanStatus != PlanActive && result.PlanStatus != PlanCompleted {
			return OperationEvent{}, contracts.Internal("missing_event_result", "进度事件缺少方案状态结果", nil)
		}
	}
	if action == EventSpecimenReleased && result.ReleaseID == "" {
		return OperationEvent{}, contracts.Internal("missing_event_result", "放行事件缺少放行记录标识", nil)
	}
	return OperationEvent{
		ID:         id,
		SpecimenID: specimen,
		PlanID:     planID,
		Action:     action,
		OccurredAt: occurredAt.UTC(),
		Result:     result,
	}, nil
}

func validEventAction(action EventAction) bool {
	switch action {
	case EventSpecimenRegistered,
		EventPlanCreated,
		EventPlanApproved,
		EventProgressLogged,
		EventSpecimenReleased:
		return true
	default:
		return false
	}
}
