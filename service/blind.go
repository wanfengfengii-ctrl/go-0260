package service

import (
	"context"

	"cacaoferment/arbiter"
	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/store"
	"cacaoferment/task"
)

// BlindSampleRequest submits either a cut-test score (during cut scoring) or a
// toxin reading (during toxin verification) for one coded sample. BlindCode is
// filled by the API layer from the request path.
type BlindSampleRequest struct {
	TaskID       string            `json:"-"`
	BlindCode    string            `json:"-"`
	OperationKey string            `json:"operation_key"`
	Generation   int64             `json:"generation"`
	CutScore     *arbiter.CutScore `json:"cut_score,omitempty"`
	ToxinPPB     *int64            `json:"toxin_ppb,omitempty"`
}

// BlindSampleResult reports the updated sample, the produced evidence, any
// anomalies, whether the reveal gate opened, and the resulting task state.
type BlindSampleResult struct {
	Task        task.FermentTask           `json:"task"`
	BlindSample evidence.BlindSample       `json:"blind_sample"`
	Evidence    []evidence.EvidenceVersion `json:"evidence"`
	Anomalies   []arbiter.AnomalyKind      `json:"anomalies,omitempty"`
	Revealed    bool                       `json:"revealed"`
}

// SubmitBlindSample records a cut-test score or toxin reading for a coded
// sample, advancing the state machine as scores and toxin readings complete.
// The blind-code to bin mapping is revealed only once the toxin gate opens.
func (s *Service) SubmitBlindSample(ctx context.Context, req BlindSampleRequest) (BlindSampleResult, error) {
	reqHash := hashRequest(req)
	var result BlindSampleResult
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
		snap, err := s.taskSnapshot(t)
		if err != nil {
			return err
		}
		sample, err := findBlindSample(ctx, tx, t.TaskID, req.BlindCode)
		if err != nil {
			return err
		}
		if cached, err := s.resolveOperation(tx, t.TaskID, req.OperationKey, reqHash); err != nil {
			return err
		} else if cached != nil {
			result = BlindSampleResult{Task: t, BlindSample: sample}
			return nil
		}

		var anomalies []arbiter.AnomalyKind
		switch t.State {
		case task.StateCutScoring:
			anomalies, err = s.recordCutScore(ctx, tx, t, snap, sample, req)
			if err != nil {
				return err
			}
			if allBlindScored(ctx, tx, t.TaskID) {
				t.State = task.StateToxinVerification
			}
		case task.StateToxinVerification:
			var revealed bool
			revealed, err = s.recordToxin(ctx, tx, t, snap, sample, req)
			if err != nil {
				return err
			}
			if revealed {
				allSealed, rerr := s.revealIfComplete(ctx, tx, t)
				if rerr != nil {
					return rerr
				}
				if allSealed {
					t.State = task.StateChemistryRetest
				}
			}
		default:
			return s.ensureState(t, task.StateCutScoring, task.StateToxinVerification)
		}

		if err := tx.SaveTask(ctx, t); err != nil {
			return err
		}
		sample, _ = findBlindSample(ctx, tx, t.TaskID, req.BlindCode)
		evs, _ := tx.ListEvidence(ctx, t.TaskID)
		if err := s.finishOperation(tx, &t, req.OperationKey, "blind_sample", "", reqHash, map[string]any{
			"task": t, "blind_sample": sample, "anomalies": anomalies,
		}); err != nil {
			return err
		}
		result = BlindSampleResult{Task: t, BlindSample: sample, Evidence: evs, Anomalies: anomalies, Revealed: sample.Sealed}
		return nil
	})
	return result, err
}

// recordCutScore validates bean conservation, derives percentages, and stores
// the cut-score evidence plus any cut/mould anomalies.
func (s *Service) recordCutScore(ctx context.Context, tx store.Store, t task.FermentTask, snap catalog.RuleSnapshot, sample evidence.BlindSample, req BlindSampleRequest) ([]arbiter.AnomalyKind, error) {
	if req.CutScore == nil {
		return nil, coded(CodeInvalidRequest, "cut score required during cut scoring")
	}
	res, err := arbiter.EvaluateCutTest(sample.SampleSize, *req.CutScore, snap.CutTestThresholds, snap.FixedScale)
	if err != nil {
		return nil, coded(CodeGrainMismatch, "cut test for %q failed: %v", sample.BlindCode, err)
	}
	reject := ""
	if !res.Accepted && len(res.Anomalies) > 0 {
		reject = string(res.Anomalies[0])
	}
	ev := evidence.EvidenceVersion{
		EvidenceID:    s.nextID("evidence"),
		TaskID:        t.TaskID,
		Generation:    t.Generation,
		EvidenceKind:  evidence.EvidenceCutScore,
		SubjectKey:    sample.BlindCode,
		VersionNo:     1,
		IntegerValues: []int64{res.UnderfermentedPercent, res.PurplePercent, res.MoldyPercent, res.InsectPercent, res.GoodPercent},
		Accepted:      res.Accepted,
		RejectCode:    reject,
		CreatedAtTick: s.nextTick(),
	}
	ev.PayloadHash = hashBytes(encodeInts(ev.IntegerValues))
	if err := tx.SaveEvidence(ctx, ev); err != nil {
		return nil, err
	}
	return res.Anomalies, nil
}

// recordToxin validates and stores a toxin reading. It returns whether this
// reading closed the toxin gate for the sample.
func (s *Service) recordToxin(ctx context.Context, tx store.Store, t task.FermentTask, snap catalog.RuleSnapshot, sample evidence.BlindSample, req BlindSampleRequest) (bool, error) {
	if req.ToxinPPB == nil {
		return false, coded(CodeInvalidRequest, "toxin reading required during toxin verification")
	}
	if sample.Sealed {
		return false, coded(CodeRevealLocked, "blind sample %q is already sealed", sample.BlindCode)
	}
	accepted := arbiter.EvaluateToxin(*req.ToxinPPB, snap.ToxinThresholds)
	reject := ""
	if !accepted {
		reject = "toxin_over_limit"
	}
	ev := evidence.EvidenceVersion{
		EvidenceID:    s.nextID("evidence"),
		TaskID:        t.TaskID,
		Generation:    t.Generation,
		EvidenceKind:  evidence.EvidenceToxin,
		SubjectKey:    sample.BlindCode,
		VersionNo:     1,
		IntegerValues: []int64{*req.ToxinPPB},
		Accepted:      accepted,
		RejectCode:    reject,
		CreatedAtTick: s.nextTick(),
	}
	ev.PayloadHash = hashBytes(encodeInts(ev.IntegerValues))
	if err := tx.SaveEvidence(ctx, ev); err != nil {
		return false, err
	}
	return true, nil
}

// revealIfComplete opens the reveal gate only when every blind sample has a
// toxin reading, then maps each coded sample to its assigned bin exactly once.
// A partial toxin set leaves every sample sealed.
func (s *Service) revealIfComplete(ctx context.Context, tx store.Store, t task.FermentTask) (bool, error) {
	samples, err := tx.ListBlindSamples(ctx, t.TaskID)
	if err != nil {
		return false, err
	}
	evs, err := tx.ListEvidence(ctx, t.TaskID)
	if err != nil {
		return false, err
	}
	toxin := make(map[string]bool)
	for _, e := range evs {
		if e.EvidenceKind == evidence.EvidenceToxin {
			toxin[e.SubjectKey] = true
		}
	}
	for _, smp := range samples {
		if !toxin[smp.BlindCode] {
			return false, nil
		}
	}
	for _, smp := range samples {
		if smp.Sealed {
			continue
		}
		smp.RevealedBinID = smp.AssignedBinID
		smp.RevealTick = s.nextTick()
		smp.RevealGeneration = t.Generation
		smp.Sealed = true
		if err := tx.SaveBlindSample(ctx, smp); err != nil {
			return false, err
		}
	}
	return true, nil
}

func findBlindSample(ctx context.Context, tx store.Store, taskID, code string) (evidence.BlindSample, error) {
	samples, err := tx.ListBlindSamples(ctx, taskID)
	if err != nil {
		return evidence.BlindSample{}, err
	}
	for _, s := range samples {
		if s.BlindCode == code {
			return s, nil
		}
	}
	return evidence.BlindSample{}, coded(CodeNotBlindFound, "blind sample %q not found", code)
}

func allBlindScored(ctx context.Context, tx store.Store, taskID string) bool {
	samples, err := tx.ListBlindSamples(ctx, taskID)
	if err != nil {
		return false
	}
	evs, err := tx.ListEvidence(ctx, taskID)
	if err != nil {
		return false
	}
	scored := make(map[string]bool)
	for _, e := range evs {
		if e.EvidenceKind == evidence.EvidenceCutScore {
			scored[e.SubjectKey] = true
		}
	}
	for _, s := range samples {
		if !scored[s.BlindCode] {
			return false
		}
	}
	return true
}
