// Package task implements the fermentation drying-transfer task aggregate:
// the generation, state machine, idempotent operation record, and the
// terminal single-write barrier replayed on restart.
package task

// State is a task state in the documented state machine.
type State string

const (
	StatePendingLock       State = "pending_lock"
	StatePendingBoxing     State = "pending_boxing"
	StateEquipmentOccupied State = "equipment_occupied"
	StateTurnCollection    State = "turn_collection"
	StateCutScoring        State = "cut_scoring"
	StateToxinVerification State = "toxin_verification"
	StateChemistryRetest   State = "chemistry_retest"
	StatePendingReview     State = "pending_review"
	StateReadyToDry        State = "ready_to_dry"
	StateDried             State = "dried"
	StateRiskIsolated      State = "risk_isolated"
	StateCancelled         State = "cancelled"
)

// orderedStates defines the legal forward transitions. Terminal states are not
// listed because they accept no ordinary transition.
var orderedStates = []State{
	StatePendingLock,
	StatePendingBoxing,
	StateEquipmentOccupied,
	StateTurnCollection,
	StateCutScoring,
	StateToxinVerification,
	StateChemistryRetest,
	StatePendingReview,
	StateReadyToDry,
	StateDried,
	StateRiskIsolated,
	StateCancelled,
}

// Valid reports whether the state is one of the documented states.
func (s State) Valid() bool {
	for _, v := range orderedStates {
		if v == s {
			return true
		}
	}
	return false
}

// IsTerminal reports whether the state rejects ordinary writes. The four
// conclusion states (可转晒、已转晒、风险隔离、已取消) are terminal.
func (s State) IsTerminal() bool {
	switch s {
	case StateReadyToDry, StateDried, StateRiskIsolated, StateCancelled:
		return true
	default:
		return false
	}
}

// CanTransitionTo reports whether the state machine allows s -> next.
// Only ready_to_dry may advance to dried among the terminal states.
func (s State) CanTransitionTo(next State) bool {
	if !s.Valid() || !next.Valid() {
		return false
	}
	if s == StateReadyToDry {
		return next == StateDried
	}
	if s.IsTerminal() {
		return false
	}
	for i, v := range orderedStates {
		if v == s {
			return i+1 < len(orderedStates) && orderedStates[i+1] == next
		}
	}
	return false
}

// FermentTask is the aggregate root for one drying-transfer joint inspection.
type FermentTask struct {
	TaskID            string
	Generation        int64
	State             State
	PlotID            string
	VarietyBatchID    string
	BinIDs            []string
	BoxedWeightGrams  int64
	DryingWindowID    string
	LockedRuleVersion string
	CreatedAtTick     int64
	TerminalResult    string
}

// MatchesGeneration reports whether the task belongs to the given generation.
func (t FermentTask) MatchesGeneration(generation int64) bool {
	return t.Generation == generation
}

// IsTerminal reports whether the task has reached a terminal state.
func (t FermentTask) IsTerminal() bool {
	return t.State.IsTerminal()
}

// OperationRecord captures an idempotent operation for retry and conflict
// detection. Equal operation keys with equal request hashes return the stored
// response; equal keys with different request hashes are a content conflict.
//
// ActorID is an internal extension recording who performed the operation (for
// boxing confirmations and reviews), and ResponseBody preserves the exact
// serialized response so a retry can replay it byte-for-byte.
type OperationRecord struct {
	TaskID        string
	OperationKey  string
	OperationKind string
	ActorID       string
	RequestHash   string
	ResponseHash  string
	ResponseBody  []byte
	StatusCode    int
	CreatedAtTick int64
}
