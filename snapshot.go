package netsec

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Sentinel errors returned by the Store and Snapshot.
var (
	// ErrRecordNotFound is returned when no record exists for the given key.
	ErrRecordNotFound = errors.New("netsec: record not found")
	// ErrRecordExists is returned when Create targets an existing record.
	ErrRecordExists = errors.New("netsec: record already exists")
	// ErrVersionStale is returned when a snapshot pins a version that was
	// superseded by a newer version without being withdrawn.
	ErrVersionStale = errors.New("netsec: snapshot version is no longer current")
	// ErrVersionWithdrawn is returned when a snapshot pins a withdrawn version.
	ErrVersionWithdrawn = errors.New("netsec: snapshot version has been withdrawn")
	// ErrBelowThreshold is returned when the pinned version's contributor count
	// is below the configured publish threshold.
	ErrBelowThreshold = errors.New("netsec: contributor count below publish threshold")
	// ErrInsufficientBudget is returned when the cost exceeds the budget that
	// remains on the pinned version.
	ErrInsufficientBudget = errors.New("netsec: insufficient remaining budget")
	// ErrInvalidArgument is returned for malformed arguments or invariants that
	// a withdrawal would violate (e.g. increasing count or budget).
	ErrInvalidArgument = errors.New("netsec: invalid argument")
)

// AuditKind classifies an audit trail entry.
type AuditKind string

const (
	AuditKindCreate   AuditKind = "create"
	AuditKindWithdraw AuditKind = "withdraw"
	AuditKindPublish  AuditKind = "publish"
)

// RecordConfig configures a record at creation time.
type RecordConfig struct {
	// Threshold is the minimum contributor count required to publish.
	Threshold int
	// Budget is the initial publish budget measured in cost units.
	Budget int64
}

// VersionView is an immutable point-in-time view of one record version.
type VersionView struct {
	Record          string
	Version         int64
	Count           int
	Threshold       int
	BudgetRemaining int64
	Withdrawn       bool
}

// AuditEntry is one append-only audit record. Entries are never modified or
// deleted, including entries that belong to a withdrawn version.
type AuditEntry struct {
	Record          string
	Seq             uint64
	Version         int64
	Kind            AuditKind
	Count           int
	Value           any
	Cost            int64
	BudgetRemaining int64
	At              time.Time
}

// Hooks are invoked inside storage transactions while the affected record is
// locked. They act as synchronization barriers for callers (notably tests) and
// allow a transaction to be aborted before any write is applied.
type Hooks struct {
	// BeforePublishCommit runs after the version, threshold and budget checks
	// have passed and before the budget is deducted or an audit entry appended.
	// A non-nil error aborts the publish transaction without side effects.
	BeforePublishCommit func(ctx context.Context, record string, view VersionView) error
	// BeforeWithdrawCommit runs after the withdrawal arguments have been
	// validated and before the old version is marked withdrawn or the new
	// version is installed. A non-nil error aborts the withdrawal.
	BeforeWithdrawCommit func(ctx context.Context, record string, view VersionView) error
}

// recordVersion is one version of a record. The budget field is only ever
// mutated by a publish transaction that has proven the version is still current.
type recordVersion struct {
	id              int64
	count           int
	threshold       int
	budgetRemaining int64
	withdrawn       bool
}

// record holds the append-only version chain and audit trail for one key.
// Versions are never removed; withdrawn versions stay readable for audit.
type record struct {
	mu       sync.Mutex
	versions []*recordVersion
	current  int64
	seq      uint64
	audit    []AuditEntry
}

func (r *record) versionByID(id int64) (*recordVersion, error) {
	idx := int(id) - 1
	if idx < 0 || idx >= len(r.versions) {
		return nil, ErrVersionStale
	}
	return r.versions[idx], nil
}

// Store is the transactional, versioned storage backend used by publish hosts.
// Each record carries its own lock, so transactions on different records run
// in parallel while transactions touching the same record are serialized with
// strict serializable semantics.
type Store struct {
	mu      sync.Mutex
	records map[string]*record
	hooks   Hooks
}

// NewStore creates an empty Store. A nil hooks value is valid.
func NewStore(hooks *Hooks) *Store {
	store := &Store{records: make(map[string]*record)}
	if hooks != nil {
		store.hooks = *hooks
	}
	return store
}

// txn is a single-record storage transaction. The record lock is acquired at
// begin and held until commit or rollback, which makes the transaction's
// reads and writes atomic with respect to every other transaction on the same
// record. A deep snapshot taken at begin lets rollback restore every mutated
// field (withdrawal flags, per-version budgets and the audit trail) when a
// hook or context cancellation aborts the transaction.
type txn struct {
	ctx   context.Context
	rec   *record
	done  bool
	apply bool

	// Snapshot captured before any write; restored verbatim on rollback.
	prev record
}

// begin locks the named record for the lifetime of the transaction.
func (s *Store) begin(ctx context.Context, recordName string) (*txn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	rec := s.records[recordName]
	s.mu.Unlock()
	if rec == nil {
		return nil, ErrRecordNotFound
	}
	rec.mu.Lock()
	t := &txn{
		ctx: ctx,
		rec: rec,
		prev: record{
			versions: append([]*recordVersion(nil), rec.versions...),
			current:  rec.current,
			seq:      rec.seq,
			audit:    append([]AuditEntry(nil), rec.audit...),
		},
	}
	return t, nil
}

func (t *txn) rollback() {
	if t.done {
		return
	}
	t.done = true
	if t.apply {
		t.rec.versions = t.prev.versions
		t.rec.current = t.prev.current
		t.rec.seq = t.prev.seq
		t.rec.audit = t.prev.audit
	}
	t.rec.mu.Unlock()
}

// commit makes the transaction visible by releasing the record lock, after a
// final context check. It returns the context error and rolls back on cancel.
func (t *txn) commit() error {
	if t.done {
		return errors.New("netsec: transaction already finished")
	}
	if err := t.ctx.Err(); err != nil {
		t.rollback()
		return err
	}
	t.done = true
	t.rec.mu.Unlock()
	return nil
}

func (t *txn) view(recordName string, v *recordVersion) VersionView {
	return VersionView{
		Record:          recordName,
		Version:         v.id,
		Count:           v.count,
		Threshold:       v.threshold,
		BudgetRemaining: v.budgetRemaining,
		Withdrawn:       v.withdrawn,
	}
}

func (t *txn) nextSeq() uint64 {
	t.rec.seq++
	return t.rec.seq
}

// Create installs a new record at version 1 and returns its initial snapshot.
func (s *Store) Create(ctx context.Context, recordName string, count int, cfg RecordConfig) (*Snapshot, error) {
	if recordName == "" || count < 0 || cfg.Threshold <= 0 || cfg.Budget < 0 {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[recordName]; ok {
		return nil, ErrRecordExists
	}
	rec := &record{current: 1}
	version := &recordVersion{
		id:              1,
		count:           count,
		threshold:       cfg.Threshold,
		budgetRemaining: cfg.Budget,
	}
	rec.versions = []*recordVersion{version}
	rec.seq = 1
	rec.audit = []AuditEntry{{
		Record:          recordName,
		Seq:             1,
		Version:         1,
		Kind:            AuditKindCreate,
		Count:           count,
		BudgetRemaining: cfg.Budget,
		At:              time.Now().UTC(),
	}}
	s.records[recordName] = rec

	return &Snapshot{store: s, record: recordName, version: 1}, nil
}

// Withdraw records that contributors have left the record. The currently
// pinned version is atomically marked withdrawn and a new version is installed
// with the remaining contributor count and budget. Withdrawal never removes
// old versions or audit entries.
func (s *Store) Withdraw(ctx context.Context, recordName string, remainingCount int, remainingBudget int64) (*Snapshot, error) {
	if remainingCount < 0 || remainingBudget < 0 {
		return nil, ErrInvalidArgument
	}
	t, err := s.begin(ctx, recordName)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			t.rollback()
		}
	}()

	current, err := t.rec.versionByID(t.rec.current)
	if err != nil {
		return nil, err
	}
	// A withdrawal can only shrink the protected population and budget.
	if remainingCount > current.count || remainingBudget > current.budgetRemaining {
		return nil, ErrInvalidArgument
	}
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	if s.hooks.BeforeWithdrawCommit != nil {
		if err := s.hooks.BeforeWithdrawCommit(ctx, recordName, t.view(recordName, current)); err != nil {
			return nil, err
		}
	}

	t.apply = true
	current.withdrawn = true
	next := &recordVersion{
		id:              current.id + 1,
		count:           remainingCount,
		threshold:       current.threshold,
		budgetRemaining: remainingBudget,
	}
	t.rec.versions = append(t.rec.versions, next)
	t.rec.current = next.id
	t.rec.audit = append(t.rec.audit, AuditEntry{
		Record:          recordName,
		Seq:             t.nextSeq(),
		Version:         next.id,
		Kind:            AuditKindWithdraw,
		Count:           remainingCount,
		BudgetRemaining: remainingBudget,
		At:              time.Now().UTC(),
	})

	if err := t.commit(); err != nil {
		return nil, err
	}
	committed = true
	return &Snapshot{store: s, record: recordName, version: next.id}, nil
}

// CurrentSnapshot returns a snapshot pinned to the record's current version.
func (s *Store) CurrentSnapshot(ctx context.Context, recordName string) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	rec := s.records[recordName]
	s.mu.Unlock()
	if rec == nil {
		return nil, ErrRecordNotFound
	}
	rec.mu.Lock()
	version := rec.current
	rec.mu.Unlock()
	return &Snapshot{store: s, record: recordName, version: version}, nil
}

// Audit returns a copy of the record's append-only audit trail.
func (s *Store) Audit(ctx context.Context, recordName string) ([]AuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	rec := s.records[recordName]
	s.mu.Unlock()
	if rec == nil {
		return nil, ErrRecordNotFound
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	trail := make([]AuditEntry, len(rec.audit))
	copy(trail, rec.audit)
	return trail, nil
}

// Snapshot is a fixed-version handle on a record. Queries always read the
// pinned version, and Publish binds the threshold check, published value and
// budget deduction to that same version inside one transaction.
type Snapshot struct {
	store   *Store
	record  string
	version int64
}

// Record returns the record key.
func (s *Snapshot) Record() string { return s.record }

// Version returns the pinned record version.
func (s *Snapshot) Version() int64 { return s.version }

// View reads the pinned version. Old versions stay readable after withdrawal;
// only publishing from them is rejected.
func (s *Snapshot) View(ctx context.Context) (VersionView, error) {
	if err := ctx.Err(); err != nil {
		return VersionView{}, err
	}
	s.store.mu.Lock()
	rec := s.store.records[s.record]
	s.store.mu.Unlock()
	if rec == nil {
		return VersionView{}, ErrRecordNotFound
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	version, err := rec.versionByID(s.version)
	if err != nil {
		return VersionView{}, err
	}
	return VersionView{
		Record:          s.record,
		Version:         version.id,
		Count:           version.count,
		Threshold:       version.threshold,
		BudgetRemaining: version.budgetRemaining,
		Withdrawn:       version.withdrawn,
	}, nil
}

// Publish emits value from the pinned version if and only if, within a single
// transaction:
//   - the pinned version is still the record's current (non-withdrawn) version;
//   - its contributor count meets the threshold;
//   - cost fits the budget remaining on that same version.
//
// On success the budget deduction and the append-only audit entry (including
// the published value) are committed together. A withdrawal that lands first
// makes the publish fail with ErrVersionWithdrawn so no new result is ever
// released from a stale snapshot, while previously committed audit entries
// remain untouched.
func (s *Snapshot) Publish(ctx context.Context, value any, cost int64) (AuditEntry, error) {
	if cost < 0 {
		return AuditEntry{}, ErrInvalidArgument
	}
	t, err := s.store.begin(ctx, s.record)
	if err != nil {
		return AuditEntry{}, err
	}
	committed := false
	defer func() {
		if !committed {
			t.rollback()
		}
	}()

	version, err := t.rec.versionByID(s.version)
	if err != nil {
		return AuditEntry{}, err
	}
	// A snapshot can only publish while it pins the current version. A
	// withdrawal marks the old version withdrawn and advances the pointer, so
	// every publish racing it fails here instead of leaking a shrunken group.
	if t.rec.current != version.id {
		if version.withdrawn {
			return AuditEntry{}, ErrVersionWithdrawn
		}
		return AuditEntry{}, ErrVersionStale
	}
	if version.withdrawn {
		return AuditEntry{}, ErrVersionWithdrawn
	}
	if version.count < version.threshold {
		return AuditEntry{}, ErrBelowThreshold
	}
	if cost > version.budgetRemaining {
		return AuditEntry{}, ErrInsufficientBudget
	}
	if err := t.ctx.Err(); err != nil {
		return AuditEntry{}, err
	}
	if s.store.hooks.BeforePublishCommit != nil {
		if err := s.store.hooks.BeforePublishCommit(ctx, s.record, t.view(s.record, version)); err != nil {
			return AuditEntry{}, err
		}
	}

	t.apply = true
	version.budgetRemaining -= cost
	entry := AuditEntry{
		Record:          s.record,
		Seq:             t.nextSeq(),
		Version:         version.id,
		Kind:            AuditKindPublish,
		Count:           version.count,
		Value:           value,
		Cost:            cost,
		BudgetRemaining: version.budgetRemaining,
		At:              time.Now().UTC(),
	}
	t.rec.audit = append(t.rec.audit, entry)

	if err := t.commit(); err != nil {
		return AuditEntry{}, err
	}
	committed = true
	return entry, nil
}
