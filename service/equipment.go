package service

import (
	"context"

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
			result = StartEquipmentResult{Task: t, Attempts: mustAttempts(tx, t.TaskID)}
			result.Started = t.State == task.StateTurnCollection
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
//
// A failed instrument imposes a retry window: the adapter may not be called
// again until the logical clock reaches the previously recorded RetryAfterTick.
// Re-entering StartEquipment inside that window does not re-drive the
// instrument or advance its fault script; instead an auditable AdapterPending
// attempt is recorded carrying the still-open window, so occupancy is never
// released early and no passing result is fabricated before the retry time.
func (s *Service) callAdapter(ctx context.Context, tx store.Store, t task.FermentTask, kind evidence.AdapterKind, target string) evidence.AdapterAttempt {
	logicalTick := s.nextTick()

	if prev, ok := lastAttemptFor(tx, t.TaskID, kind, target); ok && prev.Status != evidence.AdapterSucceeded && prev.RetryAfterTick > logicalTick {
		// Still within the retry window: record a pending attempt that carries
		// the open deadline without advancing the adapter's fault script.
		att := evidence.AdapterAttempt{
			AttemptID:       s.nextID("attempt"),
			TaskID:          t.TaskID,
			Generation:      t.Generation,
			AdapterKind:     kind,
			TargetKey:       target,
			ScriptStep:      prev.ScriptStep,
			LogicalTick:     logicalTick,
			Status:          evidence.AdapterPending,
			StableErrorCode: prev.StableErrorCode,
			RetryAfterTick:  prev.RetryAfterTick,
		}
		_ = tx.SaveAdapterAttempt(ctx, att)
		return att
	}

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

// lastAttemptFor returns the most recently recorded attempt for one instrument
// target of a task, or ok=false if none exists. It is used to enforce the
// retry-after window before re-driving an adapter.
func lastAttemptFor(tx store.Store, taskID string, kind evidence.AdapterKind, target string) (evidence.AdapterAttempt, bool) {
	attempts, _ := tx.ListAdapterAttempts(context.Background(), taskID)
	var last evidence.AdapterAttempt
	found := false
	for _, a := range attempts {
		if a.AdapterKind != kind || a.TargetKey != target {
			continue
		}
		// ListAdapterAttempts is ordered by logical_tick, so a later entry is
		// always more recent.
		last = a
		found = true
	}
	return last, found
}

func mustAttempts(tx store.Store, taskID string) []evidence.AdapterAttempt {
	attempts, _ := tx.ListAdapterAttempts(context.Background(), taskID)
	return attempts
}
