package adapter

import (
	"testing"

	"cacaoferment/evidence"
)

func TestScriptAdapterFaultSequence(t *testing.T) {
	a := NewScriptAdapter(evidence.AdapterProbe, []Step{
		{Fault: FaultReject, StableErrorCode: "probe_rejected", RetryAfterTicks: 5},
		{Fault: FaultDisconnect, StableErrorCode: "probe_disconnected", RetryAfterTicks: 3},
		{Fault: FaultTimeout, StableErrorCode: "probe_timeout", RetryAfterTicks: 7},
		{Fault: FaultMalformed, StableErrorCode: "probe_malformed", RetryAfterTicks: 2},
	})
	want := []struct {
		status evidence.AdapterStatus
		code   string
		retry  int64
	}{
		{evidence.AdapterRejected, "probe_rejected", 5},
		{evidence.AdapterDisconnected, "probe_disconnected", 3},
		{evidence.AdapterTimeout, "probe_timeout", 7},
		{evidence.AdapterMalformed, "probe_malformed", 2},
		{evidence.AdapterSucceeded, "", 0},
	}
	for i, w := range want {
		r := a.Call("probe-1")
		if r.Status != w.status {
			t.Fatalf("step %d: status = %q, want %q", i, r.Status, w.status)
		}
		if r.StableErrorCode != w.code {
			t.Fatalf("step %d: code = %q, want %q", i, r.StableErrorCode, w.code)
		}
		if r.RetryAfterTicks != w.retry {
			t.Fatalf("step %d: retry = %d, want %d", i, r.RetryAfterTicks, w.retry)
		}
		if r.StepIndex != i {
			t.Fatalf("step %d: index = %d", i, r.StepIndex)
		}
	}
}

func TestScriptAdapterRemaining(t *testing.T) {
	a := NewScriptAdapter(evidence.AdapterToxinReader, []Step{
		{Fault: FaultTimeout}, {Fault: FaultReject},
	})
	if a.Remaining() != 2 {
		t.Fatalf("remaining = %d, want 2", a.Remaining())
	}
	a.Call("well-1")
	a.Call("well-1")
	if a.Remaining() != 0 {
		t.Fatalf("remaining = %d, want 0", a.Remaining())
	}
}

func TestScriptAdapterDeterministicDigest(t *testing.T) {
	a := NewScriptAdapter(evidence.AdapterMoistureMeter, []Step{{Fault: FaultReject, StableErrorCode: "mm_reject"}})
	r1 := a.Call("meter-1")
	b := NewScriptAdapter(evidence.AdapterMoistureMeter, []Step{{Fault: FaultReject, StableErrorCode: "mm_reject"}})
	r2 := b.Call("meter-1")
	if r1.RawDigest != r2.RawDigest {
		t.Fatalf("digests differ: %q vs %q", r1.RawDigest, r2.RawDigest)
	}
}
