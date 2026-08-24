package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cacaoferment/adapter"
	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
)

func newTestServer() *Server {
	c, e := seed()
	reg := adapter.NewRegistry()
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterProbe, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterToxinReader, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterMoistureMeter, nil))
	svc := service.New(store.NewMemory(), c, e, reg)
	return New(svc, nil)
}

func seed() (*catalog.MapCatalog, *catalog.EquipmentDirectory) {
	c := catalog.NewMapCatalog()
	e := catalog.NewEquipmentDirectory()
	_ = c.AddSnapshot(catalog.RuleSnapshot{
		RuleVersion: "v1", PlotID: "plot-1", VarietyBatchID: "variety-1",
		TurnNodes: []int{1, 2, 3}, FixedScale: 2,
		TemperatureThresholds: catalog.TemperatureThresholds{AmbientCentiC: 3000, MinCentiC: 3800, MaxCentiC: 5600, MinSlopeMilliPerMin: 0, MaxSlopeMilliPerMin: 400, MinDurationMinutes: 30, MaxDurationMinutes: 240, MaxTurnCount: 4},
		QualifiedReviewers:    []catalog.Reviewer{{ID: "reviewer-a", Qualified: true}, {ID: "reviewer-b", Qualified: true}},
	})
	c.AddPlotVariety("plot-1", "variety-1")
	e.AddBin("bin-1")
	e.AddBin("bin-2")
	e.AddWindow("window-1")
	return c, e
}

func TestHealth(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestLockEndpoint(t *testing.T) {
	srv := newTestServer()
	body := `{"rule_version":"v1","plot_id":"plot-1","variety_batch_id":"variety-1","bin_ids":["bin-1","bin-2"],"boxed_weight_grams":120000,"drying_window_id":"window-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Task struct {
			TaskID string `json:"TaskID"`
		} `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Task.TaskID == "" {
		t.Fatal("expected task id")
	}
}

func TestLockValidationErrorEndpoint(t *testing.T) {
	srv := newTestServer()
	body := `{"rule_version":"v1","plot_id":"plot-1","variety_batch_id":"variety-9","bin_ids":["bin-1"],"boxed_weight_grams":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	var out struct {
		Error ErrorResponse `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Error.Code != service.CodePlotVarietyMismatch {
		t.Fatalf("code = %q", out.Error.Code)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/nope", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestAuditEndpoint(t *testing.T) {
	srv := newTestServer()
	body := `{"rule_version":"v1","plot_id":"plot-1","variety_batch_id":"variety-1","bin_ids":["bin-1"],"boxed_weight_grams":120000,"drying_window_id":"window-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/lock", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var out struct {
		Task struct {
			TaskID string `json:"TaskID"`
		} `json:"task"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)

	auditReq := httptest.NewRequest(http.MethodGet, "/api/tasks/"+out.Task.TaskID+"/audit", nil)
	auditRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("audit status = %d, body = %s", auditRec.Code, auditRec.Body.String())
	}
}
