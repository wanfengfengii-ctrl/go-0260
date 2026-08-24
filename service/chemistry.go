package service

import (
	"context"

	"cacaoferment/arbiter"
	"cacaoferment/evidence"
	"cacaoferment/store"
	"cacaoferment/task"
)

// ChemistryRequest submits the fixed-decimal moisture, pH, and free-fatty-acid
// retest readings.
type ChemistryRequest struct {
	TaskID               string `json:"-"`
	OperationKey         string `json:"operation_key"`
	Generation           int64  `json:"generation"`
	MoisturePercent      int64  `json:"moisture_percent"`
	PH                   int64  `json:"ph"`
	FreeFattyAcidPercent int64  `json:"free_fatty_acid_percent"`
}

// ChemistryResult reports the chemistry evidence and any out-of-range anomaly.
type ChemistryResult struct {
	Task      task.FermentTask      `json:"task"`
	Anomalies []arbiter.AnomalyKind `json:"anomalies,omitempty"`
	Accepted  bool                  `json:"accepted"`
}

const chemistryKind = "chemistry_readings"

// SubmitChemistry validates sign and digit length, evaluates the fixed-decimal
// readings against the locked thresholds, records the evidence, and advances
// the task to independent review when the retest is complete.
func (s *Service) SubmitChemistry(ctx context.Context, req ChemistryRequest) (ChemistryResult, error) {
	reqHash := hashRequest(req)
	var result ChemistryResult
	err := s.store.InTx(ctx, func(tx store.Store) error {
		t, err := tx.GetTask(ctx, req.TaskID)
		if err != nil {
			return err
		}
		if err := s.ensureGeneration(t, req.Generation); err != nil {
			return err
		}
		if err := s.ensureOpen(t); err != nil {
			return err
		}
		if err := s.ensureState(t, task.StateChemistryRetest); err != nil {
			return err
		}
		snap, err := s.taskSnapshot(t)
		if err != nil {
			return err
		}
		if cached, err := s.resolveOperation(tx, t.TaskID, req.OperationKey, reqHash); err != nil {
			return err
		} else if cached != nil {
			result = ChemistryResult{Task: t}
			return nil
		}

		for _, v := range []int64{req.MoisturePercent, req.PH, req.FreeFattyAcidPercent} {
			if v < 0 {
				return coded(CodeInvalidReading, "chemistry readings must be non-negative")
			}
			if err := evidence.CheckDigits(v, 9); err != nil {
				return coded(CodeInvalidReading, "chemistry reading has too many digits")
			}
		}
		reading := arbiter.ChemistryReading{
			MoisturePercent:      req.MoisturePercent,
			PH:                   req.PH,
			FreeFattyAcidPercent: req.FreeFattyAcidPercent,
		}
		anomalies := arbiter.EvaluateChemistry(reading, snap.ChemistryThresholds)
		reject := ""
		if len(anomalies) > 0 {
			reject = string(anomalies[0])
		}
		ev := evidence.EvidenceVersion{
			EvidenceID:    s.nextID("evidence"),
			TaskID:        t.TaskID,
			Generation:    t.Generation,
			EvidenceKind:  evidence.EvidenceChemistry,
			SubjectKey:    "chemistry",
			VersionNo:     1,
			IntegerValues: []int64{req.MoisturePercent, req.PH, req.FreeFattyAcidPercent},
			Accepted:      len(anomalies) == 0,
			RejectCode:    reject,
			CreatedAtTick: s.nextTick(),
		}
		ev.PayloadHash = hashBytes(encodeInts(ev.IntegerValues))

		t.State = task.StatePendingReview
		if err := tx.SaveTask(ctx, t); err != nil {
			return err
		}
		if err := s.finishOperation(tx, &t, req.OperationKey, chemistryKind, "", reqHash, map[string]any{
			"task": t, "anomalies": anomalies, "accepted": len(anomalies) == 0,
		}); err != nil {
			return err
		}
		result = ChemistryResult{Task: t, Anomalies: anomalies, Accepted: len(anomalies) == 0}
		return nil
	})
	return result, err
}
