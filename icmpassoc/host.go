package icmpassoc

import (
	"errors"
	"sync"
)

// NotifyFunc is invoked when an ICMP error is proven to belong to a
// connection. It must not block on Host methods that take the same lock for
// long; implementations should hand work to another goroutine if needed.
type NotifyFunc func(notice Notice)

// Notice carries a fully associated ICMP error to the owning connection.
type Notice struct {
	// Flow is the normalized connection tuple in host namespace.
	Flow FlowKey
	// Generation is the active generation that accepted the error.
	Generation uint64
	// ICMPType and ICMPCode are passed through verbatim from the outer
	// ICMP message. Their meaning differs between ICMPv4 and ICMPv6.
	ICMPType uint8
	ICMPCode uint8
	// QuotedLen is the number of transport bytes available in the quote.
	QuotedLen int
	// TCPSeq is the quoted sequence number; HasTCPSeq is false for UDP.
	TCPSeq    uint32
	HasTCPSeq bool
}

// Host owns the table of active transport connections and the fixed-version
// address mappers used to bring quoted addresses into its namespace.
type Host struct {
	mu      sync.Mutex
	mappers map[IPVersion]AddrMapper
	conns   map[tupleKey]*connEntry
}

type connEntry struct {
	key        tupleKey
	flow       FlowKey
	generation uint64
	active     bool
	window     SeqWindow
	hasWindow  bool
	notify     NotifyFunc
}

// Option configures a Host at construction.
type Option func(*Host)

// WithMapper registers a version-locked mapper. A second mapper for the same
// family replaces the first.
func WithMapper(m AddrMapper) Option {
	return func(h *Host) {
		if m == nil {
			return
		}
		h.mappers[m.Version()] = m
	}
}

// NewHost creates a connection manager. At least one fixed-version mapper is
// normally supplied via WithMapper; without one every quoted datagram of
// that family returns ReasonNoMapper.
func NewHost(opts ...Option) *Host {
	h := &Host{
		mappers: make(map[IPVersion]AddrMapper),
		conns:   make(map[tupleKey]*connEntry),
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// Register opens a new generation for a flow tuple. Reusing a tuple after
// Close bumps the generation so stale forged errors cannot reach the new
// connection. Registering while the current generation is active fails.
func (h *Host) Register(flow FlowKey, notify NotifyFunc) (*Conn, error) {
	if notify == nil {
		return nil, errors.New("icmpassoc: notify function is required")
	}
	key, err := keyOf(flow)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if existing, ok := h.conns[key]; ok && existing.active {
		return nil, ErrAlreadyRegistered
	}

	var generation uint64 = 1
	if existing, ok := h.conns[key]; ok {
		generation = existing.generation + 1
	}
	entry := &connEntry{
		key:        key,
		flow:       flowFromKey(key),
		generation: generation,
		active:     true,
		notify:     notify,
	}
	h.conns[key] = entry
	return &Conn{host: h, key: key, generation: generation}, nil
}

// Close retires the active generation of the connection. The tuple history
// is retained so a later Register can take the next generation and so stale
// errors are rejected with ReasonGeneration rather than silently dropped.
func (h *Host) Close(c *Conn) error {
	if c == nil {
		return ErrNotRegistered
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok := h.conns[c.key]
	if !ok || entry.generation != c.generation || !entry.active {
		return ErrNotRegistered
	}
	entry.active = false
	return nil
}

// SetSeqWindow installs the acceptable TCP sequence-number window for the
// active generation. It must be updated as the connection advances so that
// only in-window quoted segments can associate an error.
func (h *Host) SetSeqWindow(c *Conn, window SeqWindow) error {
	if !window.Valid() {
		return ErrInvalidWindow
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	entry, ok := h.conns[c.key]
	if !ok || entry.generation != c.generation || !entry.active {
		return ErrNotRegistered
	}
	if entry.key.protocol != ProtoTCP {
		return errors.New("icmpassoc: sequence windows apply only to TCP")
	}
	entry.window = window
	entry.hasWindow = true
	return nil
}

// Result reports how an ICMP error was handled. When Status is
// NotAssociated, err is non-nil and wraps ErrCannotAssociate; Reason
// explains the cause.
type Result struct {
	Status     Status
	Reason     Reason
	Generation uint64
	err        error
}

// Status is the outcome of association.
type Status uint8

const (
	// StatusDelivered means the error matched an active generation and the
	// connection's NotifyFunc was invoked.
	StatusDelivered Status = iota + 1
	// StatusNotAssociated means the error could not be bound safely.
	StatusNotAssociated
)

// Error returns the association error, or nil when the error was delivered.
func (r Result) Error() error { return r.err }

// HandleICMPError processes the transport-relevant portion of an ICMP
// error. icmpType/icmpCode are the outer message's type and code; quoted is
// the original datagram bytes the ICMP message refers to (IP header plus
// whatever transport bytes are available).
//
// Association requires, together: the right IP version/family via a
// fixed-version mapper, protocol, both addresses, both ports, the current
// active generation, and—for TCP—a quoted sequence number inside that
// generation's window. Any failure is reported as ErrCannotAssociate and no
// connection is notified.
func (h *Host) HandleICMPError(icmpType, icmpCode uint8, quoted []byte) Result {
	parsed, reason := parseQuotedDatagram(quoted)
	if reason != ReasonNone {
		return fail(reason)
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	mapper, ok := h.mappers[parsed.version]
	if !ok {
		return fail(ReasonNoMapper)
	}
	if mapper.Version() != parsed.version {
		return fail(ReasonVersionMismatch)
	}

	local, err := mapper.Translate(parsed.local)
	if err != nil {
		return fail(ReasonVersionMismatch)
	}
	remote, err := mapper.Translate(parsed.remote)
	if err != nil {
		return fail(ReasonVersionMismatch)
	}
	if local.Version != parsed.version || remote.Version != parsed.version {
		return fail(ReasonVersionMismatch)
	}

	key := tupleKey{
		version:  parsed.version,
		protocol: parsed.protocol,
		local:    normalize(local),
		remote:   normalize(remote),
	}
	entry, ok := h.conns[key]
	if !ok {
		return fail(ReasonNoConn)
	}
	if !entry.active {
		return failGeneration(entry.generation)
	}

	if entry.key.protocol == ProtoTCP {
		if !entry.hasWindow {
			return fail(ReasonNoWindow)
		}
		if !parsed.hasTCPSeq || !entry.window.Contains(parsed.tcpSeq) {
			return fail(ReasonSeqOutside)
		}
	}

	notice := Notice{
		Flow:       entry.flow,
		Generation: entry.generation,
		ICMPType:   icmpType,
		ICMPCode:   icmpCode,
		QuotedLen:  parsed.transportLen,
		TCPSeq:     parsed.tcpSeq,
		HasTCPSeq:  parsed.hasTCPSeq,
	}
	notify := entry.notify
	notify(notice)

	return Result{Status: StatusDelivered, Reason: ReasonNone, Generation: entry.generation}
}

func fail(reason Reason) Result {
	return Result{
		Status: StatusNotAssociated,
		Reason: reason,
		err:    errors.Join(ErrCannotAssociate, errors.New(string(reason))),
	}
}

func failGeneration(generation uint64) Result {
	r := fail(ReasonGeneration)
	r.Generation = generation
	return r
}

func flowFromKey(key tupleKey) FlowKey {
	return FlowKey{
		Version:  key.version,
		Protocol: key.protocol,
		Local:    key.local,
		Remote:   key.remote,
	}
}

// Conn is a handle to one registered generation of a flow.
type Conn struct {
	host       *Host
	key        tupleKey
	generation uint64
}

// Generation reports the generation represented by this handle.
func (c *Conn) Generation() uint64 { return c.generation }

// Close retires the connection's generation through its host.
func (c *Conn) Close() error { return c.host.Close(c) }

// SetSeqWindow updates the TCP sequence window for the active generation.
func (c *Conn) SetSeqWindow(window SeqWindow) error {
	return c.host.SetSeqWindow(c, window)
}
