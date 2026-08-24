package service

import (
	"context"

	"cacaoferment/evidence"
	"cacaoferment/lease"
	"cacaoferment/task"
)

// TaskView is the aggregate read for GET /api/tasks/{id}.
type TaskView struct {
	Task   task.FermentTask    `json:"task"`
	Leases []lease.LeaseRecord `json:"leases"`
}

// AuditView is the full deterministic audit trail for one task.
type AuditView struct {
	Task            task.FermentTask            `json:"task"`
	Leases          []lease.LeaseRecord         `json:"leases"`
	Operations      []task.OperationRecord      `json:"operations"`
	Evidence        []evidence.EvidenceVersion  `json:"evidence"`
	CoverageCells   []evidence.TurnCoverageCell `json:"coverage_cells"`
	BlindSamples    []evidence.BlindSample      `json:"blind_samples"`
	AdapterAttempts []evidence.AdapterAttempt   `json:"adapter_attempts"`
	Reviews         []evidence.ReviewRecord     `json:"reviews"`
	Credential      *evidence.FinalCredential   `json:"credential,omitempty"`
}

// Get returns a task with its leases.
func (s *Service) Get(ctx context.Context, taskID string) (TaskView, error) {
	t, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return TaskView{}, err
	}
	leases, err := s.store.ListLeases(ctx, taskID)
	if err != nil {
		return TaskView{}, err
	}
	return TaskView{Task: t, Leases: leases}, nil
}

// List returns every task in stable order.
func (s *Service) List(ctx context.Context) ([]task.FermentTask, error) {
	return s.store.ListTasks(ctx)
}

// Audit assembles the complete deterministic audit trail for a task.
func (s *Service) Audit(ctx context.Context, taskID string) (AuditView, error) {
	t, err := s.store.GetTask(ctx, taskID)
	if err != nil {
		return AuditView{}, err
	}
	v := AuditView{Task: t}
	if v.Leases, err = s.store.ListLeases(ctx, taskID); err != nil {
		return AuditView{}, err
	}
	if v.Operations, err = s.store.ListOperations(ctx, taskID); err != nil {
		return AuditView{}, err
	}
	if v.Evidence, err = s.store.ListEvidence(ctx, taskID); err != nil {
		return AuditView{}, err
	}
	if v.CoverageCells, err = s.store.ListCoverageCells(ctx, taskID); err != nil {
		return AuditView{}, err
	}
	if v.BlindSamples, err = s.store.ListBlindSamples(ctx, taskID); err != nil {
		return AuditView{}, err
	}
	if v.AdapterAttempts, err = s.store.ListAdapterAttempts(ctx, taskID); err != nil {
		return AuditView{}, err
	}
	if v.Reviews, err = s.store.ListReviews(ctx, taskID); err != nil {
		return AuditView{}, err
	}
	if cred, ok, err := s.store.GetCredential(ctx, taskID); err != nil {
		return AuditView{}, err
	} else if ok {
		v.Credential = &cred
	}
	return v, nil
}
