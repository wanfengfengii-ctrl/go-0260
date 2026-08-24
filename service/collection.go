package service

import (
	"context"
	"sort"

	"cacaoferment/arbiter"
	"cacaoferment/catalog"
	"cacaoferment/evidence"
	"cacaoferment/store"
	"cacaoferment/task"
)

// TurnReading is one turn-node coverage sample.
type TurnReading struct {
	BinID             string `json:"bin_id"`
	TurnNode          int    `json:"turn_node"`
	TemperatureCentiC int64  `json:"temperature_centi_c"`
	DurationMinutes   int64  `json:"duration_minutes"`
	TurnCount         int64  `json:"turn_count"`
	GapFillFlag       bool   `json:"gap_fill_flag"`
}

// TurnReadingsRequest submits a batch of coverage samples for the task.
type TurnReadingsRequest struct {
	TaskID       string        `json:"-"`
	OperationKey string        `json:"operation_key"`
	Generation   int64         `json:"generation"`
	Readings     []TurnReading `json:"readings"`
}

// TurnReadingsResult reports the written cells, the derived evidence, any
// temperature-break anomalies, and whether the turn schedule is complete.
type TurnReadingsResult struct {
	Task      task.FermentTask            `json:"task"`
	Cells     []evidence.TurnCoverageCell `json:"cells"`
	Anomalies []arbiter.AnomalyKind       `json:"anomalies,omitempty"`
	Complete  bool                        `json:"complete"`
}

const turnKind = "turn_readings"

// SubmitTurnReadings validates and persists a batch of coverage samples using
// deterministic integer rules. Invalid readings are rejected without entering
// valid cells; a slope outside the locked range is recorded as a temperature
// break but does not invalidate the otherwise-valid cell.
func (s *Service) SubmitTurnReadings(ctx context.Context, req TurnReadingsRequest) (TurnReadingsResult, error) {
	reqHash := hashRequest(req)
	var result TurnReadingsResult
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
		if err := s.ensureState(t, task.StateTurnCollection); err != nil {
			return err
		}
		snap, err := s.taskSnapshot(t)
		if err != nil {
			return err
		}
		if cached, err := s.resolveOperation(tx, t.TaskID, req.OperationKey, reqHash); err != nil {
			return err
		} else if cached != nil {
			result = TurnReadingsResult{Task: t, Cells: mustCells(tx, t.TaskID)}
			result.Complete = coverageComplete(snap, t.BinIDs, result.Cells)
			return nil
		}

		cells, anomalies, err := s.applyTurnReadings(ctx, tx, t, snap, req.Readings)
		if err != nil {
			return err
		}
		complete := coverageComplete(snap, t.BinIDs, cells)
		if complete {
			t.State = task.StateCutScoring
		}
		if err := tx.SaveTask(ctx, t); err != nil {
			return err
		}
		if err := s.finishOperation(tx, &t, req.OperationKey, turnKind, "", reqHash, map[string]any{
			"task": t, "cells": cells, "anomalies": anomalies, "complete": complete,
		}); err != nil {
			return err
		}
		result = TurnReadingsResult{Task: t, Cells: cells, Anomalies: anomalies, Complete: complete}
		return nil
	})
	return result, err
}

// applyTurnReadings validates and persists the batch, returning all coverage
// cells for the task and the detected anomalies.
func (s *Service) applyTurnReadings(ctx context.Context, tx store.Store, t task.FermentTask, snap catalog.RuleSnapshot, readings []TurnReading) ([]evidence.TurnCoverageCell, []arbiter.AnomalyKind, error) {
	existing, err := tx.ListCoverageCells(ctx, t.TaskID)
	if err != nil {
		return nil, nil, err
	}
	// Coverage is keyed per locked bin: each (bin, turn node) pair closes
	// independently so a partial-bin submission cannot be mistaken for full
	// coverage of the whole task.
	byCell := make(map[string]evidence.TurnCoverageCell, len(existing))
	for _, c := range existing {
		byCell[cellKeyOf(c.BinID, c.TurnNode)] = c
	}

	// Sort the incoming readings by bin then turn node for deterministic
	// processing, and reject intra-batch duplicates up front so a single request
	// cannot double-cover a (bin, node) slot.
	ordered := append([]TurnReading(nil), readings...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].BinID != ordered[j].BinID {
			return ordered[i].BinID < ordered[j].BinID
		}
		return ordered[i].TurnNode < ordered[j].TurnNode
	})
	seenInBatch := make(map[string]bool, len(ordered))
	for _, r := range ordered {
		if seenInBatch[cellKeyOf(r.BinID, r.TurnNode)] {
			return nil, nil, coded(CodeInvalidReading, "turn node %d for bin %q repeated in the same batch", r.TurnNode, r.BinID)
		}
		seenInBatch[cellKeyOf(r.BinID, r.TurnNode)] = true
	}

	var anomalies []arbiter.AnomalyKind
	for _, r := range ordered {
		if _, ok := turnBinIndex(t.BinIDs, r.BinID); !ok {
			return nil, nil, coded(CodeInvalidReading, "bin %q is not a locked bin", r.BinID)
		}
		nodeIndex, ok := turnNodeIndex(snap.TurnNodes, r.TurnNode)
		if !ok {
			return nil, nil, coded(CodeInvalidReading, "turn node %d is not in the locked schedule", r.TurnNode)
		}
		key := cellKeyOf(r.BinID, r.TurnNode)
		if _, dup := byCell[key]; dup {
			return nil, nil, coded(CodeInvalidReading, "turn node %d for bin %q already covered", r.TurnNode, r.BinID)
		}
		if r.GapFillFlag && nodeIndex == 0 {
			return nil, nil, coded(CodeInvalidReading, "gap fill is not allowed for the first turn node")
		}
		// Predecessor coverage for this bin is required to derive the slope.
		var prevTemp, prevDur int64
		if nodeIndex == 0 {
			prevTemp, prevDur = snap.TemperatureThresholds.AmbientCentiC, 0
		} else {
			prev, ok := byCell[cellKeyOf(r.BinID, snap.TurnNodes[nodeIndex-1])]
			if !ok {
				return nil, nil, coded(CodeInvalidReading, "turn node %d for bin %q is missing before node %d", snap.TurnNodes[nodeIndex-1], r.BinID, r.TurnNode)
			}
			prevTemp, prevDur = prev.TemperatureCentiC, prev.DurationMinutes
		}
		if r.DurationMinutes <= 0 {
			return nil, nil, coded(CodeInvalidReading, "turn node %d for bin %q has non-positive duration", r.TurnNode, r.BinID)
		}
		thr := snap.TemperatureThresholds
		if r.TemperatureCentiC < thr.MinCentiC || r.TemperatureCentiC > thr.MaxCentiC {
			return nil, nil, coded(CodeInvalidReading, "turn node %d for bin %q temperature out of range", r.TurnNode, r.BinID)
		}
		if r.DurationMinutes < thr.MinDurationMinutes || r.DurationMinutes > thr.MaxDurationMinutes {
			return nil, nil, coded(CodeInvalidReading, "turn node %d for bin %q duration out of range", r.TurnNode, r.BinID)
		}
		if r.TurnCount < 0 || r.TurnCount > thr.MaxTurnCount {
			return nil, nil, coded(CodeInvalidReading, "turn node %d for bin %q turn count out of range", r.TurnNode, r.BinID)
		}

		slope, err := evidence.DeriveSlope(prevTemp, r.TemperatureCentiC, prevDur, r.DurationMinutes)
		if err != nil {
			return nil, nil, coded(CodeInvalidReading, "turn node %d for bin %q slope derivation failed: %v", r.TurnNode, r.BinID, err)
		}
		cell := evidence.TurnCoverageCell{
			TaskID:            t.TaskID,
			Generation:        t.Generation,
			BinID:             r.BinID,
			TurnNode:          r.TurnNode,
			TemperatureCentiC: r.TemperatureCentiC,
			DurationMinutes:   r.DurationMinutes,
			TurnCount:         r.TurnCount,
			GapFillFlag:       r.GapFillFlag,
			SlopeMilliPerMin:  slope,
			Valid:             true,
		}
		if err := tx.SaveCoverageCell(ctx, cell); err != nil {
			return nil, nil, err
		}
		byCell[key] = cell

		nodeAnomalies := arbiter.EvaluateTemperature(r.TemperatureCentiC, slope, r.DurationMinutes, r.TurnCount, thr)
		for _, a := range nodeAnomalies {
			if !containsAnomaly(anomalies, a) {
				anomalies = append(anomalies, a)
			}
		}
		if err := s.recordTurnEvidence(ctx, tx, t, cell, nodeAnomalies); err != nil {
			return nil, nil, err
		}
	}

	all, err := tx.ListCoverageCells(ctx, t.TaskID)
	if err != nil {
		return nil, nil, err
	}
	return all, anomalies, nil
}

func (s *Service) recordTurnEvidence(ctx context.Context, tx store.Store, t task.FermentTask, cell evidence.TurnCoverageCell, anomalies []arbiter.AnomalyKind) error {
	accepted := len(anomalies) == 0
	reject := ""
	if len(anomalies) > 0 {
		reject = string(anomalies[0])
	}
	ev := evidence.EvidenceVersion{
		EvidenceID:    s.nextID("evidence"),
		TaskID:        t.TaskID,
		Generation:    t.Generation,
		EvidenceKind:  evidence.EvidenceTurnCoverage,
		SubjectKey:    cell.BinID + ":" + itoa(cell.TurnNode),
		VersionNo:     int64(cell.TurnNode),
		IntegerValues: []int64{cell.TemperatureCentiC, cell.DurationMinutes, cell.TurnCount, cell.SlopeMilliPerMin},
		Accepted:      accepted,
		RejectCode:    reject,
		CreatedAtTick: s.nextTick(),
	}
	ev.PayloadHash = hashBytes(encodeInts(ev.IntegerValues))
	return tx.SaveEvidence(ctx, ev)
}

func coverageComplete(snap catalog.RuleSnapshot, binIDs []string, cells []evidence.TurnCoverageCell) bool {
	// Coverage closes per locked bin: every locked bin must cover every locked
	// turn node before the task may advance. A partial-bin submission therefore
	// cannot be mistaken for full coverage of the whole task.
	have := make(map[string]map[int]bool, len(binIDs))
	for _, c := range cells {
		if have[c.BinID] == nil {
			have[c.BinID] = make(map[int]bool, len(snap.TurnNodes))
		}
		have[c.BinID][c.TurnNode] = true
	}
	for _, b := range binIDs {
		nodes, ok := have[b]
		if !ok {
			return false
		}
		for _, n := range snap.TurnNodes {
			if !nodes[n] {
				return false
			}
		}
	}
	return true
}

// cellKeyOf is the (bin, turn node) identity used to deduplicate coverage. It
// mirrors the store-level primary key so the service and store agree on what
// makes one coverage slot distinct.
func cellKeyOf(binID string, turnNode int) string {
	return binID + ":" + itoa(turnNode)
}

// turnBinIndex reports whether the given bin is one of the locked bins and
// returns its position.
func turnBinIndex(binIDs []string, binID string) (int, bool) {
	for i, b := range binIDs {
		if b == binID {
			return i, true
		}
	}
	return 0, false
}

func turnNodeIndex(nodes []int, node int) (int, bool) {
	for i, n := range nodes {
		if n == node {
			return i, true
		}
	}
	return 0, false
}

func containsAnomaly(list []arbiter.AnomalyKind, a arbiter.AnomalyKind) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}

func mustCells(tx store.Store, taskID string) []evidence.TurnCoverageCell {
	cells, _ := tx.ListCoverageCells(context.Background(), taskID)
	return cells
}
