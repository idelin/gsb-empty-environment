// Package ssi implements an in-memory Serializable Snapshot Isolation (SSI)
// conflict detector on top of a small table/key transactional store.
package ssi

import (
	"errors"
	"sort"
	"sync"
)

// Errors returned by the transaction manager.
var (
	// ErrSerialization is returned when SSI detects that a transaction is
	// part of a non-serializable rw-antidependency structure.
	ErrSerialization = errors.New("ssi: serialization failure, transaction aborted due to rw-antidependency cycle")
	// ErrAborted is returned when an operation is attempted on a transaction
	// that was explicitly aborted by the caller.
	ErrAborted = errors.New("ssi: transaction has been aborted")
	// ErrCommitted is returned when an operation is attempted on a
	// transaction that has already committed.
	ErrCommitted = errors.New("ssi: transaction has already committed")
	// ErrReadOnlyWrite is returned when Write is called on a read-only
	// transaction.
	ErrReadOnlyWrite = errors.New("ssi: write attempted on read-only transaction")
)

// TxnID identifies a transaction within a Manager.
type TxnID uint64

// TxnStatus is the lifecycle state of a transaction.
type TxnStatus int

const (
	// StatusActive covers transactions between Begin and Prepare/Commit/Abort.
	StatusActive TxnStatus = iota
	// StatusPrepared covers transactions that passed Prepare and await Commit.
	StatusPrepared
	// StatusCommitted covers committed transactions.
	StatusCommitted
	// StatusAborted covers aborted (explicit or SSI-killed) transactions.
	StatusAborted
)

func (s TxnStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusPrepared:
		return "prepared"
	case StatusCommitted:
		return "committed"
	case StatusAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

// defaultEscalationThreshold is the number of row SIREAD locks a transaction
// may hold on one table before they are promoted to a table-level lock.
const defaultEscalationThreshold = 8

// Manager is the SSI transaction manager. It is safe for concurrent use.
type Manager struct {
	mu sync.Mutex

	nextID  TxnID
	nextSeq uint64

	escalationThreshold int

	// txns retains every transaction object, including finished ones, while
	// active tracks transactions still in flight (active or prepared).
	txns   map[TxnID]*Txn
	active map[TxnID]*Txn

	// Global SIREAD lock indexes.
	rowLocks   map[string]map[string]map[TxnID]struct{}
	rangeLocks map[string]map[keyRange]map[TxnID]struct{}
	tableLocks map[string]map[TxnID]struct{}

	// writerByKey records which transactions wrote which keys, including
	// committed transactions whose SIREAD locks must still be retained.
	writerByKey map[string]map[string]map[TxnID]struct{}

	// versions is the committed multi-version store, newest last.
	versions map[string]map[string][]version
}

type version struct {
	commitSeq uint64
	value     string
}

// Txn is a transaction handle.
type Txn struct {
	id        TxnID
	readOnly  bool
	beginSeq  uint64
	commitSeq uint64
	status    TxnStatus

	// abortCause distinguishes explicit aborts from SSI kills.
	abortCause error

	// safeSnapshot is set for read-only transactions that satisfy the Safe
	// Snapshot condition: they can never participate in a dangerous structure.
	safeSnapshot bool

	// rw-antidependency adjacency.
	out map[TxnID]struct{}
	in  map[TxnID]struct{}

	// Per-transaction SIREAD lock bookkeeping.
	rowLocks   map[string]map[string]struct{}
	rangeLocks map[string][]keyRange
	tableLocks map[string]struct{}

	// Buffered writes installed into the store at commit.
	writes map[string]map[string]string
}

// ID returns the transaction identifier.
func (t *Txn) ID() TxnID { return t.id }

// Status returns the current lifecycle status.
func (t *Txn) Status() TxnStatus { return t.status }

// ReadOnly reports whether the transaction was started with WithReadOnly.
func (t *Txn) ReadOnly() bool { return t.readOnly }

// SafeSnapshot reports whether the read-only transaction obtained a safe
// snapshot and is therefore exempt from dangerous-structure aborts.
func (t *Txn) SafeSnapshot() bool { return t.safeSnapshot }

// CommitSeq returns the global commit sequence number, or zero when the
// transaction has not committed.
func (t *Txn) CommitSeq() uint64 { return t.commitSeq }

// BeginOption customizes a Begin call.
type BeginOption func(*beginConfig)

type beginConfig struct {
	readOnly bool
}

// WithReadOnly starts a read-only transaction.
func WithReadOnly() BeginOption {
	return func(c *beginConfig) { c.readOnly = true }
}

// ManagerOption customizes a Manager at construction.
type ManagerOption func(*Manager)

// WithEscalationThreshold overrides the row-lock escalation threshold. A
// transaction holding more than threshold row SIREAD locks on one table is
// promoted to a table-level SIREAD lock on that table.
func WithEscalationThreshold(threshold int) ManagerOption {
	return func(m *Manager) {
		if threshold > 0 {
			m.escalationThreshold = threshold
		}
	}
}

// NewManager creates an SSI transaction manager.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		nextID:              1,
		nextSeq:             1,
		escalationThreshold: defaultEscalationThreshold,
		txns:                make(map[TxnID]*Txn),
		active:              make(map[TxnID]*Txn),
		rowLocks:            make(map[string]map[string]map[TxnID]struct{}),
		rangeLocks:          make(map[string]map[keyRange]map[TxnID]struct{}),
		tableLocks:          make(map[string]map[TxnID]struct{}),
		writerByKey:         make(map[string]map[string]map[TxnID]struct{}),
		versions:            make(map[string]map[string][]version),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Begin starts a new transaction with a fresh snapshot.
func (m *Manager) Begin(opts ...BeginOption) *Txn {
	cfg := beginConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	t := &Txn{
		id:         m.nextID,
		readOnly:   cfg.readOnly,
		beginSeq:   m.nextSeq,
		status:     StatusActive,
		abortCause: ErrAborted,
		out:        make(map[TxnID]struct{}),
		in:         make(map[TxnID]struct{}),
		rowLocks:   make(map[string]map[string]struct{}),
		rangeLocks: make(map[string][]keyRange),
		tableLocks: make(map[string]struct{}),
		writes:     make(map[string]map[string]string),
	}
	m.nextID++
	m.nextSeq++
	m.txns[t.id] = t
	m.active[t.id] = t

	if t.readOnly {
		t.safeSnapshot = m.evalSafeSnapshot(t)
	}
	return t
}

// evalSafeSnapshot implements the Safe Snapshot test for read-only
// transactions: the snapshot is safe when no concurrent read-write
// transaction already carries any rw conflict flag. The check is
// deliberately conservative, so it never grants exemption to a transaction
// that could later be part of a dangerous structure.
func (m *Manager) evalSafeSnapshot(ro *Txn) bool {
	for _, t := range m.active {
		if t.id == ro.id || t.readOnly {
			continue
		}
		if len(t.in) > 0 || len(t.out) > 0 {
			return false
		}
	}
	return true
}

func (t *Txn) requireActive() error {
	switch t.status {
	case StatusActive, StatusPrepared:
		return nil
	case StatusCommitted:
		return ErrCommitted
	default:
		return t.abortCause
	}
}

func (t *Txn) bufferedWrite(table, key string) (string, bool) {
	if keys, ok := t.writes[table]; ok {
		val, ok := keys[key]
		return val, ok
	}
	return "", false
}

// KVPair is one key/value entry returned by RangeRead.
type KVPair struct {
	Key   string
	Value string
}

// Read returns the value of key as visible to the transaction's snapshot.
// The boolean is false when the key does not exist. A row SIREAD lock is
// acquired and rw edges against invisible concurrent writers are recorded.
func (m *Manager) Read(t *Txn, table, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := t.requireActive(); err != nil {
		return "", false, err
	}
	if val, ok := t.bufferedWrite(table, key); ok {
		return val, true, nil
	}

	if !t.safeSnapshot {
		m.acquireRowLock(t, table, key)
		m.addReadWriteConflicts(t, table, key)
	}

	val, ok := m.readVisible(t, table, key)
	return val, ok, nil
}

// RangeRead returns the committed keys in the inclusive range [start, end]
// visible to the transaction's snapshot, ordered by key. Empty start/end
// denote the smallest/largest possible key. An index-range SIREAD lock is
// acquired over the range.
func (m *Manager) RangeRead(t *Txn, table, start, end string) ([]KVPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := t.requireActive(); err != nil {
		return nil, err
	}

	r := keyRange{start: start, end: end}
	if !t.safeSnapshot {
		m.acquireRangeLock(t, table, r)
		m.addRangeReadWriteConflicts(t, table, r)
	}

	var out []KVPair
	for key, vers := range m.versions[table] {
		if !r.contains(key) {
			continue
		}
		if own, ownOK := t.bufferedWrite(table, key); ownOK {
			out = append(out, KVPair{Key: key, Value: own})
			continue
		}
		if val, ok := visibleVersion(vers, t.beginSeq); ok {
			out = append(out, KVPair{Key: key, Value: val})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Write buffers a write. Row/range/table SIREAD locks held by other
// transactions create rw edges, and dangerous structures are detected here
// as well as at commit time.
func (m *Manager) Write(t *Txn, table, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := t.requireActive(); err != nil {
		return err
	}
	if t.readOnly {
		return ErrReadOnlyWrite
	}

	m.addWriteConflicts(t, table, key)
	m.recordWrite(t, table, key)

	if t.writes[table] == nil {
		t.writes[table] = make(map[string]string)
	}
	t.writes[table][key] = value

	if victim := m.detectDangerous(t); victim != nil {
		m.cleanupAborted(victim, ErrSerialization)
		if victim.id == t.id {
			return ErrSerialization
		}
	}
	return nil
}

// Prepare validates the transaction against the current conflict graph and
// moves it to StatusPrepared.
func (m *Manager) Prepare(t *Txn) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := t.requireActive(); err != nil {
		return err
	}
	if victim := m.detectDangerous(t); victim != nil {
		m.cleanupAborted(victim, ErrSerialization)
		if victim.id == t.id {
			return ErrSerialization
		}
	}
	t.status = StatusPrepared
	return nil
}

// Commit installs the buffered writes and runs the final dangerous
// structure check. A retained SIREAD lock of an already committed
// transaction can still force this commit to abort.
func (m *Manager) Commit(t *Txn) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch t.status {
	case StatusAborted:
		return t.abortCause
	case StatusCommitted:
		return ErrCommitted
	case StatusActive, StatusPrepared:
	}

	if victim := m.detectDangerous(t); victim != nil {
		m.cleanupAborted(victim, ErrSerialization)
		if victim.id == t.id {
			return ErrSerialization
		}
	}

	commitSeq := m.nextSeq
	m.nextSeq++
	for table, keys := range t.writes {
		if m.versions[table] == nil {
			m.versions[table] = make(map[string][]version)
		}
		for key, val := range keys {
			m.versions[table][key] = append(m.versions[table][key], version{
				commitSeq: commitSeq,
				value:     val,
			})
		}
	}
	t.commitSeq = commitSeq
	t.status = StatusCommitted
	delete(m.active, t.id)

	// Post-commit check: this transaction may be the earlier-committed T3
	// that completes a dangerous structure around an active pivot.
	if victim := m.detectDangerous(t); victim != nil {
		m.cleanupAborted(victim, ErrSerialization)
	}

	m.vacuum()
	return nil
}

// Abort terminates the transaction, retracting every rw edge it caused and
// releasing its SIREAD locks and write records immediately.
func (m *Manager) Abort(t *Txn) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if t.status == StatusCommitted {
		return ErrCommitted
	}
	if t.status == StatusAborted {
		m.vacuum()
		return nil
	}
	m.cleanupAborted(t, ErrAborted)
	m.vacuum()
	return nil
}

func (m *Manager) readVisible(t *Txn, table, key string) (string, bool) {
	if own, ok := t.bufferedWrite(table, key); ok {
		return own, true
	}
	if vers, ok := m.versions[table][key]; ok {
		return visibleVersion(vers, t.beginSeq)
	}
	return "", false
}

func visibleVersion(vers []version, snapshotSeq uint64) (string, bool) {
	for i := len(vers) - 1; i >= 0; i-- {
		if vers[i].commitSeq < snapshotSeq {
			return vers[i].value, true
		}
	}
	return "", false
}
