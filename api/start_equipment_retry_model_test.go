package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"cacaoferment/adapter"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

type retryWindowModel struct {
	handler http.Handler
	probe   *adapter.ScriptAdapter
	taskID  string
}

func TestModel_StartEquipmentHonorsProbeRetryWindow(t *testing.T) {
	cases := []struct {
		name                string
		steps               []adapter.Step
		wantBoundaryStatus  evidence.AdapterStatus
		wantBoundaryCode    string
		wantBoundaryStarted bool
		wantFinalSuccess    bool
	}{
		{
			name: "single_timeout_cannot_be_turned_into_early_success",
			steps: []adapter.Step{
				{Fault: adapter.FaultTimeout, StableErrorCode: "probe_timeout", RetryAfterTicks: 4},
			},
			wantBoundaryStatus:  evidence.AdapterSucceeded,
			wantBoundaryStarted: true,
		},
		{
			name: "next_scripted_fault_is_preserved_until_retry_boundary",
			steps: []adapter.Step{
				{Fault: adapter.FaultTimeout, StableErrorCode: "probe_timeout", RetryAfterTicks: 4},
				{Fault: adapter.FaultReject, StableErrorCode: "probe_rejected_after_retry", RetryAfterTicks: 0},
			},
			wantBoundaryStatus:  evidence.AdapterRejected,
			wantBoundaryCode:    "probe_rejected_after_retry",
			wantBoundaryStarted: false,
			wantFinalSuccess:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newRetryWindowModel(t, tc.steps)
			wantRemainingAfterFirst := len(tc.steps) - 1

			first := m.startEquipment(t, "start-first")
			firstAttempt := onlyProbeAttempt(t, first)
			if first.Started || first.Task.State != task.StateEquipmentOccupied {
				t.Fatalf("first timeout started=%v state=%q, want not started in %q", first.Started, first.Task.State, task.StateEquipmentOccupied)
			}
			if firstAttempt.Status != evidence.AdapterTimeout || firstAttempt.StableErrorCode != "probe_timeout" {
				t.Fatalf("first probe attempt = %q/%q, want timeout/probe_timeout", firstAttempt.Status, firstAttempt.StableErrorCode)
			}
			if firstAttempt.ScriptStep != 0 {
				t.Fatalf("first script step = %d, want 0", firstAttempt.ScriptStep)
			}
			if firstAttempt.RetryAfterTick <= firstAttempt.LogicalTick {
				t.Fatalf("retry_after_tick = %d must be after logical_tick = %d", firstAttempt.RetryAfterTick, firstAttempt.LogicalTick)
			}
			if got := m.probe.Remaining(); got != wantRemainingAfterFirst {
				t.Fatalf("remaining script steps after first attempt = %d, want %d", got, wantRemainingAfterFirst)
			}

			replay := m.startEquipment(t, "start-first")
			if !reflect.DeepEqual(replay, first) {
				t.Fatalf("same operation_key replay changed result:\nfirst:  %+v\nreplay: %+v", first, replay)
			}
			if got := m.probe.Remaining(); got != wantRemainingAfterFirst {
				t.Fatalf("same operation_key replay consumed script step, remaining = %d, want %d", got, wantRemainingAfterFirst)
			}
			if attempts := m.auditAttempts(t); len(attempts) != 1 {
				t.Fatalf("same operation_key replay recorded %d attempts, want 1", len(attempts))
			}

			inside := m.startEquipment(t, "start-inside-window")
			insideAttempt := onlyProbeAttempt(t, inside)
			if inside.Started || inside.Task.State != task.StateEquipmentOccupied {
				t.Fatalf("inside retry window started=%v state=%q, want not started in %q", inside.Started, inside.Task.State, task.StateEquipmentOccupied)
			}
			if insideAttempt.Status != evidence.AdapterPending {
				t.Fatalf("inside retry window status = %q, want %q", insideAttempt.Status, evidence.AdapterPending)
			}
			if insideAttempt.ScriptStep != firstAttempt.ScriptStep {
				t.Fatalf("inside retry script step = %d, want preserved step %d", insideAttempt.ScriptStep, firstAttempt.ScriptStep)
			}
			if insideAttempt.RetryAfterTick != firstAttempt.RetryAfterTick {
				t.Fatalf("inside retry_after_tick = %d, want original %d", insideAttempt.RetryAfterTick, firstAttempt.RetryAfterTick)
			}
			if insideAttempt.LogicalTick >= firstAttempt.RetryAfterTick {
				t.Fatalf("inside retry logical_tick = %d, want before retry_after_tick %d", insideAttempt.LogicalTick, firstAttempt.RetryAfterTick)
			}
			if got := m.probe.Remaining(); got != wantRemainingAfterFirst {
				t.Fatalf("inside retry window consumed script step, remaining = %d, want %d", got, wantRemainingAfterFirst)
			}
			if attempts := m.auditAttempts(t); len(attempts) != 2 || attempts[1].Status != evidence.AdapterPending {
				t.Fatalf("audit attempts inside retry window = %+v, want exactly timeout then pending", attempts)
			}

			boundary := m.startEquipment(t, "start-at-retry-boundary")
			boundaryAttempt := onlyProbeAttempt(t, boundary)
			if boundaryAttempt.LogicalTick != firstAttempt.RetryAfterTick {
				t.Fatalf("boundary logical_tick = %d, want retry_after_tick %d", boundaryAttempt.LogicalTick, firstAttempt.RetryAfterTick)
			}
			if boundaryAttempt.ScriptStep != 1 {
				t.Fatalf("boundary script step = %d, want 1", boundaryAttempt.ScriptStep)
			}
			if boundaryAttempt.Status != tc.wantBoundaryStatus {
				t.Fatalf("boundary status = %q, want %q", boundaryAttempt.Status, tc.wantBoundaryStatus)
			}
			if boundaryAttempt.StableErrorCode != tc.wantBoundaryCode {
				t.Fatalf("boundary stable error = %q, want %q", boundaryAttempt.StableErrorCode, tc.wantBoundaryCode)
			}
			if boundary.Started != tc.wantBoundaryStarted {
				t.Fatalf("boundary started = %v, want %v", boundary.Started, tc.wantBoundaryStarted)
			}
			if tc.wantBoundaryStarted && boundary.Task.State != task.StateTurnCollection {
				t.Fatalf("boundary state = %q, want %q", boundary.Task.State, task.StateTurnCollection)
			}
			if !tc.wantBoundaryStarted && boundary.Task.State != task.StateEquipmentOccupied {
				t.Fatalf("boundary state = %q, want %q", boundary.Task.State, task.StateEquipmentOccupied)
			}

			if tc.wantFinalSuccess {
				final := m.startEquipment(t, "start-final")
				finalAttempt := onlyProbeAttempt(t, final)
				if finalAttempt.Status != evidence.AdapterSucceeded || finalAttempt.ScriptStep != 2 {
					t.Fatalf("final attempt = %q step %d, want succeeded step 2", finalAttempt.Status, finalAttempt.ScriptStep)
				}
				if !final.Started || final.Task.State != task.StateTurnCollection {
					t.Fatalf("final started=%v state=%q, want started in %q", final.Started, final.Task.State, task.StateTurnCollection)
				}
			}
		})
	}
}

func newRetryWindowModel(t *testing.T, steps []adapter.Step) retryWindowModel {
	t.Helper()

	c, e := seed()
	e.AddProbe("probe-1")

	reg := adapter.NewRegistry()
	probe := adapter.NewScriptAdapter(evidence.AdapterProbe, steps)
	reg.Register(probe)
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterToxinReader, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterMoistureMeter, nil))

	srv := New(service.New(store.NewMemory(), c, e, reg), nil)
	m := retryWindowModel{handler: srv.Handler(), probe: probe}

	var lockOut struct {
		Task task.FermentTask `json:"task"`
	}
	m.post(t, "/api/tasks/lock", `{"rule_version":"v1","plot_id":"plot-1","variety_batch_id":"variety-1","bin_ids":["bin-1"],"probe_ids":["probe-1"],"boxed_weight_grams":120000,"drying_window_id":"window-1"}`, http.StatusCreated, &lockOut)
	m.taskID = lockOut.Task.TaskID
	if m.taskID == "" {
		t.Fatal("lock response did not include task id")
	}

	m.post(t, "/api/tasks/"+m.taskID+"/boxing-confirmations", `{"operation_key":"box-a","generation":1,"boxer_id":"reviewer-a","boxed_weight_grams":120000}`, http.StatusOK, nil)
	m.post(t, "/api/tasks/"+m.taskID+"/boxing-confirmations", `{"operation_key":"box-b","generation":1,"boxer_id":"reviewer-b","boxed_weight_grams":120000}`, http.StatusOK, nil)

	return m
}

func (m retryWindowModel) startEquipment(t *testing.T, operationKey string) service.StartEquipmentResult {
	t.Helper()

	var out service.StartEquipmentResult
	body := fmt.Sprintf(`{"operation_key":%q,"generation":1}`, operationKey)
	m.post(t, "/api/tasks/"+m.taskID+"/start-equipment", body, http.StatusOK, &out)
	return out
}

func (m retryWindowModel) auditAttempts(t *testing.T) []evidence.AdapterAttempt {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+m.taskID+"/audit", nil)
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var out service.AuditView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	return out.AdapterAttempts
}

func (m retryWindowModel) post(t *testing.T, path string, body string, wantStatus int, out any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("POST %s status = %d, want %d, body = %s", path, rec.Code, wantStatus, rec.Body.String())
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode POST %s response: %v", path, err)
		}
	}
}

func onlyProbeAttempt(t *testing.T, res service.StartEquipmentResult) evidence.AdapterAttempt {
	t.Helper()
	if len(res.Attempts) != 1 {
		t.Fatalf("attempt count = %d, want 1: %+v", len(res.Attempts), res.Attempts)
	}
	if res.Attempts[0].AdapterKind != evidence.AdapterProbe || res.Attempts[0].TargetKey != "probe-1" {
		t.Fatalf("attempt = %+v, want probe-1 attempt", res.Attempts[0])
	}
	return res.Attempts[0]
}
