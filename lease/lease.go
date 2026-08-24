// Package lease implements the lease ledger for fermentation bins, temperature
// probes, cut-test plate wells, and drying windows. Each resource may hold at
// most one effective lease across open tasks.
package lease

// ResourceType enumerates the resource kinds subject to one-time occupancy.
type ResourceType string

const (
	ResourceBin          ResourceType = "bin"
	ResourceProbe        ResourceType = "probe"
	ResourcePlateWell    ResourceType = "plate_well"
	ResourceDryingWindow ResourceType = "drying_window"
)

// Valid reports whether the resource type is one of the documented kinds.
func (r ResourceType) Valid() bool {
	switch r {
	case ResourceBin, ResourceProbe, ResourcePlateWell, ResourceDryingWindow:
		return true
	default:
		return false
	}
}

// LeaseStatus is the lifecycle status of a lease.
type LeaseStatus string

const (
	LeaseAcquired LeaseStatus = "acquired"
	LeaseReleased LeaseStatus = "released"
)

// LeaseRecord is one occupancy entry bound to a task generation.
type LeaseRecord struct {
	LeaseID        string
	TaskID         string
	Generation     int64
	ResourceType   ResourceType
	ResourceID     string
	Status         LeaseStatus
	AcquiredAtTick int64
	ReleasedAtTick int64
	ReleaseReason  string
}

// Active reports whether the lease currently occupies its resource.
func (l LeaseRecord) Active() bool {
	return l.Status == LeaseAcquired
}
