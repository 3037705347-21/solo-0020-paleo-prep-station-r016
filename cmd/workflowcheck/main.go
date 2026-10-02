package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"example.com/paleo-prep-station/internal/contracts"
	"example.com/paleo-prep-station/internal/httpapi"
	"example.com/paleo-prep-station/internal/repository"
	"example.com/paleo-prep-station/internal/station"
)

type specimenView struct {
	ID            string `json:"id"`
	CatalogNumber string `json:"catalog_number"`
	Status        string `json:"status"`
}

type planView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
	Status     string `json:"status"`
}

type planSnapshot struct {
	Plan     planView `json:"plan"`
	Progress []struct {
		Percent int `json:"percent"`
	} `json:"progress"`
}

type releaseView struct {
	ID         string `json:"id"`
	SpecimenID string `json:"specimen_id"`
}

type eventView struct {
	ID         string `json:"id"`
	Sequence   int64  `json:"sequence"`
	SpecimenID string `json:"specimen_id"`
	PlanID     string `json:"plan_id"`
	Action     string `json:"action"`
	OccurredAt string `json:"occurred_at"`
	Result     struct {
		SpecimenStatus string `json:"specimen_status"`
		PlanStatus     string `json:"plan_status"`
		Percent        *int   `json:"percent"`
		ProgressSeq    *int   `json:"progress_sequence"`
		ReleaseID      string `json:"release_id"`
	} `json:"result"`
}

type eventList struct {
	Items []eventView `json:"items"`
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to check")
	flag.Parse()

	if err := run(*workflow); err != nil {
		fmt.Fprintln(os.Stderr, "workflow check failed:", err)
		os.Exit(1)
	}
	fmt.Printf("workflow check passed: %s\n", *workflow)
}

func run(workflow string) error {
	handler := httpapi.NewRouter(station.New(repository.NewMemory()))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}

	switch workflow {
	case "register-specimen":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		if specimen.Status != "registered" {
			return fmt.Errorf("expected registered status, got %s", specimen.Status)
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.ID != specimen.ID {
			return errors.New("registered specimen cannot be read back")
		}
		return nil

	case "approve-prep-plan":
		specimen, err := registerSpecimen(client, server.URL)
		if err != nil {
			return err
		}
		plan, err := createPlan(client, server.URL, specimen.ID)
		if err != nil {
			return err
		}
		var approved planView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/plans/"+plan.ID+"/approval",
			contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
			http.StatusOK,
			&approved,
		); err != nil {
			return err
		}
		if approved.Status != "approved" {
			return fmt.Errorf("expected approved plan, got %s", approved.Status)
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "preparing" {
			return fmt.Errorf("expected preparing specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "log-preparation":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 35, "矩阵剥离"); err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var snapshot planSnapshot
		if err := requestJSON(client, http.MethodGet, server.URL+"/plans/"+plan.ID, nil, http.StatusOK, &snapshot); err != nil {
			return err
		}
		if snapshot.Plan.Status != "completed" || len(snapshot.Progress) != 2 {
			return errors.New("plan did not complete with exactly two progress entries")
		}
		var fetchedSpecimen specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetchedSpecimen); err != nil {
			return err
		}
		if fetchedSpecimen.Status != "stabilized" {
			return fmt.Errorf("expected stabilized specimen, got %s", fetchedSpecimen.Status)
		}
		return nil

	case "release-for-study":
		specimen, plan, err := completedPlanSetup(client, server.URL)
		if err != nil {
			return err
		}
		if err := recordProgress(client, server.URL, plan.ID, 100, "表面加固"); err != nil {
			return err
		}
		var release releaseView
		if err := requestJSON(
			client,
			http.MethodPost,
			server.URL+"/specimens/"+specimen.ID+"/release",
			contracts.ReleaseRequest{
				Purpose:    "比较解剖研究",
				Recipient:  "地层研究组",
				ApprovedBy: "keeper-zhou",
			},
			http.StatusCreated,
			&release,
		); err != nil {
			return err
		}
		if release.SpecimenID != specimen.ID {
			return errors.New("release record references the wrong specimen")
		}
		var fetched specimenView
		if err := requestJSON(client, http.MethodGet, server.URL+"/specimens/"+specimen.ID, nil, http.StatusOK, &fetched); err != nil {
			return err
		}
		if fetched.Status != "available" {
			return fmt.Errorf("expected available specimen, got %s", fetched.Status)
		}
		return nil

	case "operation-events":
		return checkOperationEvents(client, server.URL)
	default:
		return fmt.Errorf("unknown workflow %q", workflow)
	}
}

func checkOperationEvents(client *http.Client, baseURL string) error {
	// 非法登记（400）不能产生任何事件。
	badRegister := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{CatalogNumber: "PPS-BAD", FieldLabel: "北坡-X"},
		http.StatusBadRequest,
		nil,
	)
	if badRegister != nil {
		return badRegister
	}

	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return err
	}

	// 重复登记（409）不能追加成功事件。
	dup := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: "PPS-2026-001",
			FieldLabel:    "北坡-07",
			MatrixKind:    "粉砂岩",
		},
		http.StatusConflict,
		nil,
	)
	if dup != nil {
		return dup
	}

	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return err
	}

	// 草稿不能重复创建方案（409），且审批必须恰好一次成功。
	dupPlan := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "重复方案",
			Method:         "手工显微剥离",
			TargetExposure: 60,
		},
		http.StatusConflict,
		nil,
	)
	if dupPlan != nil {
		return dupPlan
	}
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		nil,
	); err != nil {
		return err
	}
	// 重复审批必须被拒绝且不能产生事件。
	dupApproval := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusConflict,
		nil,
	)
	if dupApproval != nil {
		return dupApproval
	}
	if err := recordProgress(client, baseURL, plan.ID, 35, "矩阵剥离"); err != nil {
		return err
	}
	// 进度回退必须被拒绝且不能产生事件。
	regressed := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/progress",
		contracts.ProgressRequest{
			Stage:      "倒退",
			Percent:    10,
			RecordedBy: "preparator-chen",
		},
		http.StatusConflict,
		nil,
	)
	if regressed != nil {
		return regressed
	}
	if err := recordProgress(client, baseURL, plan.ID, 100, "表面加固"); err != nil {
		return err
	}
	// 完成后的方案不能再写进度。
	closed := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/progress",
		contracts.ProgressRequest{
			Stage:      "完成后追加",
			Percent:    100,
			RecordedBy: "preparator-chen",
		},
		http.StatusConflict,
		nil,
	)
	if closed != nil {
		return closed
	}

	var release releaseView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "比较解剖研究",
			Recipient:  "地层研究组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusCreated,
		&release,
	); err != nil {
		return err
	}
	// 重复放行必须被拒绝且不能产生事件。
	dupRelease := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimen.ID+"/release",
		contracts.ReleaseRequest{
			Purpose:    "重复借阅",
			Recipient:  "其他研究组",
			ApprovedBy: "keeper-zhou",
		},
		http.StatusConflict,
		nil,
	)
	if dupRelease != nil {
		return dupRelease
	}

	wantActions := []string{
		"specimen_registered",
		"plan_created",
		"plan_approved",
		"progress_logged",
		"progress_logged",
		"specimen_released",
	}

	var specimenEvents eventList
	if err := requestJSON(
		client,
		http.MethodGet,
		baseURL+"/specimens/"+specimen.ID+"/events",
		nil,
		http.StatusOK,
		&specimenEvents,
	); err != nil {
		return err
	}
	if err := assertEventOrder(specimenEvents.Items, wantActions, specimen.ID, ""); err != nil {
		return fmt.Errorf("specimen events: %w", err)
	}

	var planEvents eventList
	if err := requestJSON(
		client,
		http.MethodGet,
		baseURL+"/plans/"+plan.ID+"/events",
		nil,
		http.StatusOK,
		&planEvents,
	); err != nil {
		return err
	}
	if err := assertEventOrder(
		planEvents.Items,
		wantActions[1:5],
		specimen.ID,
		plan.ID,
	); err != nil {
		return fmt.Errorf("plan events: %w", err)
	}

	// 关键状态结果必须反映每次提交后的事实。
	first := specimenEvents.Items[0]
	if first.Result.SpecimenStatus != "registered" {
		return fmt.Errorf("register event status = %q", first.Result.SpecimenStatus)
	}
	p1 := specimenEvents.Items[3]
	if p1.Result.PlanStatus != "active" || p1.Result.Percent == nil || *p1.Result.Percent != 35 {
		return fmt.Errorf("first progress event result mismatch: %+v", p1.Result)
	}
	p2 := specimenEvents.Items[4]
	if p2.Result.PlanStatus != "completed" || p2.Result.SpecimenStatus != "stabilized" ||
		p2.Result.Percent == nil || *p2.Result.Percent != 100 ||
		p2.Result.ProgressSeq == nil || *p2.Result.ProgressSeq != 2 {
		return fmt.Errorf("completion progress event result mismatch: %+v", p2.Result)
	}
	final := specimenEvents.Items[5]
	if final.Result.SpecimenStatus != "available" || final.Result.ReleaseID != release.ID {
		return fmt.Errorf("release event result mismatch: %+v", final.Result)
	}

	// 未知对象的事件读取必须返回 404。
	missingSpecimen := requestJSON(
		client, http.MethodGet, baseURL+"/specimens/specimen-missing/events", nil,
		http.StatusNotFound, nil,
	)
	if missingSpecimen != nil {
		return missingSpecimen
	}
	missingPlan := requestJSON(
		client, http.MethodGet, baseURL+"/plans/plan-missing/events", nil,
		http.StatusNotFound, nil,
	)
	if missingPlan != nil {
		return missingPlan
	}
	return nil
}

func assertEventOrder(
	items []eventView,
	wantActions []string,
	specimenID string,
	planID string,
) error {
	if len(items) != len(wantActions) {
		return fmt.Errorf("expected %d events, got %d", len(wantActions), len(items))
	}
	var previous int64
	for i, item := range items {
		if item.Action != wantActions[i] {
			return fmt.Errorf("event %d action = %q, want %q", i, item.Action, wantActions[i])
		}
		if item.SpecimenID != specimenID {
			return fmt.Errorf("event %d not linked to specimen %s", i, specimenID)
		}
		if planID != "" && item.PlanID != planID {
			return fmt.Errorf("event %d not linked to plan %s", i, planID)
		}
		if item.OccurredAt == "" {
			return fmt.Errorf("event %d is missing the commit time", i)
		}
		if i > 0 && item.Sequence <= previous {
			return fmt.Errorf("event sequence not strictly increasing at %d: %d <= %d", i, item.Sequence, previous)
		}
		previous = item.Sequence
	}
	return nil
}

func registerSpecimen(client *http.Client, baseURL string) (specimenView, error) {
	var specimen specimenView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens",
		contracts.RegisterSpecimenRequest{
			CatalogNumber: "PPS-2026-001",
			FieldLabel:    "北坡-07",
			TaxonHint:     "大型脊椎动物",
			MatrixKind:    "粉砂岩",
			JacketNotes:   "夹克边角完好",
		},
		http.StatusCreated,
		&specimen,
	)
	return specimen, err
}

func createPlan(client *http.Client, baseURL, specimenID string) (planView, error) {
	var plan planView
	err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/specimens/"+specimenID+"/plans",
		contracts.CreatePlanRequest{
			Objective:      "暴露主要关节面",
			Method:         "手工显微剥离",
			Adhesive:       "可逆树脂",
			TargetExposure: 80,
		},
		http.StatusCreated,
		&plan,
	)
	return plan, err
}

func completedPlanSetup(client *http.Client, baseURL string) (specimenView, planView, error) {
	specimen, err := registerSpecimen(client, baseURL)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	plan, err := createPlan(client, baseURL, specimen.ID)
	if err != nil {
		return specimenView{}, planView{}, err
	}
	var approved planView
	if err := requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+plan.ID+"/approval",
		contracts.ApprovalRequest{ApprovedBy: "curator-lin"},
		http.StatusOK,
		&approved,
	); err != nil {
		return specimenView{}, planView{}, err
	}
	return specimen, approved, nil
}

func recordProgress(client *http.Client, baseURL, planID string, percent int, stage string) error {
	var ignored struct{}
	return requestJSON(
		client,
		http.MethodPost,
		baseURL+"/plans/"+planID+"/progress",
		contracts.ProgressRequest{
			Stage:      stage,
			Percent:    percent,
			Note:       "本地链路检查",
			RecordedBy: "preparator-chen",
		},
		http.StatusCreated,
		&ignored,
	)
}

func requestJSON(
	client *http.Client,
	method string,
	url string,
	body any,
	wantStatus int,
	target any,
) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, url, payload)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("request %s %s returned %d: %s", method, url, response.StatusCode, string(raw))
	}
	if target == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, url, err)
	}
	return nil
}
