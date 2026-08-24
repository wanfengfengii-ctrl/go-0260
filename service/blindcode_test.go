package service_test

import (
	"context"
	"errors"
	"testing"

	"cacaoferment/service"
	"cacaoferment/store"
)

func toToxin(t *testing.T, svc *service.Service) string {
	t.Helper()
	id := mustLock(t, svc)
	box(t, svc, id, "reviewer-a")
	box(t, svc, id, "reviewer-b")
	startEquipment(t, svc, id)
	collect(t, svc, id)
	scoreBlind(t, svc, id, "BC-1")
	scoreBlind(t, svc, id, "BC-2")
	return id
}

func blindByCode(t *testing.T, svc *service.Service, id, code string) (revealed string, sealed bool) {
	t.Helper()
	audit, err := svc.Audit(context.Background(), id)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	for _, b := range audit.BlindSamples {
		if b.BlindCode == code {
			return b.RevealedBinID, b.Sealed
		}
	}
	t.Fatalf("blind %q not found", code)
	return "", false
}

func TestBlindRevealGate(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := toToxin(t, svc)

	// Gate not open: no bin mapping before toxin closure.
	if r, _ := blindByCode(t, svc, id, "BC-1"); r != "" {
		t.Fatalf("revealed before gate open: %q", r)
	}

	toxin(t, svc, id, "BC-1", 5)
	// Still not open: one sample lacks a toxin reading.
	if r, _ := blindByCode(t, svc, id, "BC-1"); r != "" {
		t.Fatalf("revealed before all toxin recorded: %q", r)
	}

	toxin(t, svc, id, "BC-2", 5)
	// Gate open: each sample revealed exactly once to its assigned bin.
	if r, sealed := blindByCode(t, svc, id, "BC-1"); r != "bin-1" || !sealed {
		t.Fatalf("BC-1 reveal = %q sealed=%v", r, sealed)
	}
	if r, sealed := blindByCode(t, svc, id, "BC-2"); r != "bin-2" || !sealed {
		t.Fatalf("BC-2 reveal = %q sealed=%v", r, sealed)
	}
}

func TestBlindOldGenerationLateReading(t *testing.T) {
	svc := newTestService(t, store.NewMemory())
	id := mustLock(t, svc)
	box(t, svc, id, "reviewer-a")
	box(t, svc, id, "reviewer-b")
	startEquipment(t, svc, id)
	collect(t, svc, id)
	scoreBlind(t, svc, id, "BC-1")

	// Re-judgement advances the generation.
	_, err := svc.CreateRejudgement(context.Background(), service.RejudgementRequest{
		TaskID: id, OperationKey: "rej-1", Generation: 1, Kind: "temperature_break", Subjects: []string{"bin-1"},
	})
	if err != nil {
		t.Fatalf("rejudgement: %v", err)
	}

	// A late reading with the stale generation is rejected.
	cut := arbiterCutScore()
	_, err = svc.SubmitBlindSample(context.Background(), service.BlindSampleRequest{
		TaskID: id, BlindCode: "BC-2", OperationKey: "cut-BC-2", Generation: 1, CutScore: &cut,
	})
	var ce *service.CodedError
	if !errors.As(err, &ce) || ce.Code != service.CodeGenerationMismatch {
		t.Fatalf("expected generation mismatch, got %v", err)
	}

	// The original evidence remains in the audit trail.
	audit, _ := svc.Audit(context.Background(), id)
	found := false
	for _, e := range audit.Evidence {
		if e.SubjectKey == "BC-1" {
			found = true
		}
	}
	if !found {
		t.Fatal("original cut-score evidence lost")
	}
}
