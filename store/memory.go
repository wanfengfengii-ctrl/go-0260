package store

import (
	"context"
	"sort"

	"cacaoferment/evidence"
	"cacaoferment/lease"
	"cacaoferment/task"
)

// memCore holds the canonical maps for one snapshot of the in-memory store.
// It implements Store without locking; Memory wraps it with a mutex and
// copy-on-write transaction semantics.
type memCore struct {
	tasks      map[string]task.FermentTask
	leases     map[string]lease.LeaseRecord
	operations map[string]task.OperationRecord
	evidence   map[string]evidence.EvidenceVersion
	cells      map[string]evidence.TurnCoverageCell
	blinds     map[string]evidence.BlindSample
	attempts   map[string]evidence.AdapterAttempt
	reviews    map[string]evidence.ReviewRecord
	creds      map[string]evidence.FinalCredential
	// occupied tracks the active lease for each resource key.
	occupied map[string]string
	// rejudge enforces one re-judgement per task/generation/kind/subject.
	rejudge map[string]struct{}
}

func newMemCore() *memCore {
	return &memCore{
		tasks:      make(map[string]task.FermentTask),
		leases:     make(map[string]lease.LeaseRecord),
		operations: make(map[string]task.OperationRecord),
		evidence:   make(map[string]evidence.EvidenceVersion),
		cells:      make(map[string]evidence.TurnCoverageCell),
		blinds:     make(map[string]evidence.BlindSample),
		attempts:   make(map[string]evidence.AdapterAttempt),
		reviews:    make(map[string]evidence.ReviewRecord),
		creds:      make(map[string]evidence.FinalCredential),
		occupied:   make(map[string]string),
		rejudge:    make(map[string]struct{}),
	}
}

// clone performs a shallow copy of all maps plus a deep copy of the occupancy
// map, yielding an independent snapshot for transactional writes.
func (c *memCore) clone() *memCore {
	n := newMemCore()
	for k, v := range c.tasks {
		n.tasks[k] = v
	}
	for k, v := range c.leases {
		n.leases[k] = v
	}
	for k, v := range c.operations {
		n.operations[k] = v
	}
	for k, v := range c.evidence {
		n.evidence[k] = v
	}
	for k, v := range c.cells {
		n.cells[k] = v
	}
	for k, v := range c.blinds {
		n.blinds[k] = v
	}
	for k, v := range c.attempts {
		n.attempts[k] = v
	}
	for k, v := range c.reviews {
		n.reviews[k] = v
	}
	for k, v := range c.creds {
		n.creds[k] = v
	}
	for k, v := range c.occupied {
		n.occupied[k] = v
	}
	for k := range c.rejudge {
		n.rejudge[k] = struct{}{}
	}
	return n
}

func opKey(taskID, key string) string                     { return taskID + "\x00" + key }
func cellKey(taskID string, node int) string              { return taskID + "\x00" + itoa(node) }
func blindKey(taskID, code string) string                 { return taskID + "\x00" + code }
func resourceKey(rt lease.ResourceType, id string) string { return string(rt) + "\x00" + id }

// InTx on a memCore snapshot simply runs fn against the snapshot itself; the
// Memory wrapper performs the copy-on-write commit. It exists so memCore
// satisfies Store when handed back as the transaction-scoped receiver.
func (c *memCore) InTx(_ context.Context, fn func(tx Store) error) error { return fn(c) }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// --- task ---

func (c *memCore) SaveTask(_ context.Context, t task.FermentTask) error {
	c.tasks[t.TaskID] = t
	return nil
}

func (c *memCore) GetTask(_ context.Context, taskID string) (task.FermentTask, error) {
	t, ok := c.tasks[taskID]
	if !ok {
		return task.FermentTask{}, ErrNotFound
	}
	return t, nil
}

func (c *memCore) ListTasks(_ context.Context) ([]task.FermentTask, error) {
	out := make([]task.FermentTask, 0, len(c.tasks))
	for _, t := range c.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out, nil
}

// --- lease ---

func (c *memCore) SaveLease(_ context.Context, l lease.LeaseRecord) error {
	if l.Active() {
		if existing, ok := c.occupied[resourceKey(l.ResourceType, l.ResourceID)]; ok && existing != l.LeaseID {
			return ErrResourceOccupied
		}
		c.occupied[resourceKey(l.ResourceType, l.ResourceID)] = l.LeaseID
	} else {
		// Releasing a lease frees the occupancy slot only if it still points here.
		if existing, ok := c.occupied[resourceKey(l.ResourceType, l.ResourceID)]; ok && existing == l.LeaseID {
			delete(c.occupied, resourceKey(l.ResourceType, l.ResourceID))
		}
	}
	c.leases[l.LeaseID] = l
	return nil
}

func (c *memCore) ListLeases(_ context.Context, taskID string) ([]lease.LeaseRecord, error) {
	var out []lease.LeaseRecord
	for _, l := range c.leases {
		if l.TaskID == taskID {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ResourceType != out[j].ResourceType {
			return out[i].ResourceType < out[j].ResourceType
		}
		return out[i].ResourceID < out[j].ResourceID
	})
	return out, nil
}

// --- operation ---

func (c *memCore) SaveOperation(_ context.Context, o task.OperationRecord) error {
	k := opKey(o.TaskID, o.OperationKey)
	if _, exists := c.operations[k]; exists {
		return ErrDuplicate
	}
	c.operations[k] = o
	return nil
}

func (c *memCore) GetOperation(_ context.Context, taskID, key string) (task.OperationRecord, bool, error) {
	o, ok := c.operations[opKey(taskID, key)]
	return o, ok, nil
}

func (c *memCore) ListOperations(_ context.Context, taskID string) ([]task.OperationRecord, error) {
	var out []task.OperationRecord
	for _, o := range c.operations {
		if o.TaskID == taskID {
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OperationKey < out[j].OperationKey })
	return out, nil
}

// --- evidence ---

func (c *memCore) SaveEvidence(_ context.Context, e evidence.EvidenceVersion) error {
	if _, exists := c.evidence[e.EvidenceID]; exists {
		return ErrDuplicate
	}
	if e.EvidenceKind == evidence.EvidenceRejudgement {
		k := e.TaskID + "\x00" + itoa64(e.Generation) + "\x00" + string(e.EvidenceKind) + "\x00" + e.SubjectKey
		if _, exists := c.rejudge[k]; exists {
			return ErrDuplicate
		}
		c.rejudge[k] = struct{}{}
	}
	c.evidence[e.EvidenceID] = e
	return nil
}

func (c *memCore) ListEvidence(_ context.Context, taskID string) ([]evidence.EvidenceVersion, error) {
	var out []evidence.EvidenceVersion
	for _, e := range c.evidence {
		if e.TaskID == taskID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EvidenceKind != out[j].EvidenceKind {
			return out[i].EvidenceKind < out[j].EvidenceKind
		}
		if out[i].VersionNo != out[j].VersionNo {
			return out[i].VersionNo < out[j].VersionNo
		}
		return out[i].EvidenceID < out[j].EvidenceID
	})
	return out, nil
}

// --- coverage cells ---

func (c *memCore) SaveCoverageCell(_ context.Context, cell evidence.TurnCoverageCell) error {
	k := cellKey(cell.TaskID, cell.TurnNode)
	if _, exists := c.cells[k]; exists {
		return ErrDuplicate
	}
	c.cells[k] = cell
	return nil
}

func (c *memCore) ListCoverageCells(_ context.Context, taskID string) ([]evidence.TurnCoverageCell, error) {
	var out []evidence.TurnCoverageCell
	for _, cell := range c.cells {
		if cell.TaskID == taskID {
			out = append(out, cell)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TurnNode < out[j].TurnNode })
	return out, nil
}

// --- blind samples ---

func (c *memCore) SaveBlindSample(_ context.Context, b evidence.BlindSample) error {
	c.blinds[blindKey(b.TaskID, b.BlindCode)] = b
	return nil
}

func (c *memCore) ListBlindSamples(_ context.Context, taskID string) ([]evidence.BlindSample, error) {
	var out []evidence.BlindSample
	for _, b := range c.blinds {
		if b.TaskID == taskID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BlindCode < out[j].BlindCode })
	return out, nil
}

// --- adapter attempts ---

func (c *memCore) SaveAdapterAttempt(_ context.Context, a evidence.AdapterAttempt) error {
	c.attempts[a.AttemptID] = a
	return nil
}

func (c *memCore) ListAdapterAttempts(_ context.Context, taskID string) ([]evidence.AdapterAttempt, error) {
	var out []evidence.AdapterAttempt
	for _, a := range c.attempts {
		if a.TaskID == taskID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LogicalTick != out[j].LogicalTick {
			return out[i].LogicalTick < out[j].LogicalTick
		}
		return out[i].AttemptID < out[j].AttemptID
	})
	return out, nil
}

// --- reviews ---

func (c *memCore) SaveReview(_ context.Context, r evidence.ReviewRecord) error {
	k := r.TaskID + "\x00" + string(r.ReviewKind) + "\x00" + r.ReviewerID
	if _, exists := c.reviews[k]; exists {
		return ErrDuplicate
	}
	c.reviews[k] = r
	return nil
}

func (c *memCore) ListReviews(_ context.Context, taskID string) ([]evidence.ReviewRecord, error) {
	var out []evidence.ReviewRecord
	for _, r := range c.reviews {
		if r.TaskID == taskID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReviewerID != out[j].ReviewerID {
			return out[i].ReviewerID < out[j].ReviewerID
		}
		return out[i].CreatedAtTick < out[j].CreatedAtTick
	})
	return out, nil
}

// --- credentials ---

func (c *memCore) SaveCredential(_ context.Context, cr evidence.FinalCredential) error {
	if _, exists := c.creds[cr.TaskID]; exists {
		return ErrDuplicate
	}
	c.creds[cr.TaskID] = cr
	return nil
}

func (c *memCore) GetCredential(_ context.Context, taskID string) (evidence.FinalCredential, bool, error) {
	cr, ok := c.creds[taskID]
	return cr, ok, nil
}

// Memory is the in-memory Store with copy-on-write transactional semantics.
type Memory struct {
	mu   chan struct{}
	core *memCore
}

// NewMemory returns an in-memory Store with deterministic, sorted reads and
// atomic transactions suitable for concurrent tests.
func NewMemory() Store {
	return &Memory{mu: make(chan struct{}, 1), core: newMemCore()}
}

func (m *Memory) lock()   { m.mu <- struct{}{} }
func (m *Memory) unlock() { <-m.mu }

func (m *Memory) InTx(_ context.Context, fn func(tx Store) error) error {
	m.lock()
	defer m.unlock()
	clone := m.core.clone()
	if err := fn(clone); err != nil {
		return err
	}
	m.core = clone
	return nil
}

func (m *Memory) SaveTask(ctx context.Context, t task.FermentTask) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveTask(ctx, t)
}
func (m *Memory) GetTask(ctx context.Context, id string) (task.FermentTask, error) {
	m.lock()
	defer m.unlock()
	return m.core.GetTask(ctx, id)
}
func (m *Memory) ListTasks(ctx context.Context) ([]task.FermentTask, error) {
	m.lock()
	defer m.unlock()
	return m.core.ListTasks(ctx)
}
func (m *Memory) SaveLease(ctx context.Context, l lease.LeaseRecord) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveLease(ctx, l)
}
func (m *Memory) ListLeases(ctx context.Context, id string) ([]lease.LeaseRecord, error) {
	m.lock()
	defer m.unlock()
	return m.core.ListLeases(ctx, id)
}
func (m *Memory) SaveOperation(ctx context.Context, o task.OperationRecord) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveOperation(ctx, o)
}
func (m *Memory) GetOperation(ctx context.Context, id, key string) (task.OperationRecord, bool, error) {
	m.lock()
	defer m.unlock()
	return m.core.GetOperation(ctx, id, key)
}
func (m *Memory) ListOperations(ctx context.Context, id string) ([]task.OperationRecord, error) {
	m.lock()
	defer m.unlock()
	return m.core.ListOperations(ctx, id)
}
func (m *Memory) SaveEvidence(ctx context.Context, e evidence.EvidenceVersion) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveEvidence(ctx, e)
}
func (m *Memory) ListEvidence(ctx context.Context, id string) ([]evidence.EvidenceVersion, error) {
	m.lock()
	defer m.unlock()
	return m.core.ListEvidence(ctx, id)
}
func (m *Memory) SaveCoverageCell(ctx context.Context, c evidence.TurnCoverageCell) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveCoverageCell(ctx, c)
}
func (m *Memory) ListCoverageCells(ctx context.Context, id string) ([]evidence.TurnCoverageCell, error) {
	m.lock()
	defer m.unlock()
	return m.core.ListCoverageCells(ctx, id)
}
func (m *Memory) SaveBlindSample(ctx context.Context, b evidence.BlindSample) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveBlindSample(ctx, b)
}
func (m *Memory) ListBlindSamples(ctx context.Context, id string) ([]evidence.BlindSample, error) {
	m.lock()
	defer m.unlock()
	return m.core.ListBlindSamples(ctx, id)
}
func (m *Memory) SaveAdapterAttempt(ctx context.Context, a evidence.AdapterAttempt) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveAdapterAttempt(ctx, a)
}
func (m *Memory) ListAdapterAttempts(ctx context.Context, id string) ([]evidence.AdapterAttempt, error) {
	m.lock()
	defer m.unlock()
	return m.core.ListAdapterAttempts(ctx, id)
}
func (m *Memory) SaveReview(ctx context.Context, r evidence.ReviewRecord) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveReview(ctx, r)
}
func (m *Memory) ListReviews(ctx context.Context, id string) ([]evidence.ReviewRecord, error) {
	m.lock()
	defer m.unlock()
	return m.core.ListReviews(ctx, id)
}
func (m *Memory) SaveCredential(ctx context.Context, c evidence.FinalCredential) error {
	m.lock()
	defer m.unlock()
	return m.core.SaveCredential(ctx, c)
}
func (m *Memory) GetCredential(ctx context.Context, id string) (evidence.FinalCredential, bool, error) {
	m.lock()
	defer m.unlock()
	return m.core.GetCredential(ctx, id)
}
