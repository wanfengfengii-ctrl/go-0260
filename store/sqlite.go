package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cacaoferment/evidence"
	"cacaoferment/lease"
	"cacaoferment/task"

	_ "modernc.org/sqlite"
)

// sqlQuerier is the common surface shared by *sql.DB and *sql.Tx so that a
// single set of query methods can run against either a live connection or a
// transaction.
type sqlQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// sqliteBase holds the query methods shared by the top-level store and the
// transaction-scoped store.
type sqliteBase struct {
	q sqlQuerier
}

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS tasks (
	task_id             TEXT PRIMARY KEY,
	generation          INTEGER NOT NULL,
	state               TEXT NOT NULL,
	plot_id             TEXT NOT NULL,
	variety_batch_id    TEXT NOT NULL,
	bin_ids             TEXT NOT NULL,
	boxed_weight_grams  INTEGER NOT NULL,
	drying_window_id    TEXT NOT NULL,
	locked_rule_version TEXT NOT NULL,
	created_at_tick     INTEGER NOT NULL,
	terminal_result     TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS leases (
	lease_id        TEXT PRIMARY KEY,
	task_id         TEXT NOT NULL,
	generation      INTEGER NOT NULL,
	resource_type   TEXT NOT NULL,
	resource_id     TEXT NOT NULL,
	status          TEXT NOT NULL,
	acquired_at_tick INTEGER NOT NULL,
	released_at_tick INTEGER NOT NULL,
	release_reason  TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_leases_active
	ON leases(resource_type, resource_id) WHERE status = 'acquired';
CREATE TABLE IF NOT EXISTS operations (
	task_id         TEXT NOT NULL,
	operation_key   TEXT NOT NULL,
	operation_kind  TEXT NOT NULL,
	actor_id        TEXT NOT NULL DEFAULT '',
	request_hash    TEXT NOT NULL,
	response_hash   TEXT NOT NULL,
	response_body   TEXT NOT NULL DEFAULT '',
	status_code     INTEGER NOT NULL,
	created_at_tick INTEGER NOT NULL,
	PRIMARY KEY (task_id, operation_key)
);
CREATE TABLE IF NOT EXISTS evidence (
	evidence_id    TEXT PRIMARY KEY,
	task_id        TEXT NOT NULL,
	generation     INTEGER NOT NULL,
	evidence_kind  TEXT NOT NULL,
	subject_key    TEXT NOT NULL,
	version_no     INTEGER NOT NULL,
	payload_hash   TEXT NOT NULL,
	integer_values TEXT NOT NULL DEFAULT '[]',
	accepted       INTEGER NOT NULL,
	reject_code    TEXT NOT NULL DEFAULT '',
	created_at_tick INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_evidence_rejudge
	ON evidence(task_id, generation, evidence_kind, subject_key)
	WHERE evidence_kind = 'rejudgement';
CREATE TABLE IF NOT EXISTS coverage_cells (
	task_id             TEXT NOT NULL,
	generation          INTEGER NOT NULL,
	bin_id              TEXT NOT NULL,
	turn_node           INTEGER NOT NULL,
	temperature_centi_c INTEGER NOT NULL,
	duration_minutes    INTEGER NOT NULL,
	turn_count          INTEGER NOT NULL,
	gap_fill_flag       INTEGER NOT NULL,
	slope_milli_per_min INTEGER NOT NULL,
	valid               INTEGER NOT NULL,
	PRIMARY KEY (task_id, bin_id, turn_node)
);
CREATE TABLE IF NOT EXISTS blind_samples (
	task_id          TEXT NOT NULL,
	generation       INTEGER NOT NULL,
	blind_code       TEXT NOT NULL,
	sample_size      INTEGER NOT NULL,
	assigned_bin_id  TEXT NOT NULL DEFAULT '',
	revealed_bin_id  TEXT NOT NULL DEFAULT '',
	reveal_tick      INTEGER NOT NULL DEFAULT 0,
	reveal_generation INTEGER NOT NULL DEFAULT 0,
	sealed           INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (task_id, blind_code)
);
CREATE TABLE IF NOT EXISTS adapter_attempts (
	attempt_id        TEXT PRIMARY KEY,
	task_id           TEXT NOT NULL,
	generation        INTEGER NOT NULL,
	adapter_kind      TEXT NOT NULL,
	target_key        TEXT NOT NULL,
	script_step       INTEGER NOT NULL,
	logical_tick      INTEGER NOT NULL,
	status            TEXT NOT NULL,
	stable_error_code TEXT NOT NULL DEFAULT '',
	raw_digest        TEXT NOT NULL DEFAULT '',
	retry_after_tick  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS reviews (
	task_id         TEXT NOT NULL,
	generation      INTEGER NOT NULL,
	reviewer_id     TEXT NOT NULL,
	review_kind     TEXT NOT NULL,
	decision        TEXT NOT NULL,
	reason_code     TEXT NOT NULL DEFAULT '',
	created_at_tick INTEGER NOT NULL,
	PRIMARY KEY (task_id, review_kind, reviewer_id)
);
CREATE TABLE IF NOT EXISTS credentials (
	task_id              TEXT PRIMARY KEY,
	generation           INTEGER NOT NULL,
	credential_id        TEXT NOT NULL,
	terminal_state       TEXT NOT NULL,
	winner_operation_key TEXT NOT NULL,
	issued_at_tick       INTEGER NOT NULL,
	leased_window_id     TEXT NOT NULL DEFAULT '',
	digest               TEXT NOT NULL DEFAULT ''
);
`

// OpenSQLite opens (creating if necessary) a SQLite database at dir. The
// database file is created under dir as cacaoferment.db with WAL journaling,
// a busy timeout for concurrent writers, and foreign keys enabled.
func OpenSQLite(ctx context.Context, dir string) (Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	path := filepath.Join(dir, "cacaoferment.db")
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1) // single writer; WAL readers plus serialized writes
	if err := migrateCoverageCells(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate coverage cells: %w", err)
	}
	if _, err := db.ExecContext(ctx, sqliteSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &sqliteStore{sqliteBase: sqliteBase{q: db}, db: db}, nil
}

// migrateCoverageCells upgrades a legacy coverage_cells table whose primary key
// omitted bin_id (one coverage slot per turn node regardless of bin) to the
// per-bin schema. Coverage cells are keyed by (task_id, bin_id, turn_node) so a
// partial-bin submission cannot be mistaken for full coverage. A legacy table
// that predates the bin-scoped key is dropped; the CREATE TABLE step that
// follows rebuilds it with the current schema. Dropping is safe because legacy
// coverage data was ambiguous by bin and cannot be recovered into the new key.
func migrateCoverageCells(ctx context.Context, db *sql.DB) error {
	// Determine the primary-key columns of coverage_cells, if the table already
	// exists. group_concat is an aggregate: when the table is absent the join
	// yields no rows and the aggregate returns a single NULL, so scan into a
	// nullable string.
	var pkCols sql.NullString
	row := db.QueryRowContext(ctx, `SELECT group_concat(coalesce(c.name, ''), ',')
		FROM sqlite_master t
		JOIN pragma_index_list(t.name) il ON il.origin = 'pk'
		JOIN pragma_index_info(il.name) ii
		LEFT JOIN pragma_table_info(t.name) c ON c.cid = ii.cid
		WHERE t.type = 'table' AND t.name = 'coverage_cells'
		ORDER BY ii.seqno`)
	if err := row.Scan(&pkCols); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // table absent or no pk: CREATE TABLE builds the current schema
		}
		return err
	}
	if !pkCols.Valid {
		return nil // table absent: CREATE TABLE builds the current schema
	}
	// The current schema keys coverage cells by (task_id, bin_id, turn_node).
	// Any other key set is a legacy table that predates bin-scoped coverage and
	// must be rebuilt.
	switch pkCols.String {
	case "task_id,bin_id,turn_node", "bin_id,task_id,turn_node":
		return nil
	}
	_, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS coverage_cells`)
	return err
}

// sqliteStore is the top-level SQLite-backed Store.
type sqliteStore struct {
	sqliteBase
	db *sql.DB
}

// sqliteTx is the transaction-scoped Store handed to InTx callbacks.
type sqliteTx struct {
	sqliteBase
}

// InTx on a sqliteTx runs fn against the transaction directly. Nested savepoint
// support is intentionally omitted because the service never nests transactions.
func (s *sqliteTx) InTx(_ context.Context, fn func(tx Store) error) error { return fn(s) }

func (s *sqliteStore) InTx(ctx context.Context, fn func(tx Store) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	scoped := &sqliteTx{sqliteBase: sqliteBase{q: tx}}
	if err := fn(scoped); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// --- task ---

func (s *sqliteBase) SaveTask(ctx context.Context, t task.FermentTask) error {
	bins, err := json.Marshal(t.BinIDs)
	if err != nil {
		return err
	}
	_, err = s.q.ExecContext(ctx,
		`INSERT OR REPLACE INTO tasks
		 (task_id, generation, state, plot_id, variety_batch_id, bin_ids, boxed_weight_grams,
		  drying_window_id, locked_rule_version, created_at_tick, terminal_result)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		t.TaskID, t.Generation, string(t.State), t.PlotID, t.VarietyBatchID, string(bins),
		t.BoxedWeightGrams, t.DryingWindowID, t.LockedRuleVersion, t.CreatedAtTick, t.TerminalResult)
	return err
}

func (s *sqliteBase) GetTask(ctx context.Context, taskID string) (task.FermentTask, error) {
	var t task.FermentTask
	var state, bins string
	err := s.q.QueryRowContext(ctx,
		`SELECT task_id, generation, state, plot_id, variety_batch_id, bin_ids,
		        boxed_weight_grams, drying_window_id, locked_rule_version, created_at_tick, terminal_result
		 FROM tasks WHERE task_id = ?`, taskID).
		Scan(&t.TaskID, &t.Generation, &state, &t.PlotID, &t.VarietyBatchID, &bins,
			&t.BoxedWeightGrams, &t.DryingWindowID, &t.LockedRuleVersion, &t.CreatedAtTick, &t.TerminalResult)
	if errors.Is(err, sql.ErrNoRows) {
		return task.FermentTask{}, ErrNotFound
	}
	if err != nil {
		return task.FermentTask{}, err
	}
	t.State = task.State(state)
	if err := json.Unmarshal([]byte(bins), &t.BinIDs); err != nil {
		return task.FermentTask{}, err
	}
	return t, nil
}

func (s *sqliteBase) ListTasks(ctx context.Context) ([]task.FermentTask, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT task_id, generation, state, plot_id, variety_batch_id, bin_ids,
		        boxed_weight_grams, drying_window_id, locked_rule_version, created_at_tick, terminal_result
		 FROM tasks ORDER BY task_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []task.FermentTask
	for rows.Next() {
		var t task.FermentTask
		var state, bins string
		if err := rows.Scan(&t.TaskID, &t.Generation, &state, &t.PlotID, &t.VarietyBatchID, &bins,
			&t.BoxedWeightGrams, &t.DryingWindowID, &t.LockedRuleVersion, &t.CreatedAtTick, &t.TerminalResult); err != nil {
			return nil, err
		}
		t.State = task.State(state)
		_ = json.Unmarshal([]byte(bins), &t.BinIDs)
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- lease ---

func (s *sqliteBase) SaveLease(ctx context.Context, l lease.LeaseRecord) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO leases (lease_id, task_id, generation, resource_type, resource_id,
		                    status, acquired_at_tick, released_at_tick, release_reason)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		l.LeaseID, l.TaskID, l.Generation, string(l.ResourceType), l.ResourceID,
		string(l.Status), l.AcquiredAtTick, l.ReleasedAtTick, l.ReleaseReason)
	if isConstraint(err) {
		return ErrResourceOccupied
	}
	return err
}

func (s *sqliteBase) ListLeases(ctx context.Context, taskID string) ([]lease.LeaseRecord, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT lease_id, task_id, generation, resource_type, resource_id,
		        status, acquired_at_tick, released_at_tick, release_reason
		 FROM leases WHERE task_id = ? ORDER BY resource_type, resource_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lease.LeaseRecord
	for rows.Next() {
		var l lease.LeaseRecord
		var rt, st string
		if err := rows.Scan(&l.LeaseID, &l.TaskID, &l.Generation, &rt, &l.ResourceID,
			&st, &l.AcquiredAtTick, &l.ReleasedAtTick, &l.ReleaseReason); err != nil {
			return nil, err
		}
		l.ResourceType = lease.ResourceType(rt)
		l.Status = lease.LeaseStatus(st)
		out = append(out, l)
	}
	return out, rows.Err()
}

// --- operation ---

func (s *sqliteBase) SaveOperation(ctx context.Context, o task.OperationRecord) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO operations (task_id, operation_key, operation_kind, actor_id,
		                         request_hash, response_hash, response_body, status_code, created_at_tick)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		o.TaskID, o.OperationKey, o.OperationKind, o.ActorID,
		o.RequestHash, o.ResponseHash, string(o.ResponseBody), o.StatusCode, o.CreatedAtTick)
	if isConstraint(err) {
		return ErrDuplicate
	}
	return err
}

func (s *sqliteBase) GetOperation(ctx context.Context, taskID, key string) (task.OperationRecord, bool, error) {
	var o task.OperationRecord
	var body string
	err := s.q.QueryRowContext(ctx,
		`SELECT task_id, operation_key, operation_kind, actor_id, request_hash,
		        response_hash, response_body, status_code, created_at_tick
		 FROM operations WHERE task_id = ? AND operation_key = ?`, taskID, key).
		Scan(&o.TaskID, &o.OperationKey, &o.OperationKind, &o.ActorID, &o.RequestHash,
			&o.ResponseHash, &body, &o.StatusCode, &o.CreatedAtTick)
	if errors.Is(err, sql.ErrNoRows) {
		return task.OperationRecord{}, false, nil
	}
	if err != nil {
		return task.OperationRecord{}, false, err
	}
	o.ResponseBody = []byte(body)
	return o, true, nil
}

func (s *sqliteBase) ListOperations(ctx context.Context, taskID string) ([]task.OperationRecord, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT task_id, operation_key, operation_kind, actor_id, request_hash,
		        response_hash, response_body, status_code, created_at_tick
		 FROM operations WHERE task_id = ? ORDER BY operation_key`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []task.OperationRecord
	for rows.Next() {
		var o task.OperationRecord
		var body string
		if err := rows.Scan(&o.TaskID, &o.OperationKey, &o.OperationKind, &o.ActorID, &o.RequestHash,
			&o.ResponseHash, &body, &o.StatusCode, &o.CreatedAtTick); err != nil {
			return nil, err
		}
		o.ResponseBody = []byte(body)
		out = append(out, o)
	}
	return out, rows.Err()
}

// --- evidence ---

func (s *sqliteBase) SaveEvidence(ctx context.Context, e evidence.EvidenceVersion) error {
	vals, err := json.Marshal(e.IntegerValues)
	if err != nil {
		return err
	}
	_, err = s.q.ExecContext(ctx,
		`INSERT INTO evidence (evidence_id, task_id, generation, evidence_kind, subject_key,
		                       version_no, payload_hash, integer_values, accepted, reject_code, created_at_tick)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		e.EvidenceID, e.TaskID, e.Generation, string(e.EvidenceKind), e.SubjectKey,
		e.VersionNo, e.PayloadHash, string(vals), boolInt(e.Accepted), e.RejectCode, e.CreatedAtTick)
	if isConstraint(err) {
		return ErrDuplicate
	}
	return err
}

func (s *sqliteBase) ListEvidence(ctx context.Context, taskID string) ([]evidence.EvidenceVersion, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT evidence_id, task_id, generation, evidence_kind, subject_key, version_no,
		        payload_hash, integer_values, accepted, reject_code, created_at_tick
		 FROM evidence WHERE task_id = ? ORDER BY evidence_kind, version_no, evidence_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.EvidenceVersion
	for rows.Next() {
		var e evidence.EvidenceVersion
		var kind string
		var vals string
		var accepted int
		if err := rows.Scan(&e.EvidenceID, &e.TaskID, &e.Generation, &kind, &e.SubjectKey, &e.VersionNo,
			&e.PayloadHash, &vals, &accepted, &e.RejectCode, &e.CreatedAtTick); err != nil {
			return nil, err
		}
		e.EvidenceKind = evidence.EvidenceKind(kind)
		e.Accepted = accepted != 0
		_ = json.Unmarshal([]byte(vals), &e.IntegerValues)
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- coverage cells ---

func (s *sqliteBase) SaveCoverageCell(ctx context.Context, c evidence.TurnCoverageCell) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO coverage_cells (task_id, generation, bin_id, turn_node, temperature_centi_c,
		                             duration_minutes, turn_count, gap_fill_flag, slope_milli_per_min, valid)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.TaskID, c.Generation, c.BinID, c.TurnNode, c.TemperatureCentiC,
		c.DurationMinutes, c.TurnCount, boolInt(c.GapFillFlag), c.SlopeMilliPerMin, boolInt(c.Valid))
	if isConstraint(err) {
		return ErrDuplicate
	}
	return err
}

func (s *sqliteBase) ListCoverageCells(ctx context.Context, taskID string) ([]evidence.TurnCoverageCell, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT task_id, generation, bin_id, turn_node, temperature_centi_c,
		        duration_minutes, turn_count, gap_fill_flag, slope_milli_per_min, valid
		 FROM coverage_cells WHERE task_id = ? ORDER BY bin_id, turn_node`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.TurnCoverageCell
	for rows.Next() {
		var c evidence.TurnCoverageCell
		var gap, valid int
		if err := rows.Scan(&c.TaskID, &c.Generation, &c.BinID, &c.TurnNode, &c.TemperatureCentiC,
			&c.DurationMinutes, &c.TurnCount, &gap, &c.SlopeMilliPerMin, &valid); err != nil {
			return nil, err
		}
		c.GapFillFlag = gap != 0
		c.Valid = valid != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- blind samples ---

func (s *sqliteBase) SaveBlindSample(ctx context.Context, b evidence.BlindSample) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT OR REPLACE INTO blind_samples (task_id, generation, blind_code, sample_size, assigned_bin_id,
		                                       revealed_bin_id, reveal_tick, reveal_generation, sealed)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		b.TaskID, b.Generation, b.BlindCode, b.SampleSize, b.AssignedBinID,
		b.RevealedBinID, b.RevealTick, b.RevealGeneration, boolInt(b.Sealed))
	return err
}

func (s *sqliteBase) ListBlindSamples(ctx context.Context, taskID string) ([]evidence.BlindSample, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT task_id, generation, blind_code, sample_size, assigned_bin_id,
		        revealed_bin_id, reveal_tick, reveal_generation, sealed
		 FROM blind_samples WHERE task_id = ? ORDER BY blind_code`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.BlindSample
	for rows.Next() {
		var b evidence.BlindSample
		var sealed int
		if err := rows.Scan(&b.TaskID, &b.Generation, &b.BlindCode, &b.SampleSize, &b.AssignedBinID,
			&b.RevealedBinID, &b.RevealTick, &b.RevealGeneration, &sealed); err != nil {
			return nil, err
		}
		b.Sealed = sealed != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- adapter attempts ---

func (s *sqliteBase) SaveAdapterAttempt(ctx context.Context, a evidence.AdapterAttempt) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO adapter_attempts (attempt_id, task_id, generation, adapter_kind, target_key,
		                               script_step, logical_tick, status, stable_error_code, raw_digest, retry_after_tick)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		a.AttemptID, a.TaskID, a.Generation, string(a.AdapterKind), a.TargetKey,
		a.ScriptStep, a.LogicalTick, string(a.Status), a.StableErrorCode, a.RawDigest, a.RetryAfterTick)
	return err
}

func (s *sqliteBase) ListAdapterAttempts(ctx context.Context, taskID string) ([]evidence.AdapterAttempt, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT attempt_id, task_id, generation, adapter_kind, target_key, script_step,
		        logical_tick, status, stable_error_code, raw_digest, retry_after_tick
		 FROM adapter_attempts WHERE task_id = ? ORDER BY logical_tick, attempt_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.AdapterAttempt
	for rows.Next() {
		var a evidence.AdapterAttempt
		var kind, status string
		if err := rows.Scan(&a.AttemptID, &a.TaskID, &a.Generation, &kind, &a.TargetKey, &a.ScriptStep,
			&a.LogicalTick, &status, &a.StableErrorCode, &a.RawDigest, &a.RetryAfterTick); err != nil {
			return nil, err
		}
		a.AdapterKind = evidence.AdapterKind(kind)
		a.Status = evidence.AdapterStatus(status)
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- reviews ---

func (s *sqliteBase) SaveReview(ctx context.Context, r evidence.ReviewRecord) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO reviews (task_id, generation, reviewer_id, review_kind, decision, reason_code, created_at_tick)
		 VALUES (?,?,?,?,?,?,?)`,
		r.TaskID, r.Generation, r.ReviewerID, string(r.ReviewKind), string(r.Decision), r.ReasonCode, r.CreatedAtTick)
	if isConstraint(err) {
		return ErrDuplicate
	}
	return err
}

func (s *sqliteBase) ListReviews(ctx context.Context, taskID string) ([]evidence.ReviewRecord, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT task_id, generation, reviewer_id, review_kind, decision, reason_code, created_at_tick
		 FROM reviews WHERE task_id = ? ORDER BY reviewer_id, created_at_tick`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.ReviewRecord
	for rows.Next() {
		var r evidence.ReviewRecord
		var kind, decision string
		if err := rows.Scan(&r.TaskID, &r.Generation, &r.ReviewerID, &kind, &decision, &r.ReasonCode, &r.CreatedAtTick); err != nil {
			return nil, err
		}
		r.ReviewKind = evidence.ReviewKind(kind)
		r.Decision = evidence.Decision(decision)
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- credentials ---

func (s *sqliteBase) SaveCredential(ctx context.Context, c evidence.FinalCredential) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO credentials (task_id, generation, credential_id, terminal_state,
		                          winner_operation_key, issued_at_tick, leased_window_id, digest)
		 VALUES (?,?,?,?,?,?,?,?)`,
		c.TaskID, c.Generation, c.CredentialID, c.TerminalState,
		c.WinnerOperationKey, c.IssuedAtTick, c.LeasedWindowID, c.Digest)
	if isConstraint(err) {
		return ErrDuplicate
	}
	return err
}

func (s *sqliteBase) GetCredential(ctx context.Context, taskID string) (evidence.FinalCredential, bool, error) {
	var c evidence.FinalCredential
	err := s.q.QueryRowContext(ctx,
		`SELECT task_id, generation, credential_id, terminal_state,
		        winner_operation_key, issued_at_tick, leased_window_id, digest
		 FROM credentials WHERE task_id = ?`, taskID).
		Scan(&c.TaskID, &c.Generation, &c.CredentialID, &c.TerminalState,
			&c.WinnerOperationKey, &c.IssuedAtTick, &c.LeasedWindowID, &c.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return evidence.FinalCredential{}, false, nil
	}
	if err != nil {
		return evidence.FinalCredential{}, false, err
	}
	return c, true, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isConstraint reports whether err is a SQLite uniqueness/primary-key
// constraint violation, which maps to the store-level duplicate errors.
func isConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed") ||
		strings.Contains(msg, "PRIMARY KEY")
}

// compile-time assertions that both store flavors satisfy Store.
var (
	_ Store = (*Memory)(nil)
	_ Store = (*sqliteStore)(nil)
	_ Store = (*sqliteTx)(nil)
	_ Store = (*memCore)(nil)
)
