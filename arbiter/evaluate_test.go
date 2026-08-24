package arbiter

import (
	"errors"
	"testing"

	"cacaoferment/catalog"
	"cacaoferment/evidence"
)

func testThresholds() catalog.CutTestThresholds {
	return catalog.CutTestThresholds{
		MaxUnderfermentedPercent: 500,
		MaxPurplePercent:         1000,
		MaxMoldyPercent:          200,
		MaxInsectPercent:         200,
	}
}

func TestEvaluateCutTestConservation(t *testing.T) {
	// Sum does not equal the sample size: must be a grain mismatch, not an anomaly.
	_, err := EvaluateCutTest(100, CutScore{Underfermented: 10, Purple: 5, Moldy: 2, Insect: 3, Good: 70}, testThresholds(), 2)
	if !errors.Is(err, evidence.ErrGrainMismatch) {
		t.Fatalf("expected grain mismatch, got %v", err)
	}
}

func TestEvaluateCutTestMoldPositive(t *testing.T) {
	res, err := EvaluateCutTest(100, CutScore{Underfermented: 4, Purple: 3, Moldy: 5, Insect: 1, Good: 87}, testThresholds(), 2)
	if err != nil {
		t.Fatalf("EvaluateCutTest: %v", err)
	}
	if res.Accepted {
		t.Fatal("expected not accepted with mold above threshold")
	}
	if len(res.Anomalies) != 1 || res.Anomalies[0] != AnomalyMoldPositive {
		t.Fatalf("expected mold positive anomaly, got %v", res.Anomalies)
	}
}

func TestEvaluateCutTestDivergence(t *testing.T) {
	res, err := EvaluateCutTest(100, CutScore{Underfermented: 10, Purple: 5, Moldy: 0, Insect: 1, Good: 84}, testThresholds(), 2)
	if err != nil {
		t.Fatalf("EvaluateCutTest: %v", err)
	}
	if len(res.Anomalies) != 1 || res.Anomalies[0] != AnomalyCutDivergence {
		t.Fatalf("expected cut divergence anomaly, got %v", res.Anomalies)
	}
}

func TestEvaluateToxin(t *testing.T) {
	if !EvaluateToxin(5, catalog.ToxinThresholds{MaxToxinPPB: 10}) {
		t.Fatal("expected toxin within threshold to be accepted")
	}
	if EvaluateToxin(11, catalog.ToxinThresholds{MaxToxinPPB: 10}) {
		t.Fatal("expected toxin over threshold to be rejected")
	}
	if EvaluateToxin(-1, catalog.ToxinThresholds{MaxToxinPPB: 10}) {
		t.Fatal("expected negative toxin to be rejected")
	}
}

func TestEvaluateChemistry(t *testing.T) {
	thr := catalog.ChemistryThresholds{MinMoisturePercent: 5500, MaxMoisturePercent: 7500, MinPH: 500, MaxPH: 620, MaxFreeFattyAcidPercent: 150}
	if a := EvaluateChemistry(ChemistryReading{6500, 560, 100}, thr); len(a) != 0 {
		t.Fatalf("expected no anomalies, got %v", a)
	}
	if a := EvaluateChemistry(ChemistryReading{8000, 560, 100}, thr); len(a) == 0 {
		t.Fatal("expected moisture anomaly")
	}
	if a := EvaluateChemistry(ChemistryReading{6500, 560, -1}, thr); len(a) == 0 {
		t.Fatal("expected negative FFA anomaly")
	}
}
