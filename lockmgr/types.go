package lockmgr

import "time"

// Savepoint marks a point inside a transaction. Rolling back to it releases
// exactly the locks acquired (or upgraded) after it was created.
type Savepoint struct {
	id   uint64
	name string
	// seq is the global monotonic sequence at which the savepoint was taken.
	seq uint64
}

// ID returns the numeric savepoint identifier.
func (s Savepoint) ID() uint64 { return s.id }

// Name returns the optional savepoint name.
func (s Savepoint) Name() string { return s.name }

// modeStep records one conversion of a held lock so it can be undone on a
// savepoint rollback.
type modeStep struct {
	from LockMode
	to   LockMode
	seq  uint64
}

// grantedLock is the lock a transaction currently holds on one resource.
type grantedLock struct {
	resource Resource
	mode     LockMode
	// history is the stack of mode conversions (initial acquisition plus
	// every upgrade), each tagged with the global sequence at which it
	// happened.
	history []modeStep
}

// savepoint is the internal mutable savepoint bookkeeping.
type savepoint struct {
	Savepoint
}

// Transaction is one lock-owning unit. Its methods are safe for concurrent
// use only through the Manager that created it.
type Transaction struct {
	id    uint64
	start uint64 // global sequence assigned at Begin; smaller means older

	active  bool
	aborted bool

	// locks maps resource key to the currently held lock.
	locks map[string]*grantedLock
	// savepoints is the savepoint stack, oldest first.
	savepoints []*savepoint
	// pending is the transaction's single outstanding blocked request.
	pending *waiter
}

// ID returns the transaction identifier.
func (t *Transaction) ID() uint64 { return t.id }

// Active reports whether the transaction may still acquire or release locks.
func (t *Transaction) Active() bool { return t.active }

// waiter is a queued lock request.
type waiter struct {
	txn      *Transaction
	resource Resource
	want     LockMode
	upgrade  bool
	// cont holds the remaining steps of a multi-resource Lock call once this
	// step is granted. Nil for single-resource / final-step requests.
	cont []lockStep
	// ch is signalled exactly once, either with nil when the lock is granted
	// or with the deadlock/abort error that ended the wait.
	ch chan error
}

// resourceState holds the lock holders and wait queue of one resource.
type resourceState struct {
	resource Resource
	holders  map[uint64]*grantedLock
	// queue keeps waiting requests. Upgrade waiters are always inserted
	// before ordinary new waiters.
	queue []*waiter
}

func newResourceState(r Resource) *resourceState {
	return &resourceState{
		resource: r,
		holders:  make(map[uint64]*grantedLock),
	}
}

// Edge is one directed waiting-for relationship between two transactions.
type Edge struct {
	From     uint64
	To       uint64
	Resource Resource
	Mode     LockMode
}

// WaiterInfo is a snapshot of one queued request, useful for tests and
// diagnostics.
type WaiterInfo struct {
	TxnID   uint64
	Want    LockMode
	Upgrade bool
}

// Resolution records one deadlock arbitration outcome.
type Resolution struct {
	// Cycle is the ordered list of transaction ids forming the broken cycle.
	Cycle []uint64
	// Victim is the chosen transaction id.
	Victim uint64
	// Savepoint is non-nil when the victim was rolled back to this savepoint;
	// a nil Savepoint means the whole transaction was aborted.
	Savepoint *Savepoint
	// Cost is the number of held locks released / conversions undone (full
	// abort uses the total number of held locks).
	Cost int
	At   time.Time
}
