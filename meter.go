package netsec

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

// Direction pins a byte flow to one of the two independent budgets.
// The value is captured by a lease at reservation time so that a tunnel
// switch can never move bytes onto the other direction's budget.
type Direction int

const (
	// ClientUpload is the client -> proxy -> upstream byte flow.
	ClientUpload Direction = iota
	// UpstreamDownload is the upstream -> proxy -> client byte flow.
	UpstreamDownload
)

func (d Direction) String() string {
	switch d {
	case ClientUpload:
		return "client-upload"
	case UpstreamDownload:
		return "upstream-download"
	default:
		return fmt.Sprintf("direction(%d)", int(d))
	}
}

// Phase is the protocol stage at which bytes are accounted. It is fixed
// on the lease, so bytes written before and after a protocol upgrade are
// never merged into one stage.
type Phase int

const (
	// PhaseHeader covers framing bytes exchanged before the body.
	PhaseHeader Phase = iota
	// PhaseBody covers a regular request/response body.
	PhaseBody
	// PhaseTunnel covers bytes after a CONNECT/upgrade handshake.
	PhaseTunnel
)

func (p Phase) String() string {
	switch p {
	case PhaseHeader:
		return "header"
	case PhaseBody:
		return "body"
	case PhaseTunnel:
		return "tunnel"
	default:
		return fmt.Sprintf("phase(%d)", int(p))
	}
}

// Sentinel errors returned by the accounting layer. Callers should use
// errors.Is rather than comparing messages.
var (
	// ErrBudgetExceeded means a reservation does not fit in the fixed
	// directional cap. No bytes are reserved and no write is attempted.
	ErrBudgetExceeded = errors.New("netsec: directional budget exceeded")
	// ErrLeaseSettled means a lease was already committed or released;
	// the repeated call changes nothing.
	ErrLeaseSettled = errors.New("netsec: lease already settled")
	// ErrInvalidSize means a negative byte count or an oversized commit
	// was supplied; meter state is left untouched.
	ErrInvalidSize = errors.New("netsec: invalid byte count")
	// ErrInvalidDirection means the Direction value is not one of the
	// budgeted directions.
	ErrInvalidDirection = errors.New("netsec: invalid direction")
	// ErrInvalidPhase means the Phase value is outside the known stages.
	ErrInvalidPhase = errors.New("netsec: invalid protocol phase")
)

type leaseState int

const (
	leaseOpen leaseState = iota
	leaseSettled
)

// Meter owns the two independent directional budgets. It is safe for
// concurrent use by the duplex host's upload and download pumps.
type Meter struct {
	mu  sync.Mutex
	gen uint64
	dir [2]dirState
}

type dirState struct {
	cap       int64
	reserved  int64
	committed int64
	byGen     map[uint64]int64
	byPhase   map[Phase]int64
}

// NewMeter builds a meter with separate hard caps for client upload and
// upstream download. Negative caps are treated as zero.
func NewMeter(uploadCap, downloadCap int64) *Meter {
	if uploadCap < 0 {
		uploadCap = 0
	}
	if downloadCap < 0 {
		downloadCap = 0
	}
	m := &Meter{gen: 1}
	m.dir[ClientUpload] = dirState{
		cap:     uploadCap,
		byGen:   map[uint64]int64{},
		byPhase: map[Phase]int64{},
	}
	m.dir[UpstreamDownload] = dirState{
		cap:     downloadCap,
		byGen:   map[uint64]int64{},
		byPhase: map[Phase]int64{},
	}
	return m
}

// Generation returns the request generation currently stamped onto new
// leases. Existing leases keep the generation they were born with.
func (m *Meter) Generation() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gen
}

// NextGeneration marks a request/tunnel switch and returns the new
// generation. Outstanding leases are unaffected: they still settle
// against their original generation, which keeps post-switch bytes from
// being attributed to the previous request.
func (m *Meter) NextGeneration() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen++
	return m.gen
}

// Reserve books n bytes against one directional budget and returns a
// lease whose direction, generation and phase are fixed for its lifetime.
// A rejected reservation mutates nothing.
func (m *Meter) Reserve(dir Direction, phase Phase, n int64) (*Lease, error) {
	if dir != ClientUpload && dir != UpstreamDownload {
		return nil, ErrInvalidDirection
	}
	if phase < PhaseHeader || phase > PhaseTunnel {
		return nil, ErrInvalidPhase
	}
	if n < 0 {
		return nil, ErrInvalidSize
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := &m.dir[dir]
	if n > 0 && d.committed+d.reserved+n > d.cap {
		return nil, fmt.Errorf("%w: %s needs %d bytes, %d available",
			ErrBudgetExceeded, dir, n, d.cap-d.committed-d.reserved)
	}
	d.reserved += n
	return &Lease{
		meter: m,
		dir:   dir,
		gen:   m.gen,
		phase: phase,
		size:  n,
	}, nil
}

// Usage is a point-in-time snapshot of one directional budget.
type Usage struct {
	Generation uint64
	Cap        int64
	Reserved   int64
	Committed  int64
}

// Available is the cap minus bytes already sent or held by open leases.
func (u Usage) Available() int64 {
	return u.Cap - u.Committed - u.Reserved
}

// Usage snapshots the budget for dir.
func (m *Meter) Usage(dir Direction) (Usage, error) {
	if dir != ClientUpload && dir != UpstreamDownload {
		return Usage{}, ErrInvalidDirection
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := &m.dir[dir]
	return Usage{
		Generation: m.gen,
		Cap:        d.cap,
		Reserved:   d.reserved,
		Committed:  d.committed,
	}, nil
}

// Committed reports bytes actually sent on dir; those bytes can never be
// refunded.
func (m *Meter) Committed(dir Direction) (int64, error) {
	if dir != ClientUpload && dir != UpstreamDownload {
		return 0, ErrInvalidDirection
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dir[dir].committed, nil
}

// Breakdown reports committed bytes per request generation and per
// protocol phase for dir. The returned maps are copies.
type Breakdown struct {
	ByGeneration map[uint64]int64
	ByPhase      map[Phase]int64
}

// Breakdown snapshots the committed-byte attribution for dir.
func (m *Meter) Breakdown(dir Direction) (Breakdown, error) {
	if dir != ClientUpload && dir != UpstreamDownload {
		return Breakdown{}, ErrInvalidDirection
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := &m.dir[dir]
	out := Breakdown{
		ByGeneration: make(map[uint64]int64, len(d.byGen)),
		ByPhase:      make(map[Phase]int64, len(d.byPhase)),
	}
	for gen, n := range d.byGen {
		out.ByGeneration[gen] = n
	}
	for phase, n := range d.byPhase {
		out.ByPhase[phase] = n
	}
	return out, nil
}

// settle is the single internal path that moves a lease off the
// reserved counter. Committed bytes are non-refundable; the remainder of
// the reservation is returned exactly once.
func (m *Meter) settle(l *Lease, sent int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := &m.dir[l.dir]
	d.reserved -= l.size
	d.committed += sent
	if sent > 0 {
		d.byGen[l.gen] += sent
		d.byPhase[l.phase] += sent
	}
}

// Lease is a fixed-size byte reservation. Direction, request generation
// and protocol phase are immutable for its lifetime. A lease is settled
// exactly once, either by Commit, Release or Settle.
type Lease struct {
	meter *Meter
	dir   Direction
	gen   uint64
	phase Phase
	size  int64

	mu    sync.Mutex
	state leaseState
}

// Direction returns the budget this lease can only ever settle against.
func (l *Lease) Direction() Direction { return l.dir }

// Generation returns the request generation captured at reservation.
func (l *Lease) Generation() uint64 { return l.gen }

// Phase returns the protocol stage captured at reservation.
func (l *Lease) Phase() Phase { return l.phase }

// Size returns the number of reserved bytes.
func (l *Lease) Size() int64 { return l.size }

// Settle records sent bytes (0 <= sent <= Size) as non-refundable and
// returns the amount actually booked (equal to sent). The unused
// portion of the reservation is returned to the budget. It is one-shot:
// a second call returns ErrLeaseSettled and changes nothing, so failure
// paths can call it again safely without double charging or refunding.
func (l *Lease) Settle(sent int64) (int64, error) {
	if sent < 0 || sent > l.size {
		return 0, ErrInvalidSize
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != leaseOpen {
		return 0, ErrLeaseSettled
	}
	l.state = leaseSettled
	l.meter.settle(l, sent)
	return sent, nil
}

// Commit marks the whole reservation as actually sent. It is a
// one-shot convenience wrapper around Settle(Size).
func (l *Lease) Commit() error {
	_, err := l.Settle(l.size)
	return err
}

// Release gives back every reserved byte without charging anything.
// Repeated calls return ErrLeaseSettled without touching the meters.
func (l *Lease) Release() error {
	_, err := l.Settle(0)
	return err
}

// Charge reserves len(p) bytes, writes p through w, then settles the
// lease with the real write outcome:
//
//   - budget rejection: w is never called and no bytes are booked;
//   - full write: the whole reservation is committed;
//   - short write: actual bytes are committed and the rest is released;
//   - write error: actual bytes are committed and the rest is released.
//
// The lease is always settled exactly once, so a failed Charge neither
// double-charges nor double-refunds.
func (m *Meter) Charge(dir Direction, phase Phase, w io.Writer, p []byte) (int, error) {
	lease, err := m.Reserve(dir, phase, int64(len(p)))
	if err != nil {
		return 0, err
	}
	sent, werr := w.Write(p)
	if sent < 0 || sent > len(p) {
		// A broken writer violating io.Writer contract must not corrupt
		// accounting: release everything and surface the violation.
		_ = lease.Release()
		return 0, fmt.Errorf("netsec: writer returned invalid count %d for %d-byte buffer", sent, len(p))
	}
	if _, serr := lease.Settle(int64(sent)); serr != nil {
		return sent, serr
	}
	if werr != nil {
		return sent, werr
	}
	if sent < len(p) {
		return sent, io.ErrShortWrite
	}
	return sent, nil
}

// LeasedWriter adapts an io.Writer to metered charging. The phase can be
// moved forward when a protocol upgrade wins, while the direction stays
// fixed for the lifetime of the writer.
type LeasedWriter struct {
	meter *Meter
	dir   Direction

	mu    sync.Mutex
	phase Phase
	w     io.Writer
}

// NewWriter wraps w so every Write is reserved, charged and settled
// through meter under dir and phase.
func (m *Meter) NewWriter(dir Direction, phase Phase, w io.Writer) (*LeasedWriter, error) {
	if dir != ClientUpload && dir != UpstreamDownload {
		return nil, ErrInvalidDirection
	}
	if phase < PhaseHeader || phase > PhaseTunnel {
		return nil, ErrInvalidPhase
	}
	if w == nil {
		return nil, errors.New("netsec: nil writer")
	}
	return &LeasedWriter{meter: m, dir: dir, phase: phase, w: w}, nil
}

// Write implements io.Writer with the same failure semantics as Meter.Charge.
func (lw *LeasedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	phase := lw.phase
	w := lw.w
	lw.mu.Unlock()
	return lw.meter.Charge(lw.dir, phase, w, p)
}

// SetPhase moves subsequent leases to PhaseTunnel after an upgrade
// handshake. Bytes already leased keep their original phase.
func (lw *LeasedWriter) SetPhase(phase Phase) error {
	if phase < PhaseHeader || phase > PhaseTunnel {
		return ErrInvalidPhase
	}
	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.phase = phase
	return nil
}

// Phase returns the phase currently stamped onto new leases.
func (lw *LeasedWriter) Phase() Phase {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.phase
}

// Direction returns the fixed direction of this writer.
func (lw *LeasedWriter) Direction() Direction { return lw.dir }
