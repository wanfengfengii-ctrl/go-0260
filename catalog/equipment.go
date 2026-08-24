package catalog

import (
	"fmt"
	"sort"
)

// EquipmentDirectory is the cooperative's registry of allowed fermentation
// bins, temperature probes, cut-test plate wells, and drying windows. The lock
// flow validates every requested resource against this directory so that an
// unknown or unregistered device cannot be frozen into a task.
type EquipmentDirectory struct {
	bins    map[string]struct{}
	probes  map[string]struct{}
	wells   map[string]struct{}
	windows map[string]struct{}
}

// NewEquipmentDirectory returns an empty equipment registry.
func NewEquipmentDirectory() *EquipmentDirectory {
	return &EquipmentDirectory{
		bins:    make(map[string]struct{}),
		probes:  make(map[string]struct{}),
		wells:   make(map[string]struct{}),
		windows: make(map[string]struct{}),
	}
}

// AddBin registers an allowed fermentation bin.
func (d *EquipmentDirectory) AddBin(id string) { d.bins[id] = struct{}{} }

// AddProbe registers an allowed temperature probe.
func (d *EquipmentDirectory) AddProbe(id string) { d.probes[id] = struct{}{} }

// AddWell registers an allowed cut-test plate well.
func (d *EquipmentDirectory) AddWell(id string) { d.wells[id] = struct{}{} }

// AddWindow registers an allowed drying window.
func (d *EquipmentDirectory) AddWindow(id string) { d.windows[id] = struct{}{} }

// HasBin reports whether the bin is registered.
func (d *EquipmentDirectory) HasBin(id string) bool { _, ok := d.bins[id]; return ok }

// HasProbe reports whether the probe is registered.
func (d *EquipmentDirectory) HasProbe(id string) bool { _, ok := d.probes[id]; return ok }

// HasWell reports whether the plate well is registered.
func (d *EquipmentDirectory) HasWell(id string) bool { _, ok := d.wells[id]; return ok }

// HasWindow reports whether the drying window is registered.
func (d *EquipmentDirectory) HasWindow(id string) bool { _, ok := d.windows[id]; return ok }

// Validate checks that every requested resource is registered. It returns a
// deterministically sorted list of unknown resource identifiers, or nil when
// everything is allowed.
func (d *EquipmentDirectory) Validate(bins, probes, wells []string, window string) error {
	var unknown []string
	for _, id := range bins {
		if !d.HasBin(id) {
			unknown = append(unknown, "bin:"+id)
		}
	}
	for _, id := range probes {
		if !d.HasProbe(id) {
			unknown = append(unknown, "probe:"+id)
		}
	}
	for _, id := range wells {
		if !d.HasWell(id) {
			unknown = append(unknown, "well:"+id)
		}
	}
	if window != "" && !d.HasWindow(window) {
		unknown = append(unknown, "window:"+window)
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("catalog: unknown equipment: %v", unknown)
}
