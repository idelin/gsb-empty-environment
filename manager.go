package netsec

import (
	"sync"
	"time"
)

// DefaultDrainTimeout bounds how long a bind waits for previously issued
// searches to reach a terminal protocol state. If the deadline expires the
// connection is considered contaminated and is replaced rather than reused.
const DefaultDrainTimeout = 5 * time.Second

// op tracks one in-flight search on the current connection.
type op struct {
	id       int
	gen      uint64
	leaseID  uint64
	tenantID string

	// canceled is set when the caller gives up on the operation. Entry
	// messages arriving after cancellation are drained and discarded; only
	// the terminal response unlocks connection reuse.
	canceled bool

	// terminal is set once the terminal envelope has been received.
	terminal bool

	// msgCh delivers envelopes to the owning cursor. It is buffered so the
	// reader goroutine never blocks on a slow consumer; a full buffer on a
	// cancelled operation counts as a drained-and-discarded entry.
	msgCh chan Envelope

	// done is closed once the op has been removed from the registry, i.e.
	// its terminal response has been observed.
	done chan struct{}
}

// managedConn wraps a single physical connection together with its reader
// goroutine and its in-flight operation registry.
type managedConn struct {
	conn     Conn
	readerID uint64
	gen      uint64
	dead     bool
	deadCh   chan struct{}
	// readerDone closes when the connection's readLoop has exited, so
	// Close can wait for the sole Recv consumer to stop before returning.
	readerDone chan struct{}

	// nextOpID hands out protocol request ids.
	nextOpID int
	ops      map[int]*op
}

// Manager serializes bind and search traffic over a single pooled directory
// connection while enforcing generation/lease isolation.
type Manager struct {
	dial    Dialer
	confirm IdentityConfirmer
	drainTO time.Duration

	mu      sync.Mutex
	current *managedConn

	// binding is true while a bind owns the connection exclusively.
	binding  bool
	bindWait chan struct{}

	// drainSig is closed (and replaced) whenever an in-flight operation
	// terminates, waking a bind blocked in beginExclusive.
	drainSig chan struct{}

	// lease state of the most recent successful bind.
	gen     uint64
	leaseID uint64
	tenant  Tenant

	closed bool

	stats Stats
}

// Stats summarizes lease-layer enforcement activity. All counters are
// monotonic for the lifetime of the manager.
type Stats struct {
	BindsAttempted         int64
	BindsSucceeded         int64
	BindsFailed            int64
	AnonymousRefused       int64
	IdentityRejected       int64
	ConnectionsDialed      int64
	ConnectionsPoisoned    int64
	SearchesStarted        int64
	EntriesDelivered       int64
	LateOrDropped          int64
	CanceledEntriesDrained int64
}

// Config configures a Manager.
type Config struct {
	Dialer       Dialer
	Confirmer    IdentityConfirmer
	DrainTimeout time.Duration
}

// NewManager constructs a lease manager. The first connection is opened
// lazily by the first Acquire call.
func NewManager(cfg Config) *Manager {
	drainTO := cfg.DrainTimeout
	if drainTO <= 0 {
		drainTO = DefaultDrainTimeout
	}
	return &Manager{
		dial:     cfg.Dialer,
		confirm:  cfg.Confirmer,
		drainTO:  drainTO,
		bindWait: make(chan struct{}),
		drainSig: make(chan struct{}),
	}
}

// lease is the concrete Lease implementation handed to clients.
type lease struct {
	tenant  Tenant
	gen     uint64
	leaseID uint64
}

func (l *lease) Tenant() Tenant     { return l.tenant }
func (l *lease) Generation() uint64 { return l.gen }
func (l *lease) LeaseID() uint64    { return l.leaseID }
