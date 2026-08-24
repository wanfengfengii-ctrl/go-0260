package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"cacaoferment/arbiter"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestFinalizeRequiresTwoReviewers(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := driveToPendingReview(t, svc)
	review(t, svc, id, "reviewer-c", evidence.DecisionApprove)
	_, err := svc.Finalize(context.Background(), service.FinalizeRequest{
		TaskID: id, OperationKey: "fin", Generation: 1, Decision: arbiter.DecisionReadyToDry,
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeReviewsIncomplete {
		t.Fatalf("expected reviews incomplete, got %v", err)
	}
}

func TestFinalizeSingleWinnerConcurrent(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := driveToPendingReview(t, svc)
	review(t, svc, id, "reviewer-c", evidence.DecisionApprove)
	review(t, svc, id, "reviewer-d", evidence.DecisionApprove)

	const n = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.Finalize(context.Background(), service.FinalizeRequest{
				TaskID: id, OperationKey: "fin-" + string(rune('a'+i)), Generation: 1, Decision: arbiter.DecisionReadyToDry,
			})
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
			continue
		}
		var ce *service.CodedError
		if !errors.As(err, &ce) || ce.Code != service.CodeAlreadyTerminal {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("expected exactly one winner, got %d", winners)
	}
}

func TestFinalizeTerminalRejectsWrites(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := driveToPendingReview(t, svc)
	review(t, svc, id, "reviewer-c", evidence.DecisionApprove)
	review(t, svc, id, "reviewer-d", evidence.DecisionApprove)
	res, err := svc.Finalize(context.Background(), service.FinalizeRequest{
		TaskID: id, OperationKey: "fin", Generation: 1, Decision: arbiter.DecisionReadyToDry,
	})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if res.Credential.TerminalState != string(task.StateReadyToDry) {
		t.Fatalf("unexpected terminal state %q", res.Credential.TerminalState)
	}
	// Any later write is rejected without changing state.
	_, err = svc.SubmitReview(context.Background(), service.ReviewRequest{
		TaskID: id, OperationKey: "rev-late", Generation: 1, ReviewerID: "reviewer-a", Decision: evidence.DecisionApprove,
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeAlreadyTerminal {
		t.Fatalf("expected already terminal, got %v", err)
	}
}

func TestFinalizeReadyToDryRejectedOnRejection(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := driveToPendingReview(t, svc)
	review(t, svc, id, "reviewer-c", evidence.DecisionApprove)
	review(t, svc, id, "reviewer-d", evidence.DecisionReject)
	_, err := svc.Finalize(context.Background(), service.FinalizeRequest{
		TaskID: id, OperationKey: "fin", Generation: 1, Decision: arbiter.DecisionReadyToDry,
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeInvalidDecision {
		t.Fatalf("expected invalid decision for ready-to-dry with rejection, got %v", err)
	}
	// Risk isolation is the valid terminal outcome for a rejection.
	if _, err := svc.Finalize(context.Background(), service.FinalizeRequest{
		TaskID: id, OperationKey: "fin2", Generation: 1, Decision: arbiter.DecisionRiskIsolated,
	}); err != nil {
		t.Fatalf("risk isolated: %v", err)
	}
}
