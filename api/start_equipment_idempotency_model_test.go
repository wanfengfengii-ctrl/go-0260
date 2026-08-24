package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"cacaoferment/adapter"
	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_StartEquipmentIdempotentReplayFreezesAdapterAttempts(t *testing.T) {
	c := catalog.NewMapCatalog()
	e := catalog.NewEquipmentDirectory()
	if err := c.AddSnapshot(catalog.RuleSnapshot{
		RuleVersion:    "v1",
		PlotID:         "plot-1",
		VarietyBatchID: "variety-1",
		TurnNodes:      []int{1, 2, 3},
		TemperatureThresholds: catalog.TemperatureThresholds{
			AmbientCentiC:       3000,
			MinCentiC:           3800,
			MaxCentiC:           5600,
			MinSlopeMilliPerMin: 0,
			MaxSlopeMilliPerMin: 400,
			MinDurationMinutes:  30,
			MaxDurationMinutes:  240,
			MaxTurnCount:        4,
		},
		QualifiedReviewers: []catalog.Reviewer{
			{ID: "reviewer-a", Qualified: true},
			{ID: "reviewer-b", Qualified: true},
		},
		FixedScale: 2,
	}); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	c.AddPlotVariety("plot-1", "variety-1")
	for _, id := range []string{"bin-1", "bin-2"} {
		e.AddBin(id)
	}
	e.AddProbe("probe-1")
	e.AddWell("well-1")
	e.AddWindow("window-1")

	reg := adapter.NewRegistry()
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterProbe, []adapter.Step{
		{Fault: adapter.FaultReject, StableErrorCode: "probe_rejected", RetryAfterTicks: 5},
		{Fault: adapter.FaultTimeout, StableErrorCode: "probe_timeout", RetryAfterTicks: 3},
	}))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterToxinReader, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterMoistureMeter, nil))
	srv := New(service.New(store.NewMemory(), c, e, reg), nil)

	type taskJSON struct {
		TaskID string     `json:"TaskID"`
		State  task.State `json:"State"`
	}
	type attemptJSON struct {
		AttemptID       string                 `json:"AttemptID"`
		AdapterKind     evidence.AdapterKind   `json:"AdapterKind"`
		TargetKey       string                 `json:"TargetKey"`
		ScriptStep      int                    `json:"ScriptStep"`
		LogicalTick     int64                  `json:"LogicalTick"`
		Status          evidence.AdapterStatus `json:"Status"`
		StableErrorCode string                 `json:"StableErrorCode"`
		RetryAfterTick  int64                  `json:"RetryAfterTick"`
	}
	type startResponse struct {
		Task     taskJSON      `json:"task"`
		Attempts []attemptJSON `json:"attempts"`
		Started  bool          `json:"started"`
	}
	type errorResponse struct {
		Error ErrorResponse `json:"error"`
	}
	type auditResponse struct {
		Task            taskJSON      `json:"task"`
		AdapterAttempts []attemptJSON `json:"adapter_attempts"`
	}

	post := func(t *testing.T, path, body string, wantStatus int, out any) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != wantStatus {
			t.Fatalf("POST %s status = %d, want %d, body = %s", path, rec.Code, wantStatus, rec.Body.String())
		}
		if out != nil {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatalf("decode POST %s response: %v", path, err)
			}
		}
	}
	audit := func(t *testing.T, taskID string) auditResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+taskID+"/audit", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("audit status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var out auditResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode audit response: %v", err)
		}
		return out
	}
	expectAttempt := func(t *testing.T, attempts []attemptJSON, kind evidence.AdapterKind, status evidence.AdapterStatus, code string) {
		t.Helper()
		for _, a := range attempts {
			if a.AdapterKind == kind {
				if a.Status != status || a.StableErrorCode != code {
					t.Fatalf("%s attempt = status %q/code %q, want %q/%q", kind, a.Status, a.StableErrorCode, status, code)
				}
				return
			}
		}
		t.Fatalf("missing %s attempt in %+v", kind, attempts)
	}

	var lockOut struct {
		Task taskJSON `json:"task"`
	}
	post(t, "/api/tasks/lock", `{"rule_version":"v1","plot_id":"plot-1","variety_batch_id":"variety-1","bin_ids":["bin-1","bin-2"],"probe_ids":["probe-1"],"plate_well_ids":["well-1"],"boxed_weight_grams":120000,"drying_window_id":"window-1"}`, http.StatusCreated, &lockOut)
	if lockOut.Task.TaskID == "" {
		t.Fatal("lock returned empty task id")
	}
	for _, body := range []string{
		`{"operation_key":"box-a","generation":1,"boxer_id":"reviewer-a","boxed_weight_grams":120000}`,
		`{"operation_key":"box-b","generation":1,"boxer_id":"reviewer-b","boxed_weight_grams":120000}`,
	} {
		post(t, "/api/tasks/"+lockOut.Task.TaskID+"/boxing-confirmations", body, http.StatusOK, nil)
	}

	startPath := "/api/tasks/" + lockOut.Task.TaskID + "/start-equipment"
	var firstAttempts []attemptJSON
	cases := []struct {
		name              string
		body              string
		wantStatus        int
		wantStarted       bool
		wantTaskState     task.State
		wantAttemptCount  int
		wantAuditAttempts int
		wantErrorCode     string
		wantProbeStatus   evidence.AdapterStatus
		wantProbeCode     string
		snapshotFirst     bool
		wantSameAsFirst   bool
	}{
		{
			name:              "first_failed_start_records_only_this_operations_attempts",
			body:              `{"operation_key":"start-1","generation":1}`,
			wantStatus:        http.StatusOK,
			wantTaskState:     task.StateEquipmentOccupied,
			wantAttemptCount:  2,
			wantAuditAttempts: 2,
			wantProbeStatus:   evidence.AdapterRejected,
			wantProbeCode:     "probe_rejected",
			snapshotFirst:     true,
		},
		{
			name:              "second_operation_failure_appends_retry_audit_without_advancing",
			body:              `{"operation_key":"start-2","generation":1}`,
			wantStatus:        http.StatusOK,
			wantTaskState:     task.StateEquipmentOccupied,
			wantAttemptCount:  2,
			wantAuditAttempts: 4,
			wantProbeStatus:   evidence.AdapterTimeout,
			wantProbeCode:     "probe_timeout",
		},
		{
			name:              "first_key_replay_ignores_later_operation_attempts",
			body:              `{"operation_key":"start-1","generation":1}`,
			wantStatus:        http.StatusOK,
			wantTaskState:     task.StateEquipmentOccupied,
			wantAttemptCount:  2,
			wantAuditAttempts: 4,
			wantProbeStatus:   evidence.AdapterRejected,
			wantProbeCode:     "probe_rejected",
			wantSameAsFirst:   true,
		},
		{
			name:              "first_key_with_different_content_conflicts_without_new_attempts",
			body:              `{"operation_key":"start-1","generation":0}`,
			wantStatus:        http.StatusConflict,
			wantAuditAttempts: 4,
			wantErrorCode:     service.CodeConflict,
		},
		{
			name:              "subsequent_success_advances_to_turn_collection",
			body:              `{"operation_key":"start-3","generation":1}`,
			wantStatus:        http.StatusOK,
			wantStarted:       true,
			wantTaskState:     task.StateTurnCollection,
			wantAttemptCount:  2,
			wantAuditAttempts: 6,
			wantProbeStatus:   evidence.AdapterSucceeded,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantErrorCode != "" {
				var out errorResponse
				post(t, startPath, tc.body, tc.wantStatus, &out)
				if out.Error.Code != tc.wantErrorCode {
					t.Fatalf("error code = %q, want %q", out.Error.Code, tc.wantErrorCode)
				}
			} else {
				var out startResponse
				post(t, startPath, tc.body, tc.wantStatus, &out)
				if out.Started != tc.wantStarted {
					t.Fatalf("started = %v, want %v", out.Started, tc.wantStarted)
				}
				if out.Task.State != tc.wantTaskState {
					t.Fatalf("state = %q, want %q", out.Task.State, tc.wantTaskState)
				}
				if len(out.Attempts) != tc.wantAttemptCount {
					t.Fatalf("attempt count = %d, want %d: %+v", len(out.Attempts), tc.wantAttemptCount, out.Attempts)
				}
				expectAttempt(t, out.Attempts, evidence.AdapterProbe, tc.wantProbeStatus, tc.wantProbeCode)
				expectAttempt(t, out.Attempts, evidence.AdapterToxinReader, evidence.AdapterSucceeded, "")
				if tc.snapshotFirst {
					firstAttempts = append([]attemptJSON(nil), out.Attempts...)
				}
				if tc.wantSameAsFirst && !reflect.DeepEqual(out.Attempts, firstAttempts) {
					t.Fatalf("idempotent replay attempts changed:\nfirst:  %+v\nreplay: %+v", firstAttempts, out.Attempts)
				}
			}

			gotAudit := audit(t, lockOut.Task.TaskID)
			if len(gotAudit.AdapterAttempts) != tc.wantAuditAttempts {
				t.Fatalf("audit attempt count = %d, want %d: %+v", len(gotAudit.AdapterAttempts), tc.wantAuditAttempts, gotAudit.AdapterAttempts)
			}
			if tc.wantTaskState != "" && gotAudit.Task.State != tc.wantTaskState {
				t.Fatalf("audit state = %q, want %q", gotAudit.Task.State, tc.wantTaskState)
			}
		})
	}
}
