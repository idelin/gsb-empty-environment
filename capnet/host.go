package capnet

import (
	"crypto/rand"
	"crypto/subtle"
	"sync"
	"time"
)

// RealDialer is the signature of the host's genuine network dialer. It is
// captured privately by NewHost and is never reachable from a Router.
type RealDialer func(network, address string) error

// Host is owned by the test host. It mints capability handles, owns the
// trial generations and keeps the only reference to the real dialer.
type Host struct {
	mu     sync.Mutex
	dial   RealDialer
	nextID uint64
	gens   map[uint64]*Trial
}

// Trial is one test generation. Handles minted by a trial are valid only
// until End is called.
type Trial struct {
	host  *Host
	gen   uint64
	clock *fakeClock
	ends  map[uint64]*endpoint
	ended bool
	done  chan struct{}
}

// NewHost builds a Host. dial may be nil; when provided it is retained solely
// so tests can prove the simulator never reaches it.
func NewHost(dial RealDialer) *Host {
	return &Host{dial: dial, gens: make(map[uint64]*Trial)}
}

// NewTrial opens a fresh trial generation.
func (h *Host) NewTrial() *Trial {
	h.mu.Lock()
	defer h.mu.Unlock()
	gen := uint64(len(h.gens)) + 1
	t := &Trial{
		host:  h,
		gen:   gen,
		clock: newFakeClock(),
		ends:  make(map[uint64]*endpoint),
		done:  make(chan struct{}),
	}
	h.gens[gen] = t
	return t
}

// Mint creates a capability handle for a simulated endpoint. address is the
// real target known only to the host: it is stored inside the endpoint and
// never exposed through Handle or Router.
func (t *Trial) Mint(address string, script Script) (Handle, error) {
	h := t.host
	h.mu.Lock()
	defer h.mu.Unlock()
	if t.ended {
		return Handle{}, ErrTrialEnded
	}
	h.nextID++
	id := h.nextID
	var tok [tokenLen]byte
	if _, err := rand.Read(tok[:]); err != nil {
		return Handle{}, err
	}
	ep := &endpoint{
		id:      id,
		gen:     t.gen,
		address: address,
		token:   tok,
		script:  script,
		clock:   t.clock,
	}
	t.ends[id] = ep
	return Handle{host: h, gen: t.gen, id: id, tok: tok}, nil
}

// End closes the generation: all simulated connections and pending timers are
// shut down and every handle minted by this trial becomes stale.
func (t *Trial) End() {
	h := t.host
	h.mu.Lock()
	alreadyEnded := t.ended
	var conns []*simConn
	if !alreadyEnded {
		t.ended = true
		for _, ep := range t.ends {
			if ep.conn != nil {
				conns = append(conns, ep.conn)
			}
		}
	}
	h.mu.Unlock()
	if alreadyEnded {
		return
	}
	for _, c := range conns {
		c.Close()
	}
	t.clock.stopAll()
	close(t.done)
}

// Advance moves the trial's fake clock, firing due timers in deterministic
// (fire-time, insertion-order) order. Host-side only.
func (t *Trial) Advance(d time.Duration) {
	t.clock.advance(d)
}

// Now reports the trial's simulated time. Host-side only.
func (t *Trial) Now() time.Time {
	return t.clock.nowTime()
}

// Writes returns the payloads written to an endpoint so far. Host-side only.
func (t *Trial) Writes(h Handle) ([][]byte, error) {
	ep, err := t.host.resolve(h)
	if err != nil {
		return nil, err
	}
	return ep.connWrites(), nil
}

// resolve is the single chokepoint through which every capability-guarded
// operation passes. It performs all structural and token checks while holding
// the host lock; no I/O is attempted on failure.
func (h *Host) resolve(handle Handle) (*endpoint, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if handle.isZero() || handle.host == nil || handle.host != h {
		return nil, ErrBadHandle
	}
	t, ok := h.gens[handle.gen]
	if !ok {
		return nil, ErrBadHandle
	}
	if t.ended {
		return nil, ErrStaleHandle
	}
	ep, ok := t.ends[handle.id]
	if !ok {
		return nil, ErrBadHandle
	}
	if subtle.ConstantTimeCompare(ep.token[:], handle.tok[:]) != 1 {
		return nil, ErrBadHandle
	}
	return ep, nil
}

// trialDone returns the channel closed when the given generation ends.
func (h *Host) trialDone(gen uint64) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if t, ok := h.gens[gen]; ok {
		return t.done
	}
	return nil
}
