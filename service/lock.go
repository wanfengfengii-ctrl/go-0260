package service

import (
	"context"
	"sort"

	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/lease"
	"cacaoferment/store"
	"cacaoferment/task"
)

// BlindSampleSpec is one frozen sample coded at lock time. BinID is the hidden
// origin bin that is only revealed once the toxin gate opens.
type BlindSampleSpec struct {
	BlindCode  string `json:"blind_code"`
	SampleSize int64  `json:"sample_size"`
	BinID      string `json:"bin_id"`
}

// LockRequest freezes the plot, variety batch, bins, probes, plate wells,
// drying window, boxed weight, blind samples, and rule version into a new
// task generation.
type LockRequest struct {
	OperationKey     string            `json:"operation_key,omitempty"`
	RuleVersion      string            `json:"rule_version"`
	PlotID           string            `json:"plot_id"`
	VarietyBatchID   string            `json:"variety_batch_id"`
	BinIDs           []string          `json:"bin_ids"`
	ProbeIDs         []string          `json:"probe_ids,omitempty"`
	PlateWellIDs     []string          `json:"plate_well_ids,omitempty"`
	DryingWindowID   string            `json:"drying_window_id,omitempty"`
	BoxedWeightGrams int64             `json:"boxed_weight_grams"`
	BlindSamples     []BlindSampleSpec `json:"blind_samples,omitempty"`
}

// LockResult is the outcome of a successful lock.
type LockResult struct {
	Task         task.FermentTask       `json:"task"`
	Leases       []lease.LeaseRecord    `json:"leases"`
	BlindSamples []evidence.BlindSample `json:"blind_samples"`
}

// Lock creates a new joint-inspection task and atomically acquires the bin,
// probe, plate-well, and drying-window leases. Any validation failure or lease
// conflict aborts the whole transaction, leaving no partial task or leases.
func (s *Service) Lock(ctx context.Context, req LockRequest) (LockResult, error) {
	snap, ok := s.catalog.Snapshot(catalog.ThresholdVersion(req.RuleVersion))
	if !ok {
		return LockResult{}, coded(CodeUnknownRuleVersion, "unknown rule version %q", req.RuleVersion)
	}
	if err := snap.Validate(); err != nil {
		return LockResult{}, coded(CodeInvalidRequest, "invalid rule snapshot: %v", err)
	}
	if !s.catalog.MatchPlotVariety(req.PlotID, req.VarietyBatchID) {
		return LockResult{}, codedReasons(CodePlotVarietyMismatch, "plot and variety batch do not match", []string{req.PlotID, req.VarietyBatchID})
	}
	if req.BoxedWeightGrams <= 0 {
		return LockResult{}, coded(CodeInvalidWeight, "boxed weight must be positive")
	}
	if dup := duplicates(req.BinIDs); len(dup) > 0 {
		return LockResult{}, codedReasons(CodeDuplicateBin, "duplicate fermentation bin", dup)
	}
	if dup := duplicateBlindCodes(req.BlindSamples); len(dup) > 0 {
		return LockResult{}, codedReasons(CodeDuplicateBlind, "duplicate blind code", dup)
	}
	if err := s.validateBlindBins(req); err != nil {
		return LockResult{}, err
	}
	if s.equipment != nil {
		if err := s.equipment.Validate(req.BinIDs, req.ProbeIDs, req.PlateWellIDs, req.DryingWindowID); err != nil {
			return LockResult{}, coded(CodeUnknownEquipment, "%v", err)
		}
	}

	taskID := s.nextID("task")
	gen := int64(1)
	tick := s.nextTick()
	t := task.FermentTask{
		TaskID:            taskID,
		Generation:        gen,
		State:             task.StatePendingBoxing,
		PlotID:            req.PlotID,
		VarietyBatchID:    req.VarietyBatchID,
		BinIDs:            append([]string(nil), req.BinIDs...),
		BoxedWeightGrams:  req.BoxedWeightGrams,
		DryingWindowID:    req.DryingWindowID,
		LockedRuleVersion: req.RuleVersion,
		CreatedAtTick:     tick,
	}

	leases := lease.BuildLeases(lease.LeaseRequest{
		TaskID:         taskID,
		Generation:     gen,
		BinIDs:         req.BinIDs,
		ProbeIDs:       req.ProbeIDs,
		PlateWellIDs:   req.PlateWellIDs,
		DryingWindowID: req.DryingWindowID,
		Tick:           tick,
	})

	blinds := s.buildBlindSamples(taskID, gen, req.BlindSamples)

	result := LockResult{Task: t, Leases: leases, BlindSamples: blinds}
	err := s.store.InTx(ctx, func(tx store.Store) error {
		if err := tx.SaveTask(ctx, t); err != nil {
			return err
		}
		for _, l := range leases {
			if err := s.mustLease(tx, l); err != nil {
				return err
			}
		}
		for _, b := range blinds {
			if err := tx.SaveBlindSample(ctx, b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return LockResult{}, err
	}
	return result, nil
}

// validateBlindBins ensures every blind sample references a locked bin.
func (s *Service) validateBlindBins(req LockRequest) error {
	binSet := make(map[string]struct{}, len(req.BinIDs))
	for _, b := range req.BinIDs {
		binSet[b] = struct{}{}
	}
	for _, spec := range req.BlindSamples {
		if spec.SampleSize <= 0 {
			return coded(CodeInvalidRequest, "blind sample %q must have a positive sample size", spec.BlindCode)
		}
		if _, ok := binSet[spec.BinID]; !ok {
			return codedReasons(CodeInvalidRequest, "blind sample references an unlocked bin", []string{spec.BlindCode, spec.BinID})
		}
	}
	return nil
}

func (s *Service) buildBlindSamples(taskID string, gen int64, specs []BlindSampleSpec) []evidence.BlindSample {
	out := make([]evidence.BlindSample, 0, len(specs))
	for _, spec := range specs {
		out = append(out, evidence.BlindSample{
			BlindCode:     spec.BlindCode,
			TaskID:        taskID,
			Generation:    gen,
			SampleSize:    spec.SampleSize,
			AssignedBinID: spec.BinID,
			Sealed:        false,
		})
	}
	return out
}

func duplicates(values []string) []string {
	seen := make(map[string]int, len(values))
	for _, v := range values {
		seen[v]++
	}
	var out []string
	for v, n := range seen {
		if n > 1 {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func duplicateBlindCodes(specs []BlindSampleSpec) []string {
	seen := make(map[string]int, len(specs))
	for _, s := range specs {
		seen[s.BlindCode]++
	}
	var out []string
	for c, n := range seen {
		if n > 1 {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}
