package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"cacaoferment/arbiter"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_FinalizeIdempotentTerminalReplay(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		restart bool
	}{
		{name: "same process after terminal state", restart: false},
		{name: "after sqlite restart", restart: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var st store.Store
			dbDir := filepath.Join(t.TempDir(), "db")
			if tc.restart {
				var err error
				st, err = store.OpenSQLite(ctx, dbDir)
				if err != nil {
					t.Fatalf("open sqlite: %v", err)
				}
			} else {
				st = store.NewMemory()
			}

			svc := newTestService(t, st)
			taskID := driveToPendingReview(t, svc)
			review(t, svc, taskID, "reviewer-c", evidence.DecisionApprove)
			review(t, svc, taskID, "reviewer-d", evidence.DecisionApprove)

			req := service.FinalizeRequest{
				TaskID:       taskID,
				OperationKey: "finalize-ready-shared-key",
				Generation:   1,
				Decision:     arbiter.DecisionReadyToDry,
			}
			first, err := svc.Finalize(ctx, req)
			if err != nil {
				t.Fatalf("first finalize: %v", err)
			}
			if first.Task.State != task.StateReadyToDry {
				t.Fatalf("first state = %q, want ready_to_dry", first.Task.State)
			}
			if first.Credential.WinnerOperationKey != req.OperationKey {
				t.Fatalf("winner key = %q, want %q", first.Credential.WinnerOperationKey, req.OperationKey)
			}

			replaySvc := svc
			if tc.restart {
				var err error
				st, err = store.OpenSQLite(ctx, dbDir)
				if err != nil {
					t.Fatalf("reopen sqlite: %v", err)
				}
				replaySvc = newTestService(t, st)
			}

			second, err := replaySvc.Finalize(ctx, req)
			if err != nil {
				t.Fatalf("idempotent replay: %v", err)
			}
			if !reflect.DeepEqual(second.Task, first.Task) {
				t.Fatalf("replay task changed:\nfirst:  %+v\nsecond: %+v", first.Task, second.Task)
			}
			if !reflect.DeepEqual(second.Credential, first.Credential) {
				t.Fatalf("replay credential changed:\nfirst:  %+v\nsecond: %+v", first.Credential, second.Credential)
			}

			conflicting := req
			conflicting.Decision = arbiter.DecisionRiskIsolated
			_, err = replaySvc.Finalize(ctx, conflicting)
			var ce *service.CodedError
			if !errors.As(err, &ce) || ce.Code != service.CodeConflict {
				t.Fatalf("same key with changed content error = %v, want content_conflict", err)
			}

			newKey := req
			newKey.OperationKey = "finalize-ready-new-key"
			_, err = replaySvc.Finalize(ctx, newKey)
			if !errors.As(err, &ce) || ce.Code != service.CodeAlreadyTerminal {
				t.Fatalf("new key terminal write error = %v, want already_terminal", err)
			}

			audit, err := replaySvc.Audit(ctx, taskID)
			if err != nil {
				t.Fatalf("audit: %v", err)
			}
			if audit.Credential == nil || !reflect.DeepEqual(*audit.Credential, first.Credential) {
				t.Fatalf("stored credential changed after replay/conflict/new key: %+v", audit.Credential)
			}
			finalizeOps := 0
			for _, op := range audit.Operations {
				if op.OperationKind == "finalize" {
					finalizeOps++
				}
			}
			if finalizeOps != 1 {
				t.Fatalf("finalize operation records = %d, want 1", finalizeOps)
			}
		})
	}
}
