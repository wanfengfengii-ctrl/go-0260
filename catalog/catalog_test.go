package catalog

import "testing"

func validSnapshot() RuleSnapshot {
	return RuleSnapshot{
		RuleVersion:    "v1",
		PlotID:         "plot-1",
		VarietyBatchID: "variety-1",
		TurnNodes:      []int{1, 2, 3},
		QualifiedReviewers: []Reviewer{
			{ID: "r-a", Qualified: true},
			{ID: "r-b", Qualified: false},
		},
		FixedScale: 2,
	}
}

func TestRuleSnapshotValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if err := validSnapshot().Validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("non-increasing turn nodes", func(t *testing.T) {
		s := validSnapshot()
		s.TurnNodes = []int{1, 1, 3}
		if err := s.Validate(); err == nil {
			t.Fatal("expected error for non-increasing turn nodes")
		}
	})
	t.Run("invalid fixed scale", func(t *testing.T) {
		s := validSnapshot()
		s.FixedScale = 19
		if err := s.Validate(); err == nil {
			t.Fatal("expected error for out-of-range fixed scale")
		}
	})
}

func TestMapCatalogMatchPlotVariety(t *testing.T) {
	c := NewMapCatalog()
	c.AddPlotVariety("plot-1", "variety-1")
	if !c.MatchPlotVariety("plot-1", "variety-1") {
		t.Fatal("expected match for registered pair")
	}
	if c.MatchPlotVariety("plot-1", "variety-2") {
		t.Fatal("unexpected match for unregistered variety")
	}
	if c.MatchPlotVariety("plot-9", "variety-1") {
		t.Fatal("unexpected match for unregistered plot")
	}
}

func TestMapCatalogSnapshotRoundTrip(t *testing.T) {
	c := NewMapCatalog()
	s := validSnapshot()
	if err := c.AddSnapshot(s); err != nil {
		t.Fatalf("AddSnapshot: %v", err)
	}
	got, ok := c.Snapshot("v1")
	if !ok {
		t.Fatal("expected snapshot to be present")
	}
	if got.RuleVersion != "v1" || got.FixedScale != 2 {
		t.Fatalf("unexpected snapshot: %+v", got)
	}
	if err := c.AddSnapshot(s); err == nil {
		t.Fatal("expected duplicate version error")
	}
}
