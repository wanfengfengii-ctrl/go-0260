// Package store defines the persistence boundary for the joint inspection.
// A production implementation persists to a SQLite WAL directory; this package
// also ships an in-memory implementation used by tests and the dev server.
//
// Every logical write operation runs inside InTx, which hands the caller a
// transaction-scoped Store. Implementations must guarantee that a failed
// transaction leaves no partial leases, coverage cells, blind-sample reveals,
// operation records, or final credentials behind.
package store

import (
	"context"
	"errors"

	"cacaoferment/evidence"
	"cacaoferment/lease"
	"cacaoferment/task"
)

var (
	// ErrNotFound reports a missing record.
	ErrNotFound = errors.New("store: record not found")
	// ErrResourceOccupied reports a lease conflict on an already-occupied
	// resource. It is raised through the unique occupancy index.
	ErrResourceOccupied = errors.New("store: resource already occupied")
	// ErrDuplicate reports a violation of a uniqueness constraint other than
	// resource occupancy (operation keys, coverage nodes, credentials).
	ErrDuplicate = errors.New("store: duplicate record")
)

// Store is the persistence interface used by the service layer. Read methods
// are safe to call outside a transaction; write methods are intended to be
// invoked on the transaction-scoped Store received from InTx.
type Store interface {
	// Tasks.
	SaveTask(ctx context.Context, t task.FermentTask) error
	GetTask(ctx context.Context, taskID string) (task.FermentTask, error)
	ListTasks(ctx context.Context) ([]task.FermentTask, error)

	// Leases.
	SaveLease(ctx context.Context, l lease.LeaseRecord) error
	ListLeases(ctx context.Context, taskID string) ([]lease.LeaseRecord, error)

	// Idempotent operations.
	SaveOperation(ctx context.Context, o task.OperationRecord) error
	GetOperation(ctx context.Context, taskID, key string) (task.OperationRecord, bool, error)
	ListOperations(ctx context.Context, taskID string) ([]task.OperationRecord, error)

	// Evidence versions.
	SaveEvidence(ctx context.Context, e evidence.EvidenceVersion) error
	ListEvidence(ctx context.Context, taskID string) ([]evidence.EvidenceVersion, error)

	// Turn coverage cells.
	SaveCoverageCell(ctx context.Context, c evidence.TurnCoverageCell) error
	ListCoverageCells(ctx context.Context, taskID string) ([]evidence.TurnCoverageCell, error)

	// Blind samples.
	SaveBlindSample(ctx context.Context, b evidence.BlindSample) error
	ListBlindSamples(ctx context.Context, taskID string) ([]evidence.BlindSample, error)

	// Instrument adapter attempts.
	SaveAdapterAttempt(ctx context.Context, a evidence.AdapterAttempt) error
	ListAdapterAttempts(ctx context.Context, taskID string) ([]evidence.AdapterAttempt, error)

	// Independent reviews.
	SaveReview(ctx context.Context, r evidence.ReviewRecord) error
	ListReviews(ctx context.Context, taskID string) ([]evidence.ReviewRecord, error)

	// Terminal credentials.
	SaveCredential(ctx context.Context, c evidence.FinalCredential) error
	GetCredential(ctx context.Context, taskID string) (evidence.FinalCredential, bool, error)

	// InTx runs fn against a transaction-scoped Store. The transaction is
	// committed only if fn returns nil; any error aborts and rolls back.
	InTx(ctx context.Context, fn func(tx Store) error) error
}
