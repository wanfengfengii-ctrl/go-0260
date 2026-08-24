package service_test

import (
	"context"
	"errors"
	"testing"

	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestBoxingIdempotentRetry(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := mustLock(t, svc)
	req := service.BoxingRequest{
		TaskID: id, OperationKey: "box-1", Generation: 1, BoxerID: "reviewer-a", BoxedWeightGrams: 120000,
	}
	first, err := svc.ConfirmBoxing(context.Background(), req)
	if err != nil {
		t.Fatalf("first boxing: %v", err)
	}
	second, err := svc.ConfirmBoxing(context.Background(), req)
	if err != nil {
		t.Fatalf("retry boxing: %v", err)
	}
	if first.Task.State != second.Task.State || len(first.BoxerIDs) != len(second.BoxerIDs) {
		t.Fatalf("retry changed result: %+v vs %+v", first, second)
	}
}

func TestBoxingContentConflict(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := mustLock(t, svc)
	_, err := svc.ConfirmBoxing(context.Background(), service.BoxingRequest{
		TaskID: id, OperationKey: "box-1", Generation: 1, BoxerID: "reviewer-a", BoxedWeightGrams: 120000,
	})
	if err != nil {
		t.Fatalf("boxing: %v", err)
	}
	// Same operation key, different content: must be a content conflict.
	_, err = svc.ConfirmBoxing(context.Background(), service.BoxingRequest{
		TaskID: id, OperationKey: "box-1", Generation: 1, BoxerID: "reviewer-a", BoxedWeightGrams: 99999,
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeConflict {
		t.Fatalf("expected content conflict, got %v", err)
	}
	v, _ := svc.Get(context.Background(), id)
	if v.Task.State != task.StatePendingBoxing {
		t.Fatalf("state advanced on conflict: %q", v.Task.State)
	}
}

func TestBoxingRequiresDistinctQualifiedBoxers(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := mustLock(t, svc)
	box(t, svc, id, "reviewer-a")
	// Same boxer again must be rejected.
	_, err := svc.ConfirmBoxing(context.Background(), service.BoxingRequest{
		TaskID: id, OperationKey: "box-a2", Generation: 1, BoxerID: "reviewer-a", BoxedWeightGrams: 120000,
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeConflict {
		t.Fatalf("expected duplicate boxer conflict, got %v", err)
	}
	// Unqualified boxer must be rejected.
	_, err = svc.ConfirmBoxing(context.Background(), service.BoxingRequest{
		TaskID: id, OperationKey: "box-x", Generation: 1, BoxerID: "reviewer-x", BoxedWeightGrams: 120000,
	})
	if !errors.As(err, &ce) || ce.Code != service.CodeReviewerNotQualified {
		t.Fatalf("expected unqualified error, got %v", err)
	}
}
