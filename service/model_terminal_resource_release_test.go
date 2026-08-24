package service_test

import (
	"context"
	"errors"
	"testing"

	"cacaoferment/arbiter"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_TerminalFinalizeReleasesResourcesForNextLock(t *testing.T) {
	ctx := context.Background()
	reviewerIDs := []string{"reviewer-c", "reviewer-d"}

	requireCode := func(t *testing.T, err error, code string) {
		t.Helper()
		var ce *service.CodedError
		if !errors.As(err, &ce) {
			t.Fatalf("expected coded error %q, got %v", code, err)
		}
		if ce.Code != code {
			t.Fatalf("error code = %q, want %q", ce.Code, code)
		}
	}

	storeCases := []struct {
		name string
		open func(*testing.T) store.Store
	}{
		{
			name: "memory",
			open: func(t *testing.T) store.Store {
				t.Helper()
				return store.NewMemory()
			},
		},
		{
			name: "sqlite",
			open: func(t *testing.T) store.Store {
				t.Helper()
				st, err := store.OpenSQLite(ctx, t.TempDir())
				if err != nil {
					t.Fatalf("open sqlite: %v", err)
				}
				return st
			},
		},
	}

	cases := []struct {
		name     string
		decision arbiter.TerminalDecision
		reviews  []evidence.Decision
		want     task.State
	}{
		{
			name:     "ready_to_dry",
			decision: arbiter.DecisionReadyToDry,
			reviews:  []evidence.Decision{evidence.DecisionApprove, evidence.DecisionApprove},
			want:     task.StateReadyToDry,
		},
		{
			name:     "risk_isolated",
			decision: arbiter.DecisionRiskIsolated,
			reviews:  []evidence.Decision{evidence.DecisionApprove, evidence.DecisionReject},
			want:     task.StateRiskIsolated,
		},
		{
			name:     "cancelled",
			decision: arbiter.DecisionCancelled,
			want:     task.StateCancelled,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, sc := range storeCases {
				t.Run(sc.name, func(t *testing.T) {
					st := sc.open(t)
					svc := newTestService(t, st)
					id := driveToPendingReview(t, svc)

					_, err := svc.Finalize(ctx, service.FinalizeRequest{
						TaskID:       id,
						OperationKey: "premature-ready-" + tc.name,
						Generation:   1,
						Decision:     arbiter.DecisionReadyToDry,
					})
					requireCode(t, err, service.CodeReviewsIncomplete)
					if _, ok, err := st.GetCredential(ctx, id); err != nil {
						t.Fatalf("get credential after failed finalize: %v", err)
					} else if ok {
						t.Fatal("failed finalize left a credential behind")
					}
					_, err = svc.Lock(ctx, defaultLock())
					requireCode(t, err, service.CodeResourceOccupied)

					for i, decision := range tc.reviews {
						review(t, svc, id, reviewerIDs[i], decision)
					}
					res, err := svc.Finalize(ctx, service.FinalizeRequest{
						TaskID:       id,
						OperationKey: "final-" + tc.name,
						Generation:   1,
						Decision:     tc.decision,
					})
					if err != nil {
						t.Fatalf("finalize %s: %v", tc.decision, err)
					}
					if res.Task.State != tc.want {
						t.Fatalf("state = %q, want %q", res.Task.State, tc.want)
					}
					if res.Credential.TerminalState != string(tc.want) {
						t.Fatalf("credential terminal state = %q, want %q", res.Credential.TerminalState, tc.want)
					}

					leases, err := st.ListLeases(ctx, id)
					if err != nil {
						t.Fatalf("list leases: %v", err)
					}
					for _, l := range leases {
						if l.Active() {
							t.Fatalf("terminal task still actively leases %s:%s", l.ResourceType, l.ResourceID)
						}
					}

					_, err = svc.Finalize(ctx, service.FinalizeRequest{
						TaskID:       id,
						OperationKey: "late-final-" + tc.name,
						Generation:   1,
						Decision:     tc.decision,
					})
					requireCode(t, err, service.CodeAlreadyTerminal)
					_, err = svc.SubmitReview(ctx, service.ReviewRequest{
						TaskID:       id,
						OperationKey: "late-review-" + tc.name,
						Generation:   1,
						ReviewerID:   "reviewer-a",
						Decision:     evidence.DecisionApprove,
					})
					requireCode(t, err, service.CodeAlreadyTerminal)

					next, err := svc.Lock(ctx, defaultLock())
					if err != nil {
						t.Fatalf("lock with resources released by %s task: %v", tc.want, err)
					}
					if next.Task.TaskID == id {
						t.Fatal("next lock reused finalized task id")
					}
					_, err = svc.Lock(ctx, defaultLock())
					requireCode(t, err, service.CodeResourceOccupied)
				})
			}
		})
	}
}
