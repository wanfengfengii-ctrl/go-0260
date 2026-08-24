// Package arbiter implements the mould/toxin re-judgement and terminal
// arbitration rules for the joint inspection. It is pure domain logic: cut-test
// evaluation, toxin and chemistry bounds, re-judgement classification, review
// qualification, and terminal-decision mapping. The single-write barrier that
// produces the final credential is enforced by the store's unique credential
// index inside the service transaction.
package arbiter

import (
	"errors"

	"cacaoferment/task"
)

// TerminalDecision is the competing terminal outcome written at finalize.
type TerminalDecision string

const (
	DecisionReadyToDry   TerminalDecision = "ready_to_dry"
	DecisionRiskIsolated TerminalDecision = "risk_isolated"
	DecisionCancelled    TerminalDecision = "cancelled"
)

// Valid reports whether the decision is one of the three terminal outcomes.
func (d TerminalDecision) Valid() bool {
	switch d {
	case DecisionReadyToDry, DecisionRiskIsolated, DecisionCancelled:
		return true
	default:
		return false
	}
}

// State maps a terminal decision to its task state.
func (d TerminalDecision) State() task.State {
	switch d {
	case DecisionReadyToDry:
		return task.StateReadyToDry
	case DecisionRiskIsolated:
		return task.StateRiskIsolated
	case DecisionCancelled:
		return task.StateCancelled
	default:
		return task.StateCancelled
	}
}

// ErrInvalidDecision reports an unknown terminal decision.
var ErrInvalidDecision = errors.New("arbiter: invalid terminal decision")
