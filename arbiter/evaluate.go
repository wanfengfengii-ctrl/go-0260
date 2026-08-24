package arbiter

import (
	"cacaoferment/catalog"
	"cacaoferment/evidence"
)

// AnomalyKind classifies the re-judgement triggers that arise during
// collection, cut-test scoring, and chemistry retest.
type AnomalyKind string

const (
	// AnomalyTemperatureBreak flags a turn-node temperature or slope outside
	// the locked temperature thresholds.
	AnomalyTemperatureBreak AnomalyKind = "temperature_break"
	// AnomalyMoldPositive flags a cut-test moldy percentage above threshold.
	AnomalyMoldPositive AnomalyKind = "mold_positive"
	// AnomalyCutDivergence flags underfermented, purple, or insect percentages
	// above their locked thresholds.
	AnomalyCutDivergence AnomalyKind = "cut_divergence"
	// AnomalyChemistryOutOfRange flags moisture, pH, or free-fatty-acid outside
	// the locked chemistry thresholds.
	AnomalyChemistryOutOfRange AnomalyKind = "chemistry_out_of_range"
)

// CutScore is the five-way bean classification for one blind sample.
type CutScore struct {
	Underfermented int64
	Purple         int64
	Moldy          int64
	Insect         int64
	Good           int64
}

// CutResult is the derived evaluation of a cut-test score.
type CutResult struct {
	UnderfermentedPercent int64
	PurplePercent         int64
	MoldyPercent          int64
	InsectPercent         int64
	GoodPercent           int64
	Accepted              bool
	Anomalies             []AnomalyKind
}

// EvaluateCutTest checks bean conservation, derives fixed-decimal percentages,
// and classifies any threshold breach. It returns ErrGrainMismatch (or another
// arithmetic error) when the counts do not conserve the sample size; threshold
// breaches are returned as anomalies rather than errors so the score can still
// be recorded as evidence.
func EvaluateCutTest(sampleSize int64, score CutScore, thr catalog.CutTestThresholds, scale int) (CutResult, error) {
	if err := evidence.ConserveGrains(sampleSize, score.Underfermented, score.Purple, score.Moldy, score.Insect, score.Good); err != nil {
		return CutResult{}, err
	}
	under, err := evidence.PercentOf(score.Underfermented, sampleSize, scale)
	if err != nil {
		return CutResult{}, err
	}
	purple, err := evidence.PercentOf(score.Purple, sampleSize, scale)
	if err != nil {
		return CutResult{}, err
	}
	moldy, err := evidence.PercentOf(score.Moldy, sampleSize, scale)
	if err != nil {
		return CutResult{}, err
	}
	insect, err := evidence.PercentOf(score.Insect, sampleSize, scale)
	if err != nil {
		return CutResult{}, err
	}
	good, err := evidence.PercentOf(score.Good, sampleSize, scale)
	if err != nil {
		return CutResult{}, err
	}
	res := CutResult{
		UnderfermentedPercent: under,
		PurplePercent:         purple,
		MoldyPercent:          moldy,
		InsectPercent:         insect,
		GoodPercent:           good,
		Accepted:              true,
	}
	if moldy > thr.MaxMoldyPercent {
		res.Anomalies = append(res.Anomalies, AnomalyMoldPositive)
	}
	if under > thr.MaxUnderfermentedPercent || purple > thr.MaxPurplePercent || insect > thr.MaxInsectPercent {
		res.Anomalies = append(res.Anomalies, AnomalyCutDivergence)
	}
	if len(res.Anomalies) > 0 {
		res.Accepted = false
	}
	return res, nil
}

// EvaluateToxin checks a toxin reading against the locked maximum. A reading at
// or below the threshold is accepted.
func EvaluateToxin(ppb int64, thr catalog.ToxinThresholds) bool {
	return ppb >= 0 && ppb <= thr.MaxToxinPPB
}

// ChemistryReading is a fixed-decimal moisture, pH, and free-fatty-acid sample.
type ChemistryReading struct {
	MoisturePercent      int64
	PH                   int64
	FreeFattyAcidPercent int64
}

// EvaluateChemistry checks a chemistry reading against the locked bounds and
// returns the anomaly kinds that are out of range. All values are fixed-decimal
// integers already scaled by the snapshot's fixed scale.
func EvaluateChemistry(r ChemistryReading, thr catalog.ChemistryThresholds) []AnomalyKind {
	var anomalies []AnomalyKind
	if r.MoisturePercent < thr.MinMoisturePercent || r.MoisturePercent > thr.MaxMoisturePercent {
		anomalies = append(anomalies, AnomalyChemistryOutOfRange)
	}
	if r.PH < thr.MinPH || r.PH > thr.MaxPH {
		anomalies = append(anomalies, AnomalyChemistryOutOfRange)
	}
	if r.FreeFattyAcidPercent > thr.MaxFreeFattyAcidPercent || r.FreeFattyAcidPercent < 0 {
		anomalies = append(anomalies, AnomalyChemistryOutOfRange)
	}
	return anomalies
}

// EvaluateTemperature classifies a single coverage reading against the locked
// temperature thresholds. It reports a temperature-break anomaly when the
// temperature or derived slope leaves the allowed range, or when the duration
// or turn count is out of bounds.
func EvaluateTemperature(tempCentiC, slopeMilliPerMin, durationMinutes, turnCount int64, thr catalog.TemperatureThresholds) []AnomalyKind {
	var anomalies []AnomalyKind
	if tempCentiC < thr.MinCentiC || tempCentiC > thr.MaxCentiC {
		anomalies = append(anomalies, AnomalyTemperatureBreak)
	}
	if slopeMilliPerMin < thr.MinSlopeMilliPerMin || slopeMilliPerMin > thr.MaxSlopeMilliPerMin {
		anomalies = append(anomalies, AnomalyTemperatureBreak)
	}
	if durationMinutes < thr.MinDurationMinutes || durationMinutes > thr.MaxDurationMinutes {
		anomalies = append(anomalies, AnomalyTemperatureBreak)
	}
	if turnCount < 0 || turnCount > thr.MaxTurnCount {
		anomalies = append(anomalies, AnomalyTemperatureBreak)
	}
	return anomalies
}
