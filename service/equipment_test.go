package service_test

import (
	"context"
	"testing"

	"cacaoferment/adapter"
	"cacaoferment/evidence"
	"cacaoferment/service"
	"cacaoferment/store"
	"cacaoferment/task"
)

func failingProbeService(t *testing.T) (*service.Service, *adapter.ScriptAdapter) {
	t.Helper()
	c, e := testSeed()
	reg := adapter.NewRegistry()
	probe := adapter.NewScriptAdapter(evidence.AdapterProbe, []adapter.Step{
		{Fault: adapter.FaultReject, StableErrorCode: "probe_rejected", RetryAfterTicks: 5},
		{Fault: adapter.FaultTimeout, StableErrorCode: "probe_timeout", RetryAfterTicks: 3},
	})
	reg.Register(probe)
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterToxinReader, nil))
	reg.Register(adapter.NewScriptAdapter(evidence.AdapterMoistureMeter, nil))
	return service.New(store.NewMemory(), c, e, reg), probe
}

func TestStartEquipmentAdapterRetries(t *testing.T) {
	svc, probe := failingProbeService(t)
	id := mustLock(t, svc)
	box(t, svc, id, "reviewer-a")
	box(t, svc, id, "reviewer-b")

	// First attempt: probe rejects, task stays in equipment_occupied.
	res1, err := svc.StartEquipment(context.Background(), service.StartEquipmentRequest{TaskID: id, OperationKey: "start-1", Generation: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res1.Started {
		t.Fatal("expected not started after probe rejection")
	}
	if probe.Remaining() != 1 {
		t.Fatalf("expected one script step remaining, got %d", probe.Remaining())
	}
	var rejectAttempt evidence.AdapterAttempt
	for _, a := range res1.Attempts {
		if a.AdapterKind == evidence.AdapterProbe {
			rejectAttempt = a
		}
	}
	if rejectAttempt.Status != evidence.AdapterRejected || rejectAttempt.StableErrorCode != "probe_rejected" {
		t.Fatalf("unexpected reject attempt: %+v", rejectAttempt)
	}
	if rejectAttempt.RetryAfterTick <= rejectAttempt.LogicalTick {
		t.Fatalf("retry after must follow logical tick")
	}

	// Second attempt: probe times out.
	res2, _ := svc.StartEquipment(context.Background(), service.StartEquipmentRequest{TaskID: id, OperationKey: "start-2", Generation: 1})
	if res2.Started {
		t.Fatal("expected not started after probe timeout")
	}

	// Third attempt: script exhausted, succeeds and advances.
	res3, err := svc.StartEquipment(context.Background(), service.StartEquipmentRequest{TaskID: id, OperationKey: "start-3", Generation: 1})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !res3.Started {
		t.Fatal("expected started after script exhausted")
	}
	v, _ := svc.Get(context.Background(), id)
	if v.Task.State != task.StateTurnCollection {
		t.Fatalf("expected turn_collection, got %q", v.Task.State)
	}
	// Logical ticks strictly increase across attempts.
	if rejectAttempt.LogicalTick >= res3.Attempts[len(res3.Attempts)-1].LogicalTick {
		t.Fatal("logical ticks did not increase")
	}
}
