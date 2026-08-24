package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"cacaoferment/arbiter"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_SQLiteRestartAllocatesFreshTaskAndKeepsExistingAudit(t *testing.T) {
	ctx := context.Background()

	newService := func(t *testing.T, st store.Store) *service.Service {
		t.Helper()
		c, _ := testSeed()
		return service.New(st, c, nil, nil)
	}

	idSuffix := func(id string) int64 {
		i := strings.LastIndex(id, "-")
		if i < 0 || i == len(id)-1 {
			return 0
		}
		n, err := strconv.ParseInt(id[i+1:], 10, 64)
		if err != nil {
			return 0
		}
		return n
	}

	maxAuditSeq := func(v service.AuditView) int64 {
		max := idSuffix(v.Task.TaskID)
		for _, ev := range v.Evidence {
			if n := idSuffix(ev.EvidenceID); n > max {
				max = n
			}
		}
		for _, attempt := range v.AdapterAttempts {
			if n := idSuffix(attempt.AttemptID); n > max {
				max = n
			}
		}
		if v.Credential != nil {
			if n := idSuffix(v.Credential.CredentialID); n > max {
				max = n
			}
		}
		return max
	}

	maxAuditTick := func(v service.AuditView) int64 {
		max := v.Task.CreatedAtTick
		for _, l := range v.Leases {
			if l.AcquiredAtTick > max {
				max = l.AcquiredAtTick
			}
		}
		for _, op := range v.Operations {
			if op.CreatedAtTick > max {
				max = op.CreatedAtTick
			}
		}
		for _, ev := range v.Evidence {
			if ev.CreatedAtTick > max {
				max = ev.CreatedAtTick
			}
		}
		for _, attempt := range v.AdapterAttempts {
			if attempt.LogicalTick > max {
				max = attempt.LogicalTick
			}
		}
		for _, review := range v.Reviews {
			if review.CreatedAtTick > max {
				max = review.CreatedAtTick
			}
		}
		if v.Credential != nil && v.Credential.IssuedAtTick > max {
			max = v.Credential.IssuedAtTick
		}
		return max
	}

	freshLock := func() service.LockRequest {
		req := defaultLock()
		req.BinIDs = []string{"bin-next-1", "bin-next-2"}
		req.ProbeIDs = []string{"probe-next"}
		req.PlateWellIDs = []string{"well-next"}
		req.DryingWindowID = "window-next"
		req.BlindSamples = []service.BlindSampleSpec{
			{BlindCode: "BC-N1", SampleSize: 100, BinID: "bin-next-1"},
			{BlindCode: "BC-N2", SampleSize: 100, BinID: "bin-next-2"},
		}
		return req
	}

	cases := []struct {
		name           string
		prepare        func(*testing.T, *service.Service) string
		wantState      task.State
		wantCredential bool
	}{
		{
			name: "open task with evidence and attempts",
			prepare: func(t *testing.T, svc *service.Service) string {
				t.Helper()
				id := mustLock(t, svc)
				box(t, svc, id, "reviewer-a")
				box(t, svc, id, "reviewer-b")
				startEquipment(t, svc, id)
				collect(t, svc, id)
				return id
			},
			wantState: task.StateCutScoring,
		},
		{
			name: "terminal task with credential",
			prepare: func(t *testing.T, svc *service.Service) string {
				t.Helper()
				id := driveToPendingReview(t, svc)
				review(t, svc, id, "reviewer-c", evidence.DecisionApprove)
				review(t, svc, id, "reviewer-d", evidence.DecisionApprove)
				if _, err := svc.Finalize(ctx, service.FinalizeRequest{
					TaskID: id, OperationKey: "finalize", Generation: 1, Decision: arbiter.DecisionReadyToDry,
				}); err != nil {
					t.Fatalf("finalize: %v", err)
				}
				return id
			},
			wantState:      task.StateReadyToDry,
			wantCredential: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "db")
			st1, err := store.OpenSQLite(ctx, dir)
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			svc1 := newService(t, st1)
			existingID := tc.prepare(t, svc1)
			before, err := svc1.Audit(ctx, existingID)
			if err != nil {
				t.Fatalf("audit before restart: %v", err)
			}
			if before.Task.State != tc.wantState {
				t.Fatalf("state before restart = %q, want %q", before.Task.State, tc.wantState)
			}
			if (before.Credential != nil) != tc.wantCredential {
				t.Fatalf("credential before restart present = %v, want %v", before.Credential != nil, tc.wantCredential)
			}
			seqBefore := maxAuditSeq(before)
			tickBefore := maxAuditTick(before)

			st2, err := store.OpenSQLite(ctx, dir)
			if err != nil {
				t.Fatalf("reopen sqlite: %v", err)
			}
			svc2 := newService(t, st2)
			if _, err := svc2.Lock(ctx, defaultLock()); err == nil {
				t.Fatal("same resources locked after restart; want stable resource_occupied conflict")
			} else {
				var coded *service.CodedError
				if !errors.As(err, &coded) || coded.Code != service.CodeResourceOccupied {
					t.Fatalf("same-resource lock error = %v, want %s", err, service.CodeResourceOccupied)
				}
			}

			next, err := svc2.Lock(ctx, freshLock())
			if err != nil {
				t.Fatalf("fresh lock after restart: %v", err)
			}
			if next.Task.TaskID == existingID {
				t.Fatalf("fresh lock reused existing task id %q after restart", existingID)
			}
			if got := idSuffix(next.Task.TaskID); got <= seqBefore {
				t.Fatalf("fresh task id suffix = %d, want > previous generated high watermark %d", got, seqBefore)
			}
			if next.Task.CreatedAtTick <= tickBefore {
				t.Fatalf("fresh task tick = %d, want > previous logical high watermark %d", next.Task.CreatedAtTick, tickBefore)
			}

			after, err := svc2.Audit(ctx, existingID)
			if err != nil {
				t.Fatalf("audit existing after fresh lock: %v", err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("existing task audit changed after restart and fresh lock\nbefore: %+v\nafter:  %+v", before, after)
			}
			tasks, err := svc2.List(ctx)
			if err != nil {
				t.Fatalf("list tasks: %v", err)
			}
			if len(tasks) != 2 {
				t.Fatalf("task count after fresh lock = %d, want 2", len(tasks))
			}
		})
	}
}
