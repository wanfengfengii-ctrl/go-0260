package service

import (
	"context"
	"sort"

	"cacaoferment/store"
	"cacaoferment/task"
)

// BoxingRequest is one qualified person's confirmation of the boxed weight.
// TaskID is filled by the API layer from the request path.
type BoxingRequest struct {
	TaskID           string `json:"-"`
	OperationKey     string `json:"operation_key"`
	Generation       int64  `json:"generation"`
	BoxerID          string `json:"boxer_id"`
	BoxedWeightGrams int64  `json:"boxed_weight_grams"`
}

// BoxingResult reports the boxers who have confirmed and the resulting state.
type BoxingResult struct {
	Task          task.FermentTask `json:"task"`
	BoxerIDs      []string         `json:"boxer_ids"`
	Confirmations int              `json:"confirmations"`
}

const boxingKind = "boxing_confirmation"

// ConfirmBoxing records one qualified boxing confirmation. Two distinct
// qualified people must confirm; the same operation key retried with identical
// content replays the stored response, and a content conflict is rejected
// without advancing state.
func (s *Service) ConfirmBoxing(ctx context.Context, req BoxingRequest) (BoxingResult, error) {
	reqHash := hashRequest(req)
	var result BoxingResult
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
		// Replay a previously recorded confirmation before the state guard: the
		// first boxer's confirmation may have succeeded while the task was in
		// pending_boxing, and a second boxer can since have advanced it to
		// equipment_occupied. Retrying the first boxer's original operation key
		// with identical content must idempotently return that stored result
		// rather than reject on the now-advanced state.
		if cached, err := s.resolveOperation(tx, t.TaskID, req.OperationKey, reqHash); err != nil {
			return err
		} else if cached != nil {
			boxers := currentBoxers(tx, t.TaskID)
			result = BoxingResult{Task: t, BoxerIDs: boxers, Confirmations: len(boxers)}
			return nil
		}
		if err := s.ensureState(t, task.StatePendingBoxing); err != nil {
			return err
		}
		snap, err := s.taskSnapshot(t)
		if err != nil {
			return err
		}
		if !snap.IsQualifiedReviewer(req.BoxerID) {
			return coded(CodeReviewerNotQualified, "boxer %q is not qualified", req.BoxerID)
		}
		if req.BoxedWeightGrams != t.BoxedWeightGrams {
			return coded(CodeInvalidWeight, "boxed weight %d does not match locked weight %d", req.BoxedWeightGrams, t.BoxedWeightGrams)
		}

		boxers := currentBoxers(tx, t.TaskID)
		for _, b := range boxers {
			if b == req.BoxerID {
				return coded(CodeConflict, "boxer %q already confirmed", req.BoxerID)
			}
		}
		boxers = append(boxers, req.BoxerID)
		sort.Strings(boxers)

		if len(boxers) >= 2 {
			t.State = task.StateEquipmentOccupied
		}
		if err := tx.SaveTask(ctx, t); err != nil {
			return err
		}
		if err := s.finishOperation(tx, &t, req.OperationKey, boxingKind, req.BoxerID, reqHash, map[string]any{
			"task": t, "boxer_ids": boxers, "confirmations": len(boxers),
		}); err != nil {
			return err
		}
		result = BoxingResult{Task: t, BoxerIDs: boxers, Confirmations: len(boxers)}
		return nil
	})
	return result, err
}

// currentBoxers returns the sorted, distinct boxing personnel recorded for a
// task from its boxing-confirmation operations.
func currentBoxers(tx store.Store, taskID string) []string {
	ops, _ := tx.ListOperations(context.Background(), taskID)
	seen := map[string]struct{}{}
	for _, o := range ops {
		if o.OperationKind == boxingKind {
			seen[o.ActorID] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
