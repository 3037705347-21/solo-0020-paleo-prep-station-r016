package station

import (
	"context"
	"testing"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/domain"
	"example.com/paleo-prep-station/internal/repository"
)

func newTestService() *Service {
	s := New(repository.NewMemory())
	clock := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	return s
}

func registerForTest(t *testing.T, s *Service, catalog string) domain.Specimen {
	t.Helper()
	specimen, err := s.RegisterSpecimen(context.Background(), contracts.RegisterSpecimenRequest{
		CatalogNumber: catalog,
		FieldLabel:    "北坡-07",
		MatrixKind:    "粉砂岩",
	})
	if err != nil {
		t.Fatalf("register specimen: %v", err)
	}
	return specimen
}

func approveForTest(t *testing.T, s *Service, planID string) {
	t.Helper()
	if _, err := s.ApprovePlan(context.Background(), planID, contracts.ApprovalRequest{
		ApprovedBy: "curator-lin",
	}); err != nil {
		t.Fatalf("approve plan: %v", err)
	}
}

func TestRegisterEmitsOrderedEvent(t *testing.T) {
	s := newTestService()
	specimen := registerForTest(t, s, "PPS-2026-001")

	events, err := s.ListEventsForSpecimen(context.Background(), specimen.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	event := events[0]
	if event.Sequence != 1 || event.ID == "" {
		t.Fatalf("event not numbered/identified: %+v", event)
	}
	if event.SpecimenID != specimen.ID || event.PlanID != "" {
		t.Fatalf("event subject mismatch: %+v", event)
	}
	if event.Action != domain.ActionSpecimenRegistered {
		t.Fatalf("expected specimen_registered, got %s", event.Action)
	}
	if event.Result.SpecimenStatus != domain.SpecimenRegistered {
		t.Fatalf("expected registered result, got %s", event.Result.SpecimenStatus)
	}
	if event.OccurredAt != s.now() {
		t.Fatalf("expected commit timestamp %v, got %v", s.now(), event.OccurredAt)
	}
}

func TestFullWorkflowEventChain(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	specimen := registerForTest(t, s, "PPS-2026-002")

	plan, err := s.CreatePlan(ctx, specimen.ID, contracts.CreatePlanRequest{
		Objective:      "暴露主要关节面",
		Method:         "手工显微剥离",
		TargetExposure: 80,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	approveForTest(t, s, plan.ID)
	if _, err := s.LogProgress(ctx, plan.ID, contracts.ProgressRequest{
		Stage: "矩阵剥离", Percent: 35, RecordedBy: "preparator-chen",
	}); err != nil {
		t.Fatalf("log progress: %v", err)
	}
	if _, err := s.LogProgress(ctx, plan.ID, contracts.ProgressRequest{
		Stage: "表面加固", Percent: 100, RecordedBy: "preparator-chen",
	}); err != nil {
		t.Fatalf("log final progress: %v", err)
	}
	if _, err := s.ReleaseSpecimen(ctx, specimen.ID, contracts.ReleaseRequest{
		Purpose: "比较解剖研究", Recipient: "地层研究组", ApprovedBy: "keeper-zhou",
	}); err != nil {
		t.Fatalf("release: %v", err)
	}

	specimenEvents, err := s.ListEventsForSpecimen(ctx, specimen.ID)
	if err != nil {
		t.Fatalf("list specimen events: %v", err)
	}
	if len(specimenEvents) != 6 {
		t.Fatalf("expected 6 events, got %d", len(specimenEvents))
	}

	want := []struct {
		action     domain.OperationAction
		status     domain.SpecimenStatus
		planStatus domain.PlanStatus
		percent    int
	}{
		{domain.ActionSpecimenRegistered, domain.SpecimenRegistered, "", -1},
		{domain.ActionPlanCreated, domain.SpecimenPlanned, domain.PlanDraft, -1},
		{domain.ActionPlanApproved, domain.SpecimenPreparing, domain.PlanApproved, -1},
		{domain.ActionProgressLogged, domain.SpecimenPreparing, domain.PlanActive, 35},
		{domain.ActionProgressLogged, domain.SpecimenStabilized, domain.PlanCompleted, 100},
		{domain.ActionSpecimenReleased, domain.SpecimenAvailable, "", -1},
	}
	for i, expected := range want {
		event := specimenEvents[i]
		if event.Sequence != i+1 {
			t.Fatalf("event %d: expected sequence %d, got %d", i, i+1, event.Sequence)
		}
		if event.Action != expected.action {
			t.Fatalf("event %d: expected action %s, got %s", i, expected.action, event.Action)
		}
		if event.Result.SpecimenStatus != expected.status {
			t.Fatalf("event %d: expected specimen status %s, got %s", i, expected.status, event.Result.SpecimenStatus)
		}
		if expected.planStatus != "" && event.Result.PlanStatus != expected.planStatus {
			t.Fatalf("event %d: expected plan status %s, got %s", i, expected.planStatus, event.Result.PlanStatus)
		}
		if expected.percent >= 0 {
			if event.Result.Progress == nil || event.Result.Progress.Percent != expected.percent {
				t.Fatalf("event %d: expected progress percent %d, got %+v", i, expected.percent, event.Result.Progress)
			}
		}
	}

	last := specimenEvents[5]
	if last.Result.ReleaseID == "" {
		t.Fatal("release event must carry the release record id")
	}

	// Plan-scoped view contains only the four plan-affecting events and is
	// returned in the same stable order.
	planEvents, err := s.ListEventsForPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("list plan events: %v", err)
	}
	if len(planEvents) != 4 {
		t.Fatalf("expected 4 plan events, got %d", len(planEvents))
	}
	if planEvents[0].Action != domain.ActionPlanCreated || planEvents[3].Action != domain.ActionProgressLogged {
		t.Fatalf("unexpected plan event order: %+v", planEvents)
	}
	if planEvents[3].Result.Progress == nil || planEvents[3].Result.Progress.Sequence != 2 {
		t.Fatalf("final progress result should reference sequence 2: %+v", planEvents[3].Result)
	}
}

func TestZeroPercentResultIsPreserved(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	specimen := registerForTest(t, s, "PPS-2026-003")
	plan, err := s.CreatePlan(ctx, specimen.ID, contracts.CreatePlanRequest{
		Objective: "初始清理", Method: "干法刷除", TargetExposure: 50,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	approveForTest(t, s, plan.ID)
	if _, err := s.LogProgress(ctx, plan.ID, contracts.ProgressRequest{
		Stage: "初始清理", Percent: 0, RecordedBy: "preparator-chen",
	}); err != nil {
		t.Fatalf("log zero percent progress: %v", err)
	}
	planEvents, err := s.ListEventsForPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("list plan events: %v", err)
	}
	progressEvent := planEvents[len(planEvents)-1]
	if progressEvent.Result.Progress == nil {
		t.Fatal("zero percent progress result must be present, not omitted")
	}
	if progressEvent.Result.Progress.Percent != 0 {
		t.Fatalf("expected 0 percent, got %d", progressEvent.Result.Progress.Percent)
	}
}

func TestRejectedMutationsAppendNoEvent(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	specimen := registerForTest(t, s, "PPS-2026-004")

	// Duplicate registration: conflict, no extra event on either catalog.
	if _, err := s.RegisterSpecimen(ctx, contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-2026-004", FieldLabel: "北坡-08", MatrixKind: "泥岩",
	}); err == nil {
		t.Fatal("expected duplicate catalog conflict")
	}

	plan, err := s.CreatePlan(ctx, specimen.ID, contracts.CreatePlanRequest{
		Objective: "暴露关节", Method: "手工剥离", TargetExposure: 80,
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}

	// Approving twice: the second approval is a rejected repeat.
	approveForTest(t, s, plan.ID)
	if _, err := s.ApprovePlan(ctx, plan.ID, contracts.ApprovalRequest{
		ApprovedBy: "curator-lin",
	}); err == nil {
		t.Fatal("expected repeated approval conflict")
	}

	// Progress below the previous record: rejected.
	if _, err := s.LogProgress(ctx, plan.ID, contracts.ProgressRequest{
		Stage: "矩阵剥离", Percent: 40, RecordedBy: "preparator-chen",
	}); err != nil {
		t.Fatalf("log progress: %v", err)
	}
	if _, err := s.LogProgress(ctx, plan.ID, contracts.ProgressRequest{
		Stage: "倒退", Percent: 20, RecordedBy: "preparator-chen",
	}); err == nil {
		t.Fatal("expected progress regression conflict")
	}

	// Releasing before stabilized: rejected.
	if _, err := s.ReleaseSpecimen(ctx, specimen.ID, contracts.ReleaseRequest{
		Purpose: "研究", Recipient: "研究组", ApprovedBy: "keeper-zhou",
	}); err == nil {
		t.Fatal("expected release conflict")
	}

	events, err := s.ListEventsForSpecimen(ctx, specimen.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	want := []domain.OperationAction{
		domain.ActionSpecimenRegistered,
		domain.ActionPlanCreated,
		domain.ActionPlanApproved,
		domain.ActionProgressLogged,
	}
	if len(events) != len(want) {
		t.Fatalf("expected %d events after rejections, got %d", len(want), len(events))
	}
	for i, action := range want {
		if events[i].Action != action {
			t.Fatalf("event %d: expected %s, got %s", i, action, events[i].Action)
		}
	}

	// The specimen and plan snapshots keep their original business results.
	stored, err := s.GetSpecimen(ctx, specimen.ID)
	if err != nil {
		t.Fatalf("get specimen: %v", err)
	}
	if stored.Status != domain.SpecimenPreparing || stored.Version != 3 {
		t.Fatalf("specimen business result changed after rejections: %+v", stored)
	}
	snapshot, err := s.GetPlan(ctx, plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if len(snapshot.Progress) != 1 || snapshot.Plan.Status != domain.PlanActive {
		t.Fatalf("plan business result changed after rejections: %+v", snapshot)
	}
}

func TestEventSequencesAreGlobalAndGaplessAcrossSpecimens(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	specimenA := registerForTest(t, s, "PPS-2026-010")
	specimenB := registerForTest(t, s, "PPS-2026-011")
	specimenC := registerForTest(t, s, "PPS-2026-012")

	eventsA, err := s.ListEventsForSpecimen(ctx, specimenA.ID)
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	eventsB, err := s.ListEventsForSpecimen(ctx, specimenB.ID)
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	eventsC, err := s.ListEventsForSpecimen(ctx, specimenC.ID)
	if err != nil {
		t.Fatalf("list C: %v", err)
	}
	if eventsA[0].Sequence != 1 || eventsB[0].Sequence != 2 || eventsC[0].Sequence != 3 {
		t.Fatalf("expected global gapless sequences 1,2,3, got %d,%d,%d",
			eventsA[0].Sequence, eventsB[0].Sequence, eventsC[0].Sequence)
	}
}

func TestEventReadsReturnNotFoundForUnknownSubjects(t *testing.T) {
	s := newTestService()
	ctx := context.Background()
	if _, err := s.ListEventsForSpecimen(ctx, "missing"); !isConflictKind(err, contracts.KindNotFound) {
		t.Fatalf("expected not found for unknown specimen, got %v", err)
	}
	if _, err := s.ListEventsForPlan(ctx, "missing"); !isConflictKind(err, contracts.KindNotFound) {
		t.Fatalf("expected not found for unknown plan, got %v", err)
	}
}

func isConflictKind(err error, kind contracts.ErrorKind) bool {
	appErr, ok := contracts.AsError(err)
	return ok && appErr.Kind == kind
}
