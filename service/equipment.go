package service

import (
	"context"
	"encoding/json"

	"cacaoferment/adapter"
	"cacaoferment/evidence"
	"cacaoferment/lease"
	"cacaoferment/store"
	"cacaoferment/task"
)

// StartEquipmentRequest activates the temperature probes and cut-test plate
// wells occupied at lock time.
type StartEquipmentRequest struct {
	TaskID       string `json:"-"`
	OperationKey string `json:"operation_key"`
	Generation   int64  `json:"generation"`
}

// StartEquipmentResult reports the instrument attempts and whether the task
// advanced to turn collection.
type StartEquipmentResult struct {
	Task     task.FermentTask          `json:"task"`
	Attempts []evidence.AdapterAttempt `json:"attempts"`
	Started  bool                      `json:"started"`
}

const startKind = "start_equipment"

// StartEquipment drives the probe and plate-reader adapters for each occupied
// probe and plate well. Failed instruments produce auditable retry attempts
// without advancing state and without releasing occupancy.
func (s *Service) StartEquipment(ctx context.Context, req StartEquipmentRequest) (StartEquipmentResult, error) {
	reqHash := hashRequest(req)
	var result StartEquipmentResult
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
		if err := s.ensureState(t, task.StateEquipmentOccupied); err != nil {
			return err
		}
		if cached, err := s.resolveOperation(tx, t.TaskID, req.OperationKey, reqHash); err != nil {
			return err
		} else if cached != nil {
			// Replay the exact stored response. Re-querying the live attempts
			// would fold in attempts produced by later retries under different
			// operation keys, so the same idempotent replay would grow from
			// the original attempt set to the accumulated one. The task may
			// also have advanced since the original call, so the started flag
			// is taken from the stored response rather than recomputed.
			if err := json.Unmarshal(cached, &result); err != nil {
				return err
			}
			return nil
		}

		leases, err := tx.ListLeases(ctx, t.TaskID)
		if err != nil {
			return err
		}
		attempts, allOK := s.driveEquipment(ctx, tx, t, leases)
		if allOK {
			t.State = task.StateTurnCollection
		}
		if err := tx.SaveTask(ctx, t); err != nil {
			return err
		}
		if err := s.finishOperation(tx, &t, req.OperationKey, startKind, "", reqHash, map[string]any{
			"task": t, "attempts": attempts, "started": allOK,
		}); err != nil {
			return err
		}
		result = StartEquipmentResult{Task: t, Attempts: attempts, Started: allOK}
		return nil
	})
	return result, err
}

// driveEquipment calls the probe adapter for every probe lease and the toxin
// reader adapter for every plate-well lease, persisting one attempt each. It
// returns the attempts and whether every instrument succeeded.
func (s *Service) driveEquipment(ctx context.Context, tx store.Store, t task.FermentTask, leases []lease.LeaseRecord) ([]evidence.AdapterAttempt, bool) {
	var attempts []evidence.AdapterAttempt
	allOK := true
	for _, l := range leases {
		var kind evidence.AdapterKind
		switch l.ResourceType {
		case lease.ResourceProbe:
			kind = evidence.AdapterProbe
		case lease.ResourcePlateWell:
			kind = evidence.AdapterToxinReader
		default:
			continue
		}
		att := s.callAdapter(ctx, tx, t, kind, l.ResourceID)
		attempts = append(attempts, att)
		if att.Status != evidence.AdapterSucceeded {
			allOK = false
		}
	}
	return attempts, allOK
}

// callAdapter invokes one instrument, records the attempt, and returns it.
func (s *Service) callAdapter(ctx context.Context, tx store.Store, t task.FermentTask, kind evidence.AdapterKind, target string) evidence.AdapterAttempt {
	logicalTick := s.nextTick()
	res := adapter.Result{Status: evidence.AdapterSucceeded}
	if s.adapters != nil {
		if a := s.adapters.Get(kind); a != nil {
			res = a.Call(target)
		}
	}
	retryAfter := int64(0)
	if res.Status != evidence.AdapterSucceeded {
		retryAfter = logicalTick + res.RetryAfterTicks
	}
	att := evidence.AdapterAttempt{
		AttemptID:       s.nextID("attempt"),
		TaskID:          t.TaskID,
		Generation:      t.Generation,
		AdapterKind:     kind,
		TargetKey:       target,
		ScriptStep:      res.StepIndex,
		LogicalTick:     logicalTick,
		Status:          res.Status,
		StableErrorCode: res.StableErrorCode,
		RawDigest:       res.RawDigest,
		RetryAfterTick:  retryAfter,
	}
	_ = tx.SaveAdapterAttempt(ctx, att)
	return att
}
