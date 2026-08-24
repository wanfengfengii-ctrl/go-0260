package service_test

import (
	"context"
	"testing"

	"cacaoferment/adapter"
	"cacaoferment/arbiter"
	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
)

// testSeed returns a seeded catalog and equipment directory matching the
// production directory initialization.
func testSeed() (*catalog.MapCatalog, *catalog.EquipmentDirectory) {
	c := catalog.NewMapCatalog()
	e := catalog.NewEquipmentDirectory()
	snap := catalog.RuleSnapshot{
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
			{ID: "reviewer-c", Qualified: true},
			{ID: "reviewer-d", Qualified: true},
		},
		FixedScale: 2,
	}
	if err := c.AddSnapshot(snap); err != nil {
		panic(err)
	}
	c.AddPlotVariety("plot-1", "variety-1")
	for _, id := range []string{"bin-1", "bin-2"} {
		e.AddBin(id)
	}
	for _, id := range []string{"probe-1"} {
		e.AddProbe(id)
	}
	for _, id := range []string{"well-1"} {
		e.AddWell(id)
	}
	for _, id := range []string{"window-1"} {
		e.AddWindow(id)
	}
	return c, e
}

func newTestService(t *testing.T, st store.Store) *service.Service {
	t.Helper()
	c, e := testSeed()
	reg := adapter.NewRegistry()
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterProbe, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterToxinReader, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterMoistureMeter, nil))
	return service.New(st, c, e, reg)
}

func defaultLock() service.LockRequest {
	return service.LockRequest{
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
}

func mustLock(t *testing.T, svc *service.Service) string {
	t.Helper()
	res, err := svc.Lock(context.Background(), defaultLock())
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	return res.Task.TaskID
}

func box(t *testing.T, svc *service.Service, id, boxer string) {
	t.Helper()
	_, err := svc.ConfirmBoxing(context.Background(), service.BoxingRequest{
		TaskID: id, OperationKey: "box-" + boxer, Generation: 1, BoxerID: boxer, BoxedWeightGrams: 120000,
	})
	if err != nil {
		t.Fatalf("boxing %s: %v", boxer, err)
	}
}

func startEquipment(t *testing.T, svc *service.Service, id string) {
	t.Helper()
	_, err := svc.StartEquipment(context.Background(), service.StartEquipmentRequest{TaskID: id, OperationKey: "start", Generation: 1})
	if err != nil {
		t.Fatalf("start equipment: %v", err)
	}
}

func collect(t *testing.T, svc *service.Service, id string) {
	t.Helper()
	_, err := svc.SubmitTurnReadings(context.Background(), service.TurnReadingsRequest{
		TaskID: id, OperationKey: "turns", Generation: 1,
		Readings: []service.TurnReading{
			{BinID: "bin-1", TurnNode: 1, TemperatureCentiC: 4000, DurationMinutes: 60, TurnCount: 1},
			{BinID: "bin-1", TurnNode: 2, TemperatureCentiC: 4300, DurationMinutes: 120, TurnCount: 2},
			{BinID: "bin-1", TurnNode: 3, TemperatureCentiC: 4600, DurationMinutes: 180, TurnCount: 3},
		},
	})
	if err != nil {
		t.Fatalf("turn readings: %v", err)
	}
}

func scoreBlind(t *testing.T, svc *service.Service, id, code string) {
	t.Helper()
	cut := arbiterCutScore()
	_, err := svc.SubmitBlindSample(context.Background(), service.BlindSampleRequest{
		TaskID: id, BlindCode: code, OperationKey: "cut-" + code, Generation: 1, CutScore: &cut,
	})
	if err != nil {
		t.Fatalf("blind score %s: %v", code, err)
	}
}

func toxin(t *testing.T, svc *service.Service, id, code string, ppb int64) {
	t.Helper()
	_, err := svc.SubmitBlindSample(context.Background(), service.BlindSampleRequest{
		TaskID: id, BlindCode: code, OperationKey: "tox-" + code, Generation: 1, ToxinPPB: &ppb,
	})
	if err != nil {
		t.Fatalf("toxin %s: %v", code, err)
	}
}

func chemistry(t *testing.T, svc *service.Service, id string) {
	t.Helper()
	_, err := svc.SubmitChemistry(context.Background(), service.ChemistryRequest{
		TaskID: id, OperationKey: "chem", Generation: 1, MoisturePercent: 6500, PH: 560, FreeFattyAcidPercent: 100,
	})
	if err != nil {
		t.Fatalf("chemistry: %v", err)
	}
}

func review(t *testing.T, svc *service.Service, id, reviewer string, decision evidence.Decision) {
	t.Helper()
	_, err := svc.SubmitReview(context.Background(), service.ReviewRequest{
		TaskID: id, OperationKey: "rev-" + reviewer, Generation: 1, ReviewerID: reviewer, Decision: decision,
	})
	if err != nil {
		t.Fatalf("review %s: %v", reviewer, err)
	}
}

// driveToPendingReview locks and advances a task through boxing, equipment,
// collection, blind scoring, toxin, and chemistry to the pending-review state.
func driveToPendingReview(t *testing.T, svc *service.Service) string {
	t.Helper()
	id := mustLock(t, svc)
	box(t, svc, id, "reviewer-a")
	box(t, svc, id, "reviewer-b")
	startEquipment(t, svc, id)
	collect(t, svc, id)
	scoreBlind(t, svc, id, "BC-1")
	scoreBlind(t, svc, id, "BC-2")
	toxin(t, svc, id, "BC-1", 5)
	toxin(t, svc, id, "BC-2", 5)
	chemistry(t, svc, id)
	return id
}

func arbiterCutScore() arbiter.CutScore {
	return arbiter.CutScore{Underfermented: 4, Purple: 3, Moldy: 0, Insect: 1, Good: 92}
}
