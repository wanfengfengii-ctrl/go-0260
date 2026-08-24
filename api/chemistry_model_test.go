package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"cacaoferment/adapter"
	"cacaoferment/arbiter"
	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_chemistryReadingsPersistAuditAndRejectInvalidBoundaries(t *testing.T) {
	type chemistryPayload struct {
		OperationKey         string `json:"operation_key"`
		Generation           int64  `json:"generation"`
		MoisturePercent      int64  `json:"moisture_percent"`
		PH                   int64  `json:"ph"`
		FreeFattyAcidPercent int64  `json:"free_fatty_acid_percent"`
	}
	type auditTask struct {
		TaskID     string
		Generation int64
		State      task.State
	}
	type auditEvidence struct {
		TaskID        string
		Generation    int64
		EvidenceKind  evidence.EvidenceKind
		SubjectKey    string
		PayloadHash   string
		IntegerValues []int64
		Accepted      bool
		RejectCode    string
	}
	type auditOperation struct {
		OperationKey  string
		OperationKind string
		RequestHash   string
		ResponseHash  string
		StatusCode    int
	}
	type auditResponse struct {
		Task       auditTask        `json:"task"`
		Operations []auditOperation `json:"operations"`
		Evidence   []auditEvidence  `json:"evidence"`
	}

	newServer := func() *Server {
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
			CutTestThresholds: catalog.CutTestThresholds{
				MaxUnderfermentedPercent: 500,
				MaxPurplePercent:         1000,
				MaxMoldyPercent:          200,
				MaxInsectPercent:         200,
			},
			ToxinThresholds: catalog.ToxinThresholds{MaxToxinPPB: 10},
			ChemistryThresholds: catalog.ChemistryThresholds{
				MinMoisturePercent:      5500,
				MaxMoisturePercent:      7500,
				MinPH:                   500,
				MaxPH:                   620,
				MaxFreeFattyAcidPercent: 150,
			},
			QualifiedReviewers: []catalog.Reviewer{
				{ID: "reviewer-a", Qualified: true},
				{ID: "reviewer-b", Qualified: true},
			},
			FixedScale: 2,
		}); err != nil {
			t.Fatalf("seed snapshot: %v", err)
		}
		c.AddPlotVariety("plot-1", "variety-1")
		for _, id := range []string{"bin-1", "bin-2"} {
			e.AddBin(id)
		}
		e.AddProbe("probe-1")
		e.AddWell("well-1")
		e.AddWindow("window-1")
		reg := adapter.NewRegistry()
		reg.Register(adapter.NewScriptAdapter(evidence.AdapterProbe, nil))
		reg.Register(adapter.NewScriptAdapter(evidence.AdapterToxinReader, nil))
		reg.Register(adapter.NewScriptAdapter(evidence.AdapterMoistureMeter, nil))
		return New(service.New(store.NewMemory(), c, e, reg), nil)
	}

	doJSON := func(t *testing.T, h http.Handler, method, path string, payload any) (int, []byte) {
		t.Helper()
		var body bytes.Reader
		if payload != nil {
			b, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal %s %s: %v", method, path, err)
			}
			body.Reset(b)
		}
		req := httptest.NewRequest(method, path, &body)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	mustPost := func(t *testing.T, h http.Handler, path string, payload any) []byte {
		t.Helper()
		status, body := doJSON(t, h, http.MethodPost, path, payload)
		if status < 200 || status > 299 {
			t.Fatalf("POST %s status = %d, body = %s", path, status, body)
		}
		return body
	}
	driveToChemistryRetest := func(t *testing.T, h http.Handler) string {
		t.Helper()
		lockBody := service.LockRequest{
			RuleVersion:      "v1",
			PlotID:           "plot-1",
			VarietyBatchID:   "variety-1",
			BinIDs:           []string{"bin-1", "bin-2"},
			ProbeIDs:         []string{"probe-1"},
			PlateWellIDs:     []string{"well-1"},
			DryingWindowID:   "window-1",
			BoxedWeightGrams: 120000,
			BlindSamples: []service.BlindSampleSpec{
				{BlindCode: "BC-1", SampleSize: 100, BinID: "bin-1"},
				{BlindCode: "BC-2", SampleSize: 100, BinID: "bin-2"},
			},
		}
		var locked struct {
			Task struct {
				TaskID string
			} `json:"task"`
		}
		if err := json.Unmarshal(mustPost(t, h, "/api/tasks/lock", lockBody), &locked); err != nil {
			t.Fatalf("decode lock response: %v", err)
		}
		taskID := locked.Task.TaskID
		mustPost(t, h, "/api/tasks/"+taskID+"/boxing-confirmations", service.BoxingRequest{
			OperationKey:     "box-a",
			Generation:       1,
			BoxerID:          "reviewer-a",
			BoxedWeightGrams: 120000,
		})
		mustPost(t, h, "/api/tasks/"+taskID+"/boxing-confirmations", service.BoxingRequest{
			OperationKey:     "box-b",
			Generation:       1,
			BoxerID:          "reviewer-b",
			BoxedWeightGrams: 120000,
		})
		mustPost(t, h, "/api/tasks/"+taskID+"/start-equipment", service.StartEquipmentRequest{OperationKey: "start", Generation: 1})
		mustPost(t, h, "/api/tasks/"+taskID+"/turn-readings", service.TurnReadingsRequest{
			OperationKey: "turns",
			Generation:   1,
			Readings: []service.TurnReading{
				{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1},
				{BinID: "bin-1", TurnNode: 2, TemperatureCentiC: 4300, DurationMinutes: 120, TurnCount: 2},
				{BinID: "bin-1", TurnNode: 3, TemperatureCentiC: 4600, DurationMinutes: 180, TurnCount: 3},
			},
		})
		cut := arbiter.CutScore{Underfermented: 4, Purple: 3, Moldy: 0, Insect: 1, Good: 92}
		mustPost(t, h, "/api/tasks/"+taskID+"/blind-samples/BC-1/scores", service.BlindSampleRequest{OperationKey: "cut-1", Generation: 1, CutScore: &cut})
		mustPost(t, h, "/api/tasks/"+taskID+"/blind-samples/BC-2/scores", service.BlindSampleRequest{OperationKey: "cut-2", Generation: 1, CutScore: &cut})
		toxin := int64(5)
		mustPost(t, h, "/api/tasks/"+taskID+"/blind-samples/BC-1/scores", service.BlindSampleRequest{OperationKey: "tox-1", Generation: 1, ToxinPPB: &toxin})
		mustPost(t, h, "/api/tasks/"+taskID+"/blind-samples/BC-2/scores", service.BlindSampleRequest{OperationKey: "tox-2", Generation: 1, ToxinPPB: &toxin})
		return taskID
	}
	lockOnly := func(t *testing.T, h http.Handler) string {
		t.Helper()
		var locked struct {
			Task struct {
				TaskID string
			} `json:"task"`
		}
		body := service.LockRequest{
			RuleVersion:      "v1",
			PlotID:           "plot-1",
			VarietyBatchID:   "variety-1",
			BinIDs:           []string{"bin-1"},
			DryingWindowID:   "window-1",
			BoxedWeightGrams: 120000,
		}
		if err := json.Unmarshal(mustPost(t, h, "/api/tasks/lock", body), &locked); err != nil {
			t.Fatalf("decode lock-only response: %v", err)
		}
		return locked.Task.TaskID
	}
	loadAudit := func(t *testing.T, h http.Handler, taskID string) auditResponse {
		t.Helper()
		status, body := doJSON(t, h, http.MethodGet, "/api/tasks/"+taskID+"/audit", nil)
		if status != http.StatusOK {
			t.Fatalf("audit status = %d, body = %s", status, body)
		}
		var audit auditResponse
		if err := json.Unmarshal(body, &audit); err != nil {
			t.Fatalf("decode audit: %v", err)
		}
		return audit
	}
	hashInts := func(values []int64) string {
		b, err := json.Marshal(values)
		if err != nil {
			t.Fatalf("marshal values: %v", err)
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}

	cases := []struct {
		name                  string
		prepare               func(*testing.T, http.Handler) string
		request               chemistryPayload
		wantStatus            int
		wantErrorCode         string
		wantTaskState         task.State
		wantChemistryEvidence bool
		wantAccepted          bool
		wantRejectCode        string
		wantValues            []int64
		wantOperation         bool
	}{
		{
			name:                  "accepted readings advance and appear in audit",
			prepare:               driveToChemistryRetest,
			request:               chemistryPayload{OperationKey: "chem-ok", Generation: 1, MoisturePercent: 6500, PH: 560, FreeFattyAcidPercent: 100},
			wantStatus:            http.StatusOK,
			wantTaskState:         task.StatePendingReview,
			wantChemistryEvidence: true,
			wantAccepted:          true,
			wantValues:            []int64{6500, 560, 100},
			wantOperation:         true,
		},
		{
			name:                  "out of range readings advance with rejected audit evidence",
			prepare:               driveToChemistryRetest,
			request:               chemistryPayload{OperationKey: "chem-reject", Generation: 1, MoisturePercent: 8000, PH: 560, FreeFattyAcidPercent: 100},
			wantStatus:            http.StatusOK,
			wantTaskState:         task.StatePendingReview,
			wantChemistryEvidence: true,
			wantAccepted:          false,
			wantRejectCode:        string(arbiter.AnomalyChemistryOutOfRange),
			wantValues:            []int64{8000, 560, 100},
			wantOperation:         true,
		},
		{
			name:          "negative reading keeps audit unchanged",
			prepare:       driveToChemistryRetest,
			request:       chemistryPayload{OperationKey: "chem-negative", Generation: 1, MoisturePercent: -1, PH: 560, FreeFattyAcidPercent: 100},
			wantStatus:    http.StatusBadRequest,
			wantErrorCode: service.CodeInvalidReading,
			wantTaskState: task.StateChemistryRetest,
		},
		{
			name:          "too many fixed decimal digits keeps audit unchanged",
			prepare:       driveToChemistryRetest,
			request:       chemistryPayload{OperationKey: "chem-long", Generation: 1, MoisturePercent: 1000000000, PH: 560, FreeFattyAcidPercent: 100},
			wantStatus:    http.StatusBadRequest,
			wantErrorCode: service.CodeInvalidReading,
			wantTaskState: task.StateChemistryRetest,
		},
		{
			name:          "state mismatch keeps audit unchanged",
			prepare:       lockOnly,
			request:       chemistryPayload{OperationKey: "chem-state", Generation: 1, MoisturePercent: 6500, PH: 560, FreeFattyAcidPercent: 100},
			wantStatus:    http.StatusConflict,
			wantErrorCode: service.CodeInvalidState,
			wantTaskState: task.StatePendingBoxing,
		},
		{
			name:          "generation conflict keeps audit unchanged",
			prepare:       driveToChemistryRetest,
			request:       chemistryPayload{OperationKey: "chem-generation", Generation: 2, MoisturePercent: 6500, PH: 560, FreeFattyAcidPercent: 100},
			wantStatus:    http.StatusConflict,
			wantErrorCode: service.CodeGenerationMismatch,
			wantTaskState: task.StateChemistryRetest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newServer()
			h := srv.Handler()
			taskID := tc.prepare(t, h)

			status, body := doJSON(t, h, http.MethodPost, "/api/tasks/"+taskID+"/chemistry-readings", tc.request)
			if status != tc.wantStatus {
				t.Fatalf("chemistry status = %d, want %d, body = %s", status, tc.wantStatus, body)
			}
			if tc.wantErrorCode != "" {
				var out struct {
					Error ErrorResponse `json:"error"`
				}
				if err := json.Unmarshal(body, &out); err != nil {
					t.Fatalf("decode error response: %v", err)
				}
				if out.Error.Code != tc.wantErrorCode {
					t.Fatalf("error code = %q, want %q", out.Error.Code, tc.wantErrorCode)
				}
			}

			audit := loadAudit(t, h, taskID)
			if audit.Task.State != tc.wantTaskState {
				t.Fatalf("audit task state = %q, want %q", audit.Task.State, tc.wantTaskState)
			}
			var chemistryEvidence []auditEvidence
			for _, ev := range audit.Evidence {
				if ev.EvidenceKind == evidence.EvidenceChemistry {
					chemistryEvidence = append(chemistryEvidence, ev)
				}
			}
			if tc.wantChemistryEvidence {
				if len(chemistryEvidence) != 1 {
					t.Fatalf("chemistry evidence count = %d, want 1; evidence = %#v", len(chemistryEvidence), audit.Evidence)
				}
				ev := chemistryEvidence[0]
				if ev.TaskID != taskID || ev.Generation != 1 || ev.SubjectKey != "chemistry" {
					t.Fatalf("chemistry evidence identity = %#v", ev)
				}
				if !reflect.DeepEqual(ev.IntegerValues, tc.wantValues) {
					t.Fatalf("chemistry values = %v, want %v", ev.IntegerValues, tc.wantValues)
				}
				if ev.Accepted != tc.wantAccepted || ev.RejectCode != tc.wantRejectCode {
					t.Fatalf("accepted/reject = %v/%q, want %v/%q", ev.Accepted, ev.RejectCode, tc.wantAccepted, tc.wantRejectCode)
				}
				if ev.PayloadHash != hashInts(tc.wantValues) {
					t.Fatalf("payload hash = %q, want hash for %v", ev.PayloadHash, tc.wantValues)
				}
			} else if len(chemistryEvidence) != 0 {
				t.Fatalf("unexpected chemistry evidence after failed request: %#v", chemistryEvidence)
			}

			var chemOps []auditOperation
			for _, op := range audit.Operations {
				if op.OperationKey == tc.request.OperationKey {
					chemOps = append(chemOps, op)
				}
			}
			if tc.wantOperation {
				if len(chemOps) != 1 {
					t.Fatalf("chemistry operation count = %d, want 1; operations = %#v", len(chemOps), audit.Operations)
				}
				op := chemOps[0]
				if op.OperationKind != "chemistry_readings" || op.StatusCode != http.StatusOK || op.RequestHash == "" || op.ResponseHash == "" {
					t.Fatalf("chemistry operation = %#v", op)
				}
			} else if len(chemOps) != 0 {
				t.Fatalf("unexpected chemistry operation after failed request: %#v", chemOps)
			}
		})
	}
}
