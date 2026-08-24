package service

import (
	"context"

	"cacaoferment/arbiter"
	"cacaoferment/evidence"
	"cacaoferment/store"
	"cacaoferment/task"
)

// RejudgementRequest creates a current-generation re-judgement for one or more
// affected subjects (bins, blind codes, or plate wells).
type RejudgementRequest struct {
	TaskID       string              `json:"-"`
	OperationKey string              `json:"operation_key"`
	Generation   int64               `json:"generation"`
	Kind         arbiter.AnomalyKind `json:"kind"`
	Subjects     []string            `json:"subjects"`
}

// RejudgementResult reports the re-judgement evidence and the new generation.
type RejudgementResult struct {
	Task     task.FermentTask           `json:"task"`
	Evidence []evidence.EvidenceVersion `json:"evidence"`
}

const rejudgeKind = "rejudgement"

// CreateRejudgement records one re-judgement evidence per subject for the
// current generation and then advances the task generation. A second
// re-judgement of the same subject in the same generation is rejected, and a
// stale generation is a stable conflict.
func (s *Service) CreateRejudgement(ctx context.Context, req RejudgementRequest) (RejudgementResult, error) {
	reqHash := hashRequest(req)
	var result RejudgementResult
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
		if cached, err := s.resolveOperation(tx, t.TaskID, req.OperationKey, reqHash); err != nil {
			return err
		} else if cached != nil {
			result = RejudgementResult{Task: t, Evidence: mustEvidence(tx, t.TaskID)}
			return nil
		}

		var created []evidence.EvidenceVersion
		for _, subject := range req.Subjects {
			ev := evidence.EvidenceVersion{
				EvidenceID:    s.nextID("evidence"),
				TaskID:        t.TaskID,
				Generation:    t.Generation,
				EvidenceKind:  evidence.EvidenceRejudgement,
				SubjectKey:    subject,
				VersionNo:     1,
				IntegerValues: []int64{t.Generation},
				Accepted:      false,
				RejectCode:    string(req.Kind),
				CreatedAtTick: s.nextTick(),
			}
			ev.PayloadHash = hashBytes(encodeInts(ev.IntegerValues))
			if err := tx.SaveEvidence(ctx, ev); err != nil {
				if err == store.ErrDuplicate {
					return coded(CodeRejudgeExists, "re-judgement already exists for %q", subject)
				}
				return err
			}
			created = append(created, ev)
		}

		t.Generation++
		if err := tx.SaveTask(ctx, t); err != nil {
			return err
		}
		if err := s.finishOperation(tx, &t, req.OperationKey, rejudgeKind, "", reqHash, map[string]any{
			"task": t, "evidence": created,
		}); err != nil {
			return err
		}
		result = RejudgementResult{Task: t, Evidence: created}
		return nil
	})
	return result, err
}

// ReviewRequest is one independent reviewer's decision.
type ReviewRequest struct {
	TaskID       string            `json:"-"`
	OperationKey string            `json:"operation_key"`
	Generation   int64             `json:"generation"`
	ReviewerID   string            `json:"reviewer_id"`
	Decision     evidence.Decision `json:"decision"`
	ReasonCode   string            `json:"reason_code,omitempty"`
}

// ReviewResult reports the recorded review and the resulting task state.
type ReviewResult struct {
	Task    task.FermentTask        `json:"task"`
	Reviews []evidence.ReviewRecord `json:"reviews"`
}

const reviewKind = "independent_review"

// SubmitReview records one independent review from a qualified reviewer who is
// not one of the boxing personnel. The same reviewer may decide only once.
func (s *Service) SubmitReview(ctx context.Context, req ReviewRequest) (ReviewResult, error) {
	reqHash := hashRequest(req)
	var result ReviewResult
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
		if err := s.ensureState(t, task.StatePendingReview); err != nil {
			return err
		}
		snap, err := s.taskSnapshot(t)
		if err != nil {
			return err
		}
		if !snap.IsQualifiedReviewer(req.ReviewerID) {
			return coded(CodeReviewerNotQualified, "reviewer %q is not qualified", req.ReviewerID)
		}
		for _, b := range currentBoxers(tx, t.TaskID) {
			if b == req.ReviewerID {
				return coded(CodeBoxerOverlap, "reviewer %q was a boxing personnel", req.ReviewerID)
			}
		}
		if cached, err := s.resolveOperation(tx, t.TaskID, req.OperationKey, reqHash); err != nil {
			return err
		} else if cached != nil {
			result = ReviewResult{Task: t, Reviews: mustReviews(tx, t.TaskID)}
			return nil
		}

		rec := evidence.ReviewRecord{
			TaskID:        t.TaskID,
			Generation:    t.Generation,
			ReviewerID:    req.ReviewerID,
			ReviewKind:    evidence.ReviewIndependent,
			Decision:      req.Decision,
			ReasonCode:    req.ReasonCode,
			CreatedAtTick: s.nextTick(),
		}
		if err := tx.SaveReview(ctx, rec); err != nil {
			if err == store.ErrDuplicate {
				return coded(CodeConflict, "reviewer %q already reviewed", req.ReviewerID)
			}
			return err
		}
		if err := s.finishOperation(tx, &t, req.OperationKey, reviewKind, req.ReviewerID, reqHash, map[string]any{
			"task": t, "reviews": mustReviews(tx, t.TaskID),
		}); err != nil {
			return err
		}
		result = ReviewResult{Task: t, Reviews: mustReviews(tx, t.TaskID)}
		return nil
	})
	return result, err
}

// FinalizeRequest competes to write the single terminal outcome.
type FinalizeRequest struct {
	TaskID       string                   `json:"-"`
	OperationKey string                   `json:"operation_key"`
	Generation   int64                    `json:"generation"`
	Decision     arbiter.TerminalDecision `json:"decision"`
}

// FinalizeResult reports the winning credential and terminal task.
type FinalizeResult struct {
	Task       task.FermentTask         `json:"task"`
	Credential evidence.FinalCredential `json:"credential"`
}

// Finalize enforces the terminal single-write barrier: the first successful
// competitor produces the only credential; later competitors receive a stable
// already-terminal error. Ready-to-dry and risk-isolated require two distinct
// qualified reviewers; cancellation is always permitted from an open task.
func (s *Service) Finalize(ctx context.Context, req FinalizeRequest) (FinalizeResult, error) {
	reqHash := hashRequest(req)
	var result FinalizeResult
	err := s.store.InTx(ctx, func(tx store.Store) error {
		t, err := tx.GetTask(ctx, req.TaskID)
		if err != nil {
			return err
		}
		// Idempotent retry takes precedence over every state check. A finalize that
		// already committed — most often because the client timed out after the
		// credential was issued — must replay its stored terminal result instead of
		// being rejected by the open/generation/state checks below, which now see a
		// terminal task. The request hash pins the content, so a reused key with
		// different content still surfaces as a stable content conflict.
		if cached, err := s.resolveOperation(tx, t.TaskID, req.OperationKey, reqHash); err != nil {
			return err
		} else if cached != nil {
			if cred, ok, _ := tx.GetCredential(ctx, t.TaskID); ok {
				result = FinalizeResult{Task: t, Credential: cred}
			} else {
				result = FinalizeResult{Task: t}
			}
			return nil
		}

		if err := s.ensureGeneration(t, req.Generation); err != nil {
			return err
		}
		if err := s.ensureOpen(t); err != nil {
			return err
		}
		if err := s.ensureState(t, task.StatePendingReview); err != nil {
			return err
		}
		if !req.Decision.Valid() {
			return coded(CodeInvalidDecision, "invalid terminal decision %q", req.Decision)
		}

		snap, err := s.taskSnapshot(t)
		if err != nil {
			return err
		}
		boxers := currentBoxers(tx, t.TaskID)
		reviews := mustReviews(tx, t.TaskID)
		verdict := arbiter.AssessReviews(reviews, boxers, snap)
		hasRejudge := hasRejudgement(tx, t.TaskID)

		if req.Decision != arbiter.DecisionCancelled {
			if !verdict.Complete {
				return coded(CodeReviewsIncomplete, "at least two distinct qualified reviewers required")
			}
			if req.Decision == arbiter.DecisionReadyToDry && (verdict.Rejections > 0 || hasRejudge) {
				return coded(CodeInvalidDecision, "ready-to-dry requires unanimous approval and no re-judgement")
			}
			if req.Decision == arbiter.DecisionRiskIsolated && verdict.Rejections == 0 && !hasRejudge {
				return coded(CodeInvalidDecision, "risk-isolated requires a rejection or a re-judgement")
			}
		}

		tick := s.nextTick()
		cred := evidence.FinalCredential{
			TaskID:             t.TaskID,
			Generation:         t.Generation,
			CredentialID:       s.nextID("credential"),
			TerminalState:      string(req.Decision.State()),
			WinnerOperationKey: req.OperationKey,
			IssuedAtTick:       tick,
			LeasedWindowID:     t.DryingWindowID,
			Digest:             arbiter.CredentialDigest(t.TaskID, t.Generation, req.Decision.State(), req.OperationKey, tick),
		}
		if err := tx.SaveCredential(ctx, cred); err != nil {
			if err == store.ErrDuplicate {
				return coded(CodeAlreadyTerminal, "task already finalized")
			}
			return err
		}
		t.State = req.Decision.State()
		t.TerminalResult = string(req.Decision)
		if err := tx.SaveTask(ctx, t); err != nil {
			return err
		}
		if err := s.finishOperation(tx, &t, req.OperationKey, "finalize", "", reqHash, map[string]any{
			"task": t, "credential": cred,
		}); err != nil {
			return err
		}
		result = FinalizeResult{Task: t, Credential: cred}
		return nil
	})
	return result, err
}

func hasRejudgement(tx store.Store, taskID string) bool {
	evs, _ := tx.ListEvidence(context.Background(), taskID)
	for _, e := range evs {
		if e.EvidenceKind == evidence.EvidenceRejudgement {
			return true
		}
	}
	return false
}

func mustEvidence(tx store.Store, taskID string) []evidence.EvidenceVersion {
	evs, _ := tx.ListEvidence(context.Background(), taskID)
	return evs
}

func mustReviews(tx store.Store, taskID string) []evidence.ReviewRecord {
	reviews, _ := tx.ListReviews(context.Background(), taskID)
	return reviews
}
