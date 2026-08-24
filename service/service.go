// Package service orchestrates the fermentation-to-drying joint-inspection
// business flows. It is the single writer that runs each logical operation in
// one store transaction, enforcing the state machine, generation matching,
// idempotent operation keys, lease arbitration, evidence immutability, the
// blind-code reveal gate, re-judgement, independent review, and the terminal
// single-write barrier.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"cacaoferment/adapter"
	"cacaoferment/catalog"
	"cacaoferment/lease"
	"cacaoferment/store"
	"cacaoferment/task"
)

// Stable error codes returned across the HTTP API. They are kept stable so
// clients and tests can assert on them deterministically.
const (
	CodeInvalidRequest       = "invalid_request"
	CodeNotFound             = "not_found"
	CodeInternal             = "internal"
	CodeUnknownRuleVersion   = "unknown_rule_version"
	CodePlotVarietyMismatch  = "plot_variety_mismatch"
	CodeInvalidWeight        = "invalid_weight"
	CodeDuplicateBin         = "duplicate_bin"
	CodeDuplicateBlind       = "duplicate_blind_code"
	CodeUnknownEquipment     = "unknown_equipment"
	CodeResourceOccupied     = "resource_occupied"
	CodeGenerationMismatch   = "generation_mismatch"
	CodeInvalidState         = "invalid_state"
	CodeAlreadyTerminal      = "already_terminal"
	CodeInvalidDecision      = "invalid_decision"
	CodeConflict             = "content_conflict"
	CodeInvalidReading       = "invalid_reading"
	CodeGrainMismatch        = "grain_mismatch"
	CodeReviewerNotQualified = "reviewer_not_qualified"
	CodeBoxerOverlap         = "boxer_overlap"
	CodeReviewsIncomplete    = "reviews_incomplete"
	CodeRevealLocked         = "reveal_locked"
	CodeNotBlindFound        = "blind_sample_not_found"
	CodeRejudgeExists        = "rejudgement_exists"
)

// CodedError is a stable rejection with a machine-readable code and a
// deterministically ordered list of reasons.
type CodedError struct {
	Code    string
	Message string
	Reasons []string
}

func (e *CodedError) Error() string {
	if len(e.Reasons) > 0 {
		return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Reasons)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func coded(code, format string, args ...any) *CodedError {
	return &CodedError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func codedReasons(code, format string, reasons []string) *CodedError {
	return &CodedError{Code: code, Message: format, Reasons: reasons}
}

// Service wires the store, catalog, equipment directory, adapters, and the
// deterministic tick/sequence generators into the business flows.
type Service struct {
	store     store.Store
	catalog   catalog.RuleCatalog
	equipment *catalog.EquipmentDirectory
	adapters  *adapter.Registry
	seq       atomic.Int64
	tick      atomic.Int64
}

// New constructs a Service. equipment and adapters may be nil to disable
// equipment validation and instrument calls respectively.
func New(st store.Store, cat catalog.RuleCatalog, equip *catalog.EquipmentDirectory, reg *adapter.Registry) *Service {
	return &Service{store: st, catalog: cat, equipment: equip, adapters: reg}
}

func (s *Service) nextID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, s.seq.Add(1))
}

func (s *Service) nextTick() int64 {
	return s.tick.Add(1)
}

// hashRequest produces a deterministic hash of a request payload used for
// idempotent retry and content-conflict detection.
func hashRequest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// resolveOperation implements the idempotency prefix shared by every write
// flow: an existing operation with the same key and hash returns its stored
// response bytes, while a different hash is a content conflict.
func (s *Service) resolveOperation(tx store.Store, taskID, key, reqHash string) ([]byte, error) {
	rec, ok, err := tx.GetOperation(context.Background(), taskID, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	if rec.RequestHash != reqHash {
		return nil, coded(CodeConflict, "operation key %q reused with different content", key)
	}
	return rec.ResponseBody, nil
}

// finishOperation records the idempotent operation entry after a successful
// write, preserving the exact serialized response for future replays.
func (s *Service) finishOperation(tx store.Store, t *task.FermentTask, key, kind, actor, reqHash string, response any) error {
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	rec := task.OperationRecord{
		TaskID:        t.TaskID,
		OperationKey:  key,
		OperationKind: kind,
		ActorID:       actor,
		RequestHash:   reqHash,
		ResponseHash:  hashBytes(body),
		ResponseBody:  body,
		StatusCode:    200,
		CreatedAtTick: s.nextTick(),
	}
	return tx.SaveOperation(context.Background(), rec)
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// taskSnapshot resolves the frozen rule snapshot for a task and returns an
// error when the locked rule version is no longer registered.
func (s *Service) taskSnapshot(t task.FermentTask) (catalog.RuleSnapshot, error) {
	snap, ok := s.catalog.Snapshot(catalog.ThresholdVersion(t.LockedRuleVersion))
	if !ok {
		return catalog.RuleSnapshot{}, coded(CodeUnknownRuleVersion, "locked rule version %q no longer registered", t.LockedRuleVersion)
	}
	return snap, nil
}

// ensureGeneration checks the request generation against the task generation.
func (s *Service) ensureGeneration(t task.FermentTask, generation int64) error {
	if generation != 0 && generation != t.Generation {
		return coded(CodeGenerationMismatch, "generation %d does not match task generation %d", generation, t.Generation)
	}
	return nil
}

// ensureOpen rejects writes against a terminal task.
func (s *Service) ensureOpen(t task.FermentTask) error {
	if t.IsTerminal() {
		return coded(CodeAlreadyTerminal, "task %s is already %s", t.TaskID, t.State)
	}
	return nil
}

// ensureState rejects an operation when the task is not in one of the allowed
// states.
func (s *Service) ensureState(t task.FermentTask, allowed ...task.State) error {
	for _, st := range allowed {
		if t.State == st {
			return nil
		}
	}
	return coded(CodeInvalidState, "task %s is in state %s", t.TaskID, t.State)
}

// mustLease wraps a lease save so a resource conflict surfaces as a stable code.
func (s *Service) mustLease(tx store.Store, l lease.LeaseRecord) error {
	if err := tx.SaveLease(context.Background(), l); err != nil {
		if err == store.ErrResourceOccupied {
			return codedReasons(CodeResourceOccupied, "resource already occupied", []string{string(l.ResourceType) + ":" + l.ResourceID})
		}
		return err
	}
	return nil
}
