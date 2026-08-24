package task

import "testing"

func TestStateIsTerminal(t *testing.T) {
	terminal := []State{StateReadyToDry, StateDried, StateRiskIsolated, StateCancelled}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("state %q should be terminal", s)
		}
	}
	open := []State{StatePendingLock, StatePendingBoxing, StateTurnCollection}
	for _, s := range open {
		if s.IsTerminal() {
			t.Errorf("state %q should not be terminal", s)
		}
	}
}

func TestCanTransitionTo(t *testing.T) {
	cases := []struct {
		from, to State
		want     bool
	}{
		{StatePendingLock, StatePendingBoxing, true},
		{StatePendingBoxing, StateEquipmentOccupied, true},
		{StatePendingReview, StateReadyToDry, true},
		{StateReadyToDry, StateDried, true},
		{StatePendingLock, StateTurnCollection, false},
		{StateDried, StateRiskIsolated, false},
		{StateCancelled, StatePendingLock, false},
	}
	for _, c := range cases {
		if got := c.from.CanTransitionTo(c.to); got != c.want {
			t.Errorf("CanTransitionTo(%q, %q) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestFermentTaskGeneration(t *testing.T) {
	ft := FermentTask{Generation: 2, State: StateCancelled}
	if !ft.MatchesGeneration(2) {
		t.Fatal("expected generation match")
	}
	if ft.MatchesGeneration(1) {
		t.Fatal("unexpected generation match")
	}
	if !ft.IsTerminal() {
		t.Fatal("expected terminal task")
	}
}
