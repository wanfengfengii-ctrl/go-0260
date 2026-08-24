package main

import (
	"cacaoferment/catalog"
)

// seedCatalog registers the cooperative's fictional plots, variety batches,
// frozen threshold version, allowed equipment, and qualified reviewers before
// any task is locked. This is the directory-initialization flow.
func seedCatalog(c *catalog.MapCatalog, e *catalog.EquipmentDirectory) {
	snap := catalog.RuleSnapshot{
		RuleVersion:    "v1",
		PlotID:         "plot-1",
		VarietyBatchID: "variety-criollo-1",
		TurnNodes:      []int{1, 2, 3},
		TemperatureThresholds: catalog.TemperatureThresholds{
			AmbientCentiC:       3000,
			MinCentiC:           3800,
			MaxCentiC:           5600,
			MinSlopeMilliPerMin: 0,
			MaxSlopeMilliPerMin: 400,
			MinDurationMinutes:  30,
			MaxDurationMinutes:  240,
			MaxTurnCount:        4,
		},
		CutTestThresholds: catalog.CutTestThresholds{
			MaxUnderfermentedPercent: 500,
			MaxPurplePercent:         1000,
			MaxMoldyPercent:          200,
			MaxInsectPercent:         200,
		},
		ToxinThresholds: catalog.ToxinThresholds{MaxToxinPPB: 10},
		ChemistryThresholds: catalog.ChemistryThresholds{
			MinMoisturePercent:      5500,
			MaxMoisturePercent:      7500,
			MinPH:                   500,
			MaxPH:                   620,
			MaxFreeFattyAcidPercent: 150,
		},
		QualifiedReviewers: []catalog.Reviewer{
			{ID: "reviewer-a", Qualified: true},
			{ID: "reviewer-b", Qualified: true},
			{ID: "reviewer-c", Qualified: true},
		},
		FixedScale: 2,
	}
	if err := c.AddSnapshot(snap); err != nil {
		panic(err)
	}
	c.AddPlotVariety("plot-1", "variety-criollo-1")
	c.AddPlotVariety("plot-2", "variety-forastero-1")

	for _, id := range []string{"bin-1", "bin-2", "bin-3"} {
		e.AddBin(id)
	}
	for _, id := range []string{"probe-1", "probe-2"} {
		e.AddProbe(id)
	}
	for _, id := range []string{"well-1", "well-2", "well-3"} {
		e.AddWell(id)
	}
	for _, id := range []string{"window-1", "window-2"} {
		e.AddWindow(id)
	}
}
