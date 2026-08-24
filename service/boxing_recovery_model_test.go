package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestModel_BoxingConfirmationProtocolRecovery(t *testing.T) {
	ctx := context.Background()
	boxingReq := func(taskID, key, boxer string) service.BoxingRequest {
		return service.BoxingRequest{
			TaskID:           taskID,
			OperationKey:     key,
			Generation:       1,
			BoxerID:          boxer,
			BoxedWeightGrams: 120000,
		}
	}
	setupLocked := func(t *testing.T) (*service.Service, string) {
		t.Helper()
		svc := newTestService(t, store.NewMemory())
		return svc, mustLock(t, svc)
	}
	setupRestartedAfterTwoBoxers := func(t *testing.T) (*service.Service, string, service.BoxingRequest) {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "db")
		st1, err := store.OpenSQLite(ctx, dir)
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		svc1 := newTestService(t, st1)
		id := mustLock(t, svc1)
		firstReq := boxingReq(id, "box-first", "reviewer-a")
		if _, err := svc1.ConfirmBoxing(ctx, firstReq); err != nil {
			t.Fatalf("first boxing: %v", err)
		}
		if _, err := svc1.ConfirmBoxing(ctx, boxingReq(id, "box-second", "reviewer-b")); err != nil {
			t.Fatalf("second boxing: %v", err)
		}
		st2, err := store.OpenSQLite(ctx, dir)
		if err != nil {
			t.Fatalf("reopen sqlite: %v", err)
		}
		return newTestService(t, st2), id, firstReq
	}
	assertCode := func(t *testing.T, err error, code string) {
		t.Helper()
		var ce *service.CodedError
		if !errors.As(err, &ce) || ce.Code != code {
			t.Fatalf("error code = %v, want %s", err, code)
		}
	}
	assertOperations := func(t *testing.T, svc *service.Service, taskID string, want int) {
		t.Helper()
		audit, err := svc.Audit(ctx, taskID)
		if err != nil {
			t.Fatalf("audit: %v", err)
		}
		if len(audit.Operations) != want {
			t.Fatalf("operation count = %d, want %d", len(audit.Operations), want)
		}
	}

	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "first_confirmation_waits_for_second_boxer",
			run: func(t *testing.T) {
				svc, id := setupLocked(t)
				res, err := svc.ConfirmBoxing(ctx, boxingReq(id, "box-first", "reviewer-a"))
				if err != nil {
					t.Fatalf("first boxing: %v", err)
				}
				if res.Task.State != task.StatePendingBoxing {
					t.Fatalf("state = %q, want %q", res.Task.State, task.StatePendingBoxing)
				}
				if res.Confirmations != 1 || !reflect.DeepEqual(res.BoxerIDs, []string{"reviewer-a"}) {
					t.Fatalf("confirmation result = (%d, %v), want one reviewer-a", res.Confirmations, res.BoxerIDs)
				}
			},
		},
		{
			name: "second_distinct_confirmation_advances_to_equipment_occupied",
			run: func(t *testing.T) {
				svc, id := setupLocked(t)
				if _, err := svc.ConfirmBoxing(ctx, boxingReq(id, "box-first", "reviewer-a")); err != nil {
					t.Fatalf("first boxing: %v", err)
				}
				res, err := svc.ConfirmBoxing(ctx, boxingReq(id, "box-second", "reviewer-b"))
				if err != nil {
					t.Fatalf("second boxing: %v", err)
				}
				if res.Task.State != task.StateEquipmentOccupied {
					t.Fatalf("state = %q, want %q", res.Task.State, task.StateEquipmentOccupied)
				}
				if res.Confirmations != 2 || !reflect.DeepEqual(res.BoxerIDs, []string{"reviewer-a", "reviewer-b"}) {
					t.Fatalf("confirmation result = (%d, %v), want reviewers a and b", res.Confirmations, res.BoxerIDs)
				}
			},
		},
		{
			name: "same_content_retry_after_advance_and_restart_stays_successful",
			run: func(t *testing.T) {
				svc, id, firstReq := setupRestartedAfterTwoBoxers(t)
				res, err := svc.ConfirmBoxing(ctx, firstReq)
				if err != nil {
					var ce *service.CodedError
					if errors.As(err, &ce) && ce.Code == service.CodeInvalidState {
						t.Fatalf("retry returned invalid_state instead of the saved successful confirmation: %v", err)
					}
					t.Fatalf("retry first boxing: %v", err)
				}
				if res.Task.TaskID != id || res.Task.State != task.StateEquipmentOccupied {
					t.Fatalf("retry task = (%q, %q), want (%q, %q)", res.Task.TaskID, res.Task.State, id, task.StateEquipmentOccupied)
				}
				if res.Confirmations != 2 || !reflect.DeepEqual(res.BoxerIDs, []string{"reviewer-a", "reviewer-b"}) {
					t.Fatalf("retry result = (%d, %v), want existing two confirmations", res.Confirmations, res.BoxerIDs)
				}
				assertOperations(t, svc, id, 2)
			},
		},
		{
			name: "changed_content_reusing_key_after_advance_reports_content_conflict",
			run: func(t *testing.T) {
				svc, id, firstReq := setupRestartedAfterTwoBoxers(t)
				firstReq.BoxedWeightGrams = 120001
				_, err := svc.ConfirmBoxing(ctx, firstReq)
				assertCode(t, err, service.CodeConflict)
				assertOperations(t, svc, id, 2)
			},
		},
		{
			name: "new_operation_without_history_after_advance_reports_invalid_state",
			run: func(t *testing.T) {
				svc, id, _ := setupRestartedAfterTwoBoxers(t)
				_, err := svc.ConfirmBoxing(ctx, boxingReq(id, "box-third", "reviewer-c"))
				assertCode(t, err, service.CodeInvalidState)
				assertOperations(t, svc, id, 2)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}
