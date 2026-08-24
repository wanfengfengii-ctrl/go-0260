package service_test

import (
	"context"
	"errors"
	"testing"

	"cacaoferment/arbiter"
	"cacaoferment/service"
	"cacaoferment/store"
)

func TestRejudgementStaleGeneration(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := mustLock(t, svc)
	_, err := svc.CreateRejudgement(context.Background(), service.RejudgementRequest{
		TaskID: id, OperationKey: "rej-1", Generation: 1, Kind: "temperature_break", Subjects: []string{"bin-1"},
	})
	if err != nil {
		t.Fatalf("rejudgement: %v", err)
	}
	// Generation advanced; a stale re-judgement must be rejected.
	_, err = svc.CreateRejudgement(context.Background(), service.RejudgementRequest{
		TaskID: id, OperationKey: "rej-2", Generation: 1, Kind: "mold_positive", Subjects: []string{"BC-1"},
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeGenerationMismatch {
		t.Fatalf("expected generation mismatch, got %v", err)
	}
}

func TestRejudgementDuplicateSubject(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := mustLock(t, svc)
	// Two subjects with the same key in one request must produce one evidence
	// and reject the duplicate.
	_, err := svc.CreateRejudgement(context.Background(), service.RejudgementRequest{
		TaskID: id, OperationKey: "rej-1", Generation: 1, Kind: "temperature_break", Subjects: []string{"bin-1", "bin-1"},
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeRejudgeExists {
		t.Fatalf("expected rejudgement exists, got %v", err)
	}
}

func TestRejudgementKindsProduceEvidence(t *testing.T) {
	kinds := []string{"temperature_break", "mold_positive", "cut_divergence", "chemistry_out_of_range"}
	for _, kind := range kinds {
		kind := kind
		t.Run(kind, func(t *testing.T) {
			svc := newTestService(t, store.NewMemory())
			id := mustLock(t, svc)
			res, err := svc.CreateRejudgement(context.Background(), service.RejudgementRequest{
				TaskID: id, OperationKey: "rej-1", Generation: 1, Kind: arbiter.AnomalyKind(kind), Subjects: []string{"bin-1"},
			})
			if err != nil {
				t.Fatalf("rejudgement: %v", err)
			}
			if len(res.Evidence) != 1 {
				t.Fatalf("expected one re-judgement evidence, got %d", len(res.Evidence))
			}
			if res.Evidence[0].RejectCode != kind {
				t.Fatalf("reject code = %q, want %q", res.Evidence[0].RejectCode, kind)
			}
		})
	}
}
