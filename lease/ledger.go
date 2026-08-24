package lease

// NewLeaseID builds the deterministic lease identifier for a resource bound to
// a task. Identifiers are stable and sortable, which keeps audit output
// reproducible across restarts.
func NewLeaseID(taskID string, rt ResourceType, resourceID string) string {
	return taskID + ":" + string(rt) + ":" + resourceID
}

// LeaseRequest is the frozen set of resources a lock or equipment-start step
// must occupy in a single transaction.
type LeaseRequest struct {
	TaskID         string
	Generation     int64
	BinIDs         []string
	ProbeIDs       []string
	PlateWellIDs   []string
	DryingWindowID string
	Tick           int64
}

// BuildLeases expands a LeaseRequest into the ordered set of lease records to
// persist. Every resource kind gets a lease bound to the supplied generation.
func BuildLeases(req LeaseRequest) []LeaseRecord {
	var out []LeaseRecord
	for _, id := range req.BinIDs {
		out = append(out, LeaseRecord{
			LeaseID:        NewLeaseID(req.TaskID, ResourceBin, id),
			TaskID:         req.TaskID,
			Generation:     req.Generation,
			ResourceType:   ResourceBin,
			ResourceID:     id,
			Status:         LeaseAcquired,
			AcquiredAtTick: req.Tick,
		})
	}
	for _, id := range req.ProbeIDs {
		out = append(out, LeaseRecord{
			LeaseID:        NewLeaseID(req.TaskID, ResourceProbe, id),
			TaskID:         req.TaskID,
			Generation:     req.Generation,
			ResourceType:   ResourceProbe,
			ResourceID:     id,
			Status:         LeaseAcquired,
			AcquiredAtTick: req.Tick,
		})
	}
	for _, id := range req.PlateWellIDs {
		out = append(out, LeaseRecord{
			LeaseID:        NewLeaseID(req.TaskID, ResourcePlateWell, id),
			TaskID:         req.TaskID,
			Generation:     req.Generation,
			ResourceType:   ResourcePlateWell,
			ResourceID:     id,
			Status:         LeaseAcquired,
			AcquiredAtTick: req.Tick,
		})
	}
	if req.DryingWindowID != "" {
		out = append(out, LeaseRecord{
			LeaseID:        NewLeaseID(req.TaskID, ResourceDryingWindow, req.DryingWindowID),
			TaskID:         req.TaskID,
			Generation:     req.Generation,
			ResourceType:   ResourceDryingWindow,
			ResourceID:     req.DryingWindowID,
			Status:         LeaseAcquired,
			AcquiredAtTick: req.Tick,
		})
	}
	return out
}
