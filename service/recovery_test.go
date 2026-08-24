package service_test

import (
	"context"
	"path/filepath"
	"testing"

	"cacaoferment/arbiter"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func TestRecoveryAfterLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	st1, err := store.OpenSQLite(context.Background(), dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	svc1 := newTestService(t, st1)
	id := mustLock(t, svc1)

	// Simulate restart by opening a fresh store against the same directory.
	st2, err := store.OpenSQLite(context.Background(), dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	svc2 := newTestService(t, st2)
	v, err := svc2.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if v.Task.State != task.StatePendingBoxing {
		t.Fatalf("state = %q, want pending_boxing", v.Task.State)
	}
	if len(v.Leases) != 5 {
		t.Fatalf("expected 5 leases recovered, got %d", len(v.Leases))
	}
}

func TestRecoveryAfterFinalize(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	st1, err := store.OpenSQLite(context.Background(), dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	svc1 := newTestService(t, st1)
	id := driveToPendingReview(t, svc1)
	review(t, svc1, id, "reviewer-c", evidence.DecisionApprove)
	review(t, svc1, id, "reviewer-d", evidence.DecisionApprove)
	if _, err := svc1.Finalize(context.Background(), service.FinalizeRequest{
		TaskID: id, OperationKey: "fin", Generation: 1, Decision: arbiter.DecisionReadyToDry,
	}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	before, err := svc1.Audit(context.Background(), id)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	// Reopen and verify the full audit trail and single credential.
	st2, err := store.OpenSQLite(context.Background(), dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	svc2 := newTestService(t, st2)
	after, err := svc2.Audit(context.Background(), id)
	if err != nil {
		t.Fatalf("audit after restart: %v", err)
	}
	if after.Task.State != task.StateReadyToDry {
		t.Fatalf("state = %q, want ready_to_dry", after.Task.State)
	}
	if len(after.Evidence) != len(before.Evidence) {
		t.Fatalf("evidence count changed: %d -> %d", len(before.Evidence), len(after.Evidence))
	}
	if len(after.Leases) != len(before.Leases) {
		t.Fatalf("lease count changed")
	}
	if after.Credential == nil {
		t.Fatal("credential missing after restart")
	}
	if after.Credential.Digest != before.Credential.Digest {
		t.Fatal("credential digest changed")
	}
}

func TestRecoveryIdempotencySurvivesRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	st1, _ := store.OpenSQLite(context.Background(), dir)
	svc1 := newTestService(t, st1)
	id := mustLock(t, svc1)
	box(t, svc1, id, "reviewer-a")

	st2, _ := store.OpenSQLite(context.Background(), dir)
	svc2 := newTestService(t, st2)
	// Replaying the same operation key with identical content must not create a
	// second confirmation or advance state.
	res, err := svc2.ConfirmBoxing(context.Background(), service.BoxingRequest{
		TaskID: id, OperationKey: "box-reviewer-a", Generation: 1, BoxerID: "reviewer-a", BoxedWeightGrams: 120000,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(res.BoxerIDs) != 1 {
		t.Fatalf("expected one boxer after replay, got %d", len(res.BoxerIDs))
	}
}

func TestRecoveryOpenTaskKeepsGeneration(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "db")
	st1, _ := store.OpenSQLite(context.Background(), dir)
	svc1 := newTestService(t, st1)
	id := mustLock(t, svc1)
	box(t, svc1, id, "reviewer-a")
	box(t, svc1, id, "reviewer-b")
	startEquipment(t, svc1, id)
	collect(t, svc1, id)

	st2, _ := store.OpenSQLite(context.Background(), dir)
	svc2 := newTestService(t, st2)
	v, err := svc2.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v.Task.Generation != 1 {
		t.Fatalf("generation = %d, want 1", v.Task.Generation)
	}
	if v.Task.State != task.StateCutScoring {
		t.Fatalf("state = %q, want cut_scoring", v.Task.State)
	}
}
