// Package adapter models the external instruments (temperature probe, toxin
// plate reader, moisture meter) as deterministic, scriptable devices. Each
// device replays a fixed failure script; once the script is exhausted every
// further call succeeds. This lets the service and tests exercise reject,
// disconnect, timeout, and malformed outcomes without real hardware.
package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	"cacaoferment/evidence"
)

// Fault enumerates the scripted failure modes an instrument may emit.
type Fault string

const (
	FaultReject     Fault = "reject"
	FaultDisconnect Fault = "disconnect"
	FaultTimeout    Fault = "timeout"
	FaultMalformed  Fault = "malformed"
)

// Step is one scripted outcome. StableErrorCode is the deterministic code the
// service records on the attempt, and RetryAfterTicks is the logical delay
// before the next retry is permitted.
type Step struct {
	Fault           Fault
	StableErrorCode string
	RetryAfterTicks int64
}

// Result is the outcome of a single instrument call.
type Result struct {
	Status          evidence.AdapterStatus
	StableErrorCode string
	RetryAfterTicks int64
	RawDigest       string
	StepIndex       int
}

// ScriptAdapter replays Steps in order. When the script is exhausted it
// succeeds indefinitely, modelling a device that recovers after a fixed fault
// sequence.
type ScriptAdapter struct {
	kind  evidence.AdapterKind
	steps []Step
	next  int
}

// NewScriptAdapter returns an adapter that replays the given steps in order.
func NewScriptAdapter(kind evidence.AdapterKind, steps []Step) *ScriptAdapter {
	return &ScriptAdapter{kind: kind, steps: append([]Step(nil), steps...)}
}

// Kind returns the instrument kind this adapter stands in for.
func (a *ScriptAdapter) Kind() evidence.AdapterKind { return a.kind }

// Call advances the script by one step and returns the deterministic outcome.
func (a *ScriptAdapter) Call(target string) Result {
	idx := a.next
	if a.next < len(a.steps) {
		a.next++
	}
	r := Result{StepIndex: idx}
	if idx >= len(a.steps) {
		r.Status = evidence.AdapterSucceeded
	} else {
		s := a.steps[idx]
		switch s.Fault {
		case FaultReject:
			r.Status = evidence.AdapterRejected
		case FaultDisconnect:
			r.Status = evidence.AdapterDisconnected
		case FaultTimeout:
			r.Status = evidence.AdapterTimeout
		case FaultMalformed:
			r.Status = evidence.AdapterMalformed
		default:
			r.Status = evidence.AdapterSucceeded
		}
		r.StableErrorCode = s.StableErrorCode
		r.RetryAfterTicks = s.RetryAfterTicks
	}
	r.RawDigest = digest(target, strconv.Itoa(idx), string(r.Status), r.StableErrorCode)
	return r
}

// Remaining reports how many scripted steps have not yet been replayed. It is
// used by tests to assert the exact retry count.
func (a *ScriptAdapter) Remaining() int {
	if a.next >= len(a.steps) {
		return 0
	}
	return len(a.steps) - a.next
}

func digest(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		fmt.Fprintf(h, "%d:%s;", i, p)
	}
	return hex.EncodeToString(h.Sum(nil))
}
