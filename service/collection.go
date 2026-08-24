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
			result.Complete = coverageComplete(snap, result.Cells)
			return nil
		}

		cells, anomalies, err := s.applyTurnReadings(ctx, tx, t, snap, req.Readings)
		if err != nil {
			return err
		}
		complete := coverageComplete(snap, cells)
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
	byNode := make(map[int]evidence.TurnCoverageCell, len(existing))
	for _, c := range existing {
		byNode[c.TurnNode] = c
	}

	// Sort the incoming readings by turn node for deterministic processing.
	ordered := append([]TurnReading(nil), readings...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].TurnNode < ordered[j].TurnNode })

	var anomalies []arbiter.AnomalyKind
	for _, r := range ordered {
		nodeIndex, ok := turnNodeIndex(snap.TurnNodes, r.TurnNode)
		if !ok {
			return nil, nil, coded(CodeInvalidReading, "turn node %d is not in the locked schedule", r.TurnNode)
		}
		if _, dup := byNode[r.TurnNode]; dup {
			return nil, nil, coded(CodeInvalidReading, "turn node %d already covered", r.TurnNode)
		}
		if r.GapFillFlag && nodeIndex == 0 {
			return nil, nil, coded(CodeInvalidReading, "gap fill is not allowed for the first turn node")
		}
		// Predecessor coverage is required to derive the slope.
		var prevTemp, prevDur int64
		if nodeIndex == 0 {
			prevTemp, prevDur = snap.TemperatureThresholds.AmbientCentiC, 0
		} else {
			prev, ok := byNode[snap.TurnNodes[nodeIndex-1]]
			if !ok {
				return nil, nil, coded(CodeInvalidReading, "turn node %d is missing before node %d", snap.TurnNodes[nodeIndex-1], r.TurnNode)
			}
			prevTemp, prevDur = prev.TemperatureCentiC, prev.DurationMinutes
		}
		if r.DurationMinutes <= 0 {
			return nil, nil, coded(CodeInvalidReading, "turn node %d has non-positive duration", r.TurnNode)
		}
		thr := snap.TemperatureThresholds
		if r.TemperatureCentiC < thr.MinCentiC || r.TemperatureCentiC > thr.MaxCentiC {
			return nil, nil, coded(CodeInvalidReading, "turn node %d temperature out of range", r.TurnNode)
		}
		if r.DurationMinutes < thr.MinDurationMinutes || r.DurationMinutes > thr.MaxDurationMinutes {
			return nil, nil, coded(CodeInvalidReading, "turn node %d duration out of range", r.TurnNode)
		}
		if r.TurnCount < 0 || r.TurnCount > thr.MaxTurnCount {
			return nil, nil, coded(CodeInvalidReading, "turn node %d turn count out of range", r.TurnNode)
		}

		slope, err := evidence.DeriveSlope(prevTemp, r.TemperatureCentiC, prevDur, r.DurationMinutes)
		if err != nil {
			return nil, nil, coded(CodeInvalidReading, "turn node %d slope derivation failed: %v", r.TurnNode, err)
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
		byNode[r.TurnNode] = cell

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

func coverageComplete(snap catalog.RuleSnapshot, cells []evidence.TurnCoverageCell) bool {
	if len(cells) < len(snap.TurnNodes) {
		return false
	}
	have := make(map[int]bool, len(cells))
	for _, c := range cells {
		have[c.TurnNode] = true
	}
	for _, n := range snap.TurnNodes {
		if !have[n] {
			return false
		}
	}
	return true
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
