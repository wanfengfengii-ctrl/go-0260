// Package catalog implements the cocoa variety and fermentation rule catalog.
// It maintains plots, variety batches, threshold versions, turn templates,
// allowed equipment, reviewer qualification, and rule-snapshot validation.
package catalog

import (
	"fmt"
)

// ThresholdVersion identifies an immutable rules snapshot.
type ThresholdVersion string

// Reviewer identifies a person and whether they are qualified to review.
type Reviewer struct {
	ID        string
	Qualified bool
}

// TemperatureThresholds holds fixed-integer bounds for temperature rise.
// Temperatures are scaled to centi-degrees Celsius (10^-2). AmbientCentiC is
// the reference temperature used to derive the slope of the first turn node.
type TemperatureThresholds struct {
	AmbientCentiC       int64
	MinCentiC           int64
	MaxCentiC           int64
	MinSlopeMilliPerMin int64
	MaxSlopeMilliPerMin int64
	MinDurationMinutes  int64
	MaxDurationMinutes  int64
	MaxTurnCount        int64
}

// CutTestThresholds holds cut-test percentage bounds, scaled by FixedScale.
type CutTestThresholds struct {
	MaxUnderfermentedPercent int64
	MaxPurplePercent         int64
	MaxMoldyPercent          int64
	MaxInsectPercent         int64
}

// ToxinThresholds holds toxin reading bounds in parts-per-billion.
type ToxinThresholds struct {
	MaxToxinPPB int64
}

// ChemistryThresholds holds chemistry bounds. Moisture and free fatty acid are
// scaled by FixedScale; pH is scaled by FixedScale as well.
type ChemistryThresholds struct {
	MinMoisturePercent      int64
	MaxMoisturePercent      int64
	MinPH                   int64
	MaxPH                   int64
	MaxFreeFattyAcidPercent int64
}

// RuleSnapshot is the frozen set of thresholds and reviewer qualification for
// one rule version. It is validated at lock time and never mutated afterwards.
type RuleSnapshot struct {
	RuleVersion           ThresholdVersion
	PlotID                string
	VarietyBatchID        string
	TurnNodes             []int
	TemperatureThresholds TemperatureThresholds
	CutTestThresholds     CutTestThresholds
	ToxinThresholds       ToxinThresholds
	ChemistryThresholds   ChemistryThresholds
	QualifiedReviewers    []Reviewer
	FixedScale            int
}

// Validate checks the structural invariants of the snapshot. It returns a
// stable error for non-increasing turn nodes and an invalid fixed scale.
func (s RuleSnapshot) Validate() error {
	if s.RuleVersion == "" {
		return fmt.Errorf("catalog: rule_version must not be empty")
	}
	if s.FixedScale < 0 || s.FixedScale > 18 {
		return fmt.Errorf("catalog: fixed_scale out of range: %d", s.FixedScale)
	}
	for i := 1; i < len(s.TurnNodes); i++ {
		if s.TurnNodes[i] <= s.TurnNodes[i-1] {
			return fmt.Errorf("catalog: turn nodes must be strictly increasing at index %d", i)
		}
	}
	return nil
}

// IsQualifiedReviewer reports whether the given reviewer id is present and
// qualified in this snapshot.
func (s RuleSnapshot) IsQualifiedReviewer(id string) bool {
	for _, r := range s.QualifiedReviewers {
		if r.ID == id {
			return r.Qualified
		}
	}
	return false
}

// RuleCatalog is the read interface used at lock time to resolve a frozen
// snapshot and to validate plot/variety matching.
type RuleCatalog interface {
	Snapshot(version ThresholdVersion) (RuleSnapshot, bool)
	MatchPlotVariety(plotID, varietyBatchID string) bool
}

// MapCatalog is an in-memory RuleCatalog. It is used to seed the cooperative's
// directory before tasks are locked.
type MapCatalog struct {
	snapshots     map[ThresholdVersion]RuleSnapshot
	plotVarieties map[string]map[string]struct{}
}

// NewMapCatalog returns an empty catalog.
func NewMapCatalog() *MapCatalog {
	return &MapCatalog{
		snapshots:     make(map[ThresholdVersion]RuleSnapshot),
		plotVarieties: make(map[string]map[string]struct{}),
	}
}

// AddSnapshot registers a rule snapshot. It returns an error if the snapshot
// is invalid or if the version is already registered.
func (c *MapCatalog) AddSnapshot(s RuleSnapshot) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if _, exists := c.snapshots[s.RuleVersion]; exists {
		return fmt.Errorf("catalog: rule version %q already registered", s.RuleVersion)
	}
	c.snapshots[s.RuleVersion] = s
	return nil
}

// AddPlotVariety registers an allowed variety batch for a collection plot.
func (c *MapCatalog) AddPlotVariety(plotID, varietyBatchID string) {
	if c.plotVarieties[plotID] == nil {
		c.plotVarieties[plotID] = make(map[string]struct{})
	}
	c.plotVarieties[plotID][varietyBatchID] = struct{}{}
}

// Snapshot returns the frozen snapshot for a version.
func (c *MapCatalog) Snapshot(version ThresholdVersion) (RuleSnapshot, bool) {
	s, ok := c.snapshots[version]
	return s, ok
}

// MatchPlotVariety reports whether the variety batch is allowed on the plot.
func (c *MapCatalog) MatchPlotVariety(plotID, varietyBatchID string) bool {
	allowed := c.plotVarieties[plotID]
	if allowed == nil {
		return false
	}
	_, ok := allowed[varietyBatchID]
	return ok
}
