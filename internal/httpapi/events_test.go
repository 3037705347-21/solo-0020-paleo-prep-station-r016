package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type eventView struct {
	Sequence   int    `json:"sequence"`
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	PlanID     string `json:"plan_id"`
	Action     string `json:"action"`
	OccurredAt string `json:"occurred_at"`
	Result     struct {
		SpecimenStatus string `json:"specimen_status"`
		PlanStatus     string `json:"plan_status"`
		Progress       *struct {
			Sequence int `json:"sequence"`
			Percent  int `json:"percent"`
		} `json:"progress"`
	} `json:"result"`
}

func newEventServer() (http.Handler, *httptest.Server) {
	service := station.New(repository.NewMemory())
	handler := NewRouter(service)
	return handler, httptest.NewServer(handler)
}

func postJSON(t *testing.T, client *http.Client, url string, body any) []byte {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	response, err := client.Post(url, "application/json", bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		t.Fatalf("post %s returned %d", url, response.StatusCode)
	}
	var decoded map[string]any
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	raw, _ := json.Marshal(decoded)
	return raw
}

func TestEventEndpointsReturnStableOrderedRecords(t *testing.T) {
	_, server := newEventServer()
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	specimenRaw := postJSON(t, client, server.URL+"/specimens", contracts.RegisterSpecimenRequest{
		CatalogNumber: "PPS-2026-100",
		FieldLabel:    "北坡-07",
		MatrixKind:    "粉砂岩",
	})
	var specimen struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(specimenRaw, &specimen); err != nil {
		t.Fatalf("decode specimen: %v", err)
	}

	planRaw := postJSON(t, client, server.URL+"/specimens/"+specimen.ID+"/plans", contracts.CreatePlanRequest{
		Objective: "暴露主要关节面", Method: "手工显微剥离", TargetExposure: 80,
	})
	var plan struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(planRaw, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	postJSON(t, client, server.URL+"/plans/"+plan.ID+"/approval", contracts.ApprovalRequest{
		ApprovedBy: "curator-lin",
	})

	getEvents := func(url string) []eventView {
		response, err := client.Get(url)
		if err != nil {
			t.Fatalf("get %s: %v", url, err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("get %s returned %d", url, response.StatusCode)
		}
		var body struct {
			Items []eventView `json:"items"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatalf("decode events: %v", err)
		}
		return body.Items
	}

	specimenEvents := getEvents(server.URL + "/specimens/" + specimen.ID + "/events")
	if len(specimenEvents) != 3 {
		t.Fatalf("expected 3 specimen events, got %d", len(specimenEvents))
	}
	wantActions := []string{"specimen_registered", "plan_created", "plan_approved"}
	for i, action := range wantActions {
		if specimenEvents[i].Action != action {
			t.Fatalf("event %d: expected %s, got %s", i, action, specimenEvents[i].Action)
		}
		if specimenEvents[i].Sequence != i+1 {
			t.Fatalf("event %d: unstable sequence %d", i, specimenEvents[i].Sequence)
		}
		if specimenEvents[i].SpecimenID != specimen.ID {
			t.Fatalf("event %d is not linked to specimen", i)
		}
		if specimenEvents[i].OccurredAt == "" {
			t.Fatalf("event %d missing commit timestamp", i)
		}
	}
	if specimenEvents[1].PlanID != plan.ID || specimenEvents[2].PlanID != plan.ID {
		t.Fatal("plan events must carry the plan id")
	}

	planEvents := getEvents(server.URL + "/plans/" + plan.ID + "/events")
	if len(planEvents) != 2 {
		t.Fatalf("expected 2 plan events, got %d", len(planEvents))
	}
	if planEvents[0].Action != "plan_created" || planEvents[1].Action != "plan_approved" {
		t.Fatalf("unexpected plan event order: %+v", planEvents)
	}

	// Repeated reads stay stable.
	specimenEventsAgain := getEvents(server.URL + "/specimens/" + specimen.ID + "/events")
	if specimenEventsAgain[0].ID != specimenEvents[0].ID ||
		specimenEventsAgain[2].ID != specimenEvents[2].ID {
		t.Fatal("event order or identity changed between reads")
	}
}

func TestEventEndpointsNotFound(t *testing.T) {
	_, server := newEventServer()
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	for _, url := range []string{
		server.URL + "/specimens/missing/events",
		server.URL + "/plans/missing/events",
	} {
		response, err := client.Get(url)
		if err != nil {
			t.Fatalf("get %s: %v", url, err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("get %s returned %d, want 404", url, response.StatusCode)
		}
	}
}
