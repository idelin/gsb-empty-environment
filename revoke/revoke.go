// Package revoke implements a generation-aware dispatch layer for a Go
// messaging host.
//
// Every protected frame is bound to the authentication generation that was
// active when the frame was submitted. A logout drops a revocation barrier at a
// single linearization point shared with callback admission: frames that have
// already entered their sensitive callback are allowed to finish, while every
// frame that has not started yet is revoked under the old generation. A later
// login establishes a fresh generation and never re-marks or re-admits frames
// queued by a superseded generation.
package revoke

import (
	"errors"
	"sort"
	"sync"
)

// Reason explains why a frame was or was not authorized.
type Reason string

const (
	// ReasonAuthorized marks a frame whose sensitive callback ran.
	ReasonAuthorized Reason = "authorized"
	// ReasonRevoked marks a frame of a generation whose logout barrier
	// was reached before the frame could start.
	ReasonRevoked Reason = "revoked"
	// ReasonNoSession marks a frame submitted while no generation is active.
	ReasonNoSession Reason = "no-session"
	// ReasonStaleToken marks a frame submitted against a generation that is
	// no longer the active one, while another active generation exists.
	ReasonStaleToken Reason = "stale-token"
)

// ErrNoActiveSession is returned by Logout when no generation is currently
// active.
var ErrNoActiveSession = errors.New("revoke: no active session")

// ErrAlreadyLoggedIn is returned by Login while a generation is still active;
// the caller must Logout before establishing a new generation.
var ErrAlreadyLoggedIn = errors.New("revoke: session already active")

// Generation is an opaque, sequence-ordered authentication token handed out by
// Login. Its identity must be carried by every frame submitted on its behalf.
type Generation struct {
	id uint64
}

// GenID exposes the monotonically increasing generation identifier, useful for
// tests and audit logging.
func (g Generation) GenID() uint64 { return g.id }

// AuthContext is handed to an authorized sensitive callback.
type AuthContext struct {
	Generation Generation
	FrameID    string
}

// SensitiveFunc is a frame callback that must only run while its frame
// generation is still the one authorized to act.
type SensitiveFunc func(AuthContext)

// Decision is the authorization basis recorded for every submitted frame.
type Decision struct {
	FrameID    string
	GenID      uint64
	Authorized bool
	Reason     Reason
	// Point is the global linearization point at which callback admission or
	// revocation was decided. Admission points and revocation barriers draw
	// from one shared sequence, so their relative order is unambiguous.
	Point uint64
}

// Confirmation is returned once a logout is fully settled.
type Confirmation struct {
	GenID uint64
	// BarrierPoint is the linearization point of the logout revocation barrier.
	BarrierPoint uint64
	// Decisions covers every frame of the logged-out generation, including
	// frames revoked at the barrier and frames whose callbacks were already in
	// flight when the barrier was dropped.
	Decisions []Decision
}

// Executor runs admitted tasks. The dispatch package ships a goroutine-backed
// executor and a PausableExecutor whose scheduling can be driven one task at a
// time for deterministic interleaving tests.
type Executor interface {
	Go(func())
}

type frame struct {
	id        string
	gen       Generation
	sensitive SensitiveFunc
	decision  chan Decision
	decided   bool
	final     Decision
	scheduled chan struct{}
}

// Dispatcher serializes frame admission and revocation onto one mutex. All
// ordering questions — does this frame start before or after the logout
// barrier — are answered while holding mu, against a single point counter.
type Dispatcher struct {
	exec Executor

	mu       sync.Mutex
	seq      uint64
	active   *Generation
	genSeq   uint64
	queued   map[string]*frame
	inflight map[string]*frame
	registry map[string]*frame
	barrier  uint64
	cond     *sync.Cond
}

// New builds a Dispatcher that dispatches admitted callbacks through exec.
func New(exec Executor) *Dispatcher {
	return &Dispatcher{
		exec:     exec,
		queued:   make(map[string]*frame),
		inflight: make(map[string]*frame),
		registry: make(map[string]*frame),
	}
}

func (d *Dispatcher) nextPoint() uint64 {
	d.seq++
	return d.seq
}

// Login establishes a fresh authentication generation. A prior session must be
// fully logged out first; queued frames of older generations are never
// re-marked and remain revoked.
func (d *Dispatcher) Login() (Generation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.active != nil {
		return Generation{}, ErrAlreadyLoggedIn
	}
	d.genSeq++
	gen := Generation{id: d.genSeq}
	d.active = &gen
	return gen, nil
}

// Receipt lets the host wait for a frame's final authorization decision.
type Receipt struct {
	frame *frame
}

// Submit binds frameID to gen and queues its sensitive callback. The returned
// receipt resolves once admission or revocation has been decided.
func (d *Dispatcher) Submit(gen Generation, frameID string, fn SensitiveFunc) (*Receipt, Decision) {
	d.mu.Lock()
	f := &frame{
		id:        frameID,
		gen:       gen,
		sensitive: fn,
		decision:  make(chan Decision, 1),
		scheduled: make(chan struct{}),
	}

	switch {
	case d.active == nil:
		dec := Decision{
			FrameID: frameID, GenID: gen.id, Authorized: false,
			Reason: ReasonNoSession, Point: d.nextPoint(),
		}
		f.decided = true
		f.final = dec
		d.mu.Unlock()
		f.decision <- dec
		return &Receipt{frame: f}, dec
	case d.active.id != gen.id:
		reason := ReasonStaleToken
		// A token belonging to a generation whose own session has ended is
		// reported as revoked rather than merely stale.
		if gen.id < d.active.id {
			reason = ReasonRevoked
		}
		dec := Decision{
			FrameID: frameID, GenID: gen.id, Authorized: false,
			Reason: reason, Point: d.nextPoint(),
		}
		f.decided = true
		f.final = dec
		d.mu.Unlock()
		f.decision <- dec
		return &Receipt{frame: f}, dec
	default:
		d.queued[frameID] = f
		d.registry[frameID] = f
		d.mu.Unlock()
	}

	d.exec.Go(func() { d.runFrame(f) })
	return &Receipt{frame: f}, Decision{}
}

// Decision blocks until the frame's authorization basis is known.
func (r *Receipt) Decision() Decision {
	return <-r.frame.decision
}

// TryDecision reports the decision without blocking.
func (r *Receipt) TryDecision() (Decision, bool) {
	select {
	case dec := <-r.frame.decision:
		r.frame.decision <- dec
		return dec, true
	default:
		return Decision{}, false
	}
}

func (d *Dispatcher) runFrame(f *frame) {
	d.mu.Lock()
	// The admission check and the logout barrier use the same lock and point
	// counter, establishing one shared linear order.
	if d.active == nil || d.active.id != f.gen.id {
		point := d.nextPoint()
		// A frame whose own generation ended at the current logout barrier is
		// rejected at that barrier point, so every not-yet-started frame of a
		// logged-out generation shares one revocation linearization point.
		if f.gen.id <= d.genSeq && d.barrier != 0 {
			point = d.barrier
		}
		dec := Decision{
			FrameID: f.id, GenID: f.gen.id, Authorized: false,
			Reason: ReasonRevoked, Point: point,
		}
		if _, queued := d.queued[f.id]; queued {
			delete(d.queued, f.id)
		}
		d.deliverLocked(f, dec)
		close(f.scheduled)
		d.mu.Unlock()
		return
	}

	delete(d.queued, f.id)
	d.inflight[f.id] = f
	dec := Decision{
		FrameID: f.id, GenID: f.gen.id, Authorized: true,
		Reason: ReasonAuthorized, Point: d.nextPoint(),
	}
	close(f.scheduled)
	ctx := AuthContext{Generation: f.gen, FrameID: f.id}
	sensitive := f.sensitive
	d.mu.Unlock()

	// The callback runs outside the lock; the logout barrier waits for it.
	sensitive(ctx)

	d.mu.Lock()
	delete(d.inflight, f.id)
	d.deliverLocked(f, dec)
	d.broadcastLocked()
	d.mu.Unlock()
}

func (d *Dispatcher) deliverLocked(f *frame, dec Decision) {
	if f.decided {
		return
	}
	f.decided = true
	f.final = dec
	f.decision <- dec
}

// Logout drops the revocation barrier for the active generation. It blocks
// until every already-started sensitive callback of that generation has
// returned; all not-yet-started frames of the generation are revoked at the
// barrier. The returned Confirmation lists the authorization basis for every
// frame of the generation.
func (d *Dispatcher) Logout() (Confirmation, error) {
	d.mu.Lock()
	if d.active == nil {
		d.mu.Unlock()
		return Confirmation{}, ErrNoActiveSession
	}
	genID := d.active.id
	barrier := d.nextPoint()
	d.active = nil
	d.barrier = barrier

	var pending []*frame
	for id, f := range d.queued {
		if f.gen.id == genID {
			delete(d.queued, id)
			pending = append(pending, f)
		}
	}

	decisions := make([]Decision, 0, len(pending))
	for _, f := range pending {
		dec := Decision{
			FrameID: f.id, GenID: genID, Authorized: false,
			Reason: ReasonRevoked, Point: barrier,
		}
		d.deliverLocked(f, dec)
	}

	// Wait for in-flight callbacks. Any in-flight frame belongs to the
	// generation being logged out; it was admitted at a point before the
	// barrier and is therefore authorized to complete.
	for len(d.inflight) > 0 {
		d.condWaitLocked()
	}

	// The confirmation enumerates every frame ever submitted against this
	// generation: frames completed before the barrier, frames in flight when
	// the barrier dropped, and frames revoked at the barrier. All have settled
	// by now, so each carries its final authorization basis.
	for _, f := range d.registry {
		if f.gen.id == genID {
			decisions = append(decisions, f.final)
		}
	}
	sort.Slice(decisions, func(i, j int) bool {
		if decisions[i].FrameID != decisions[j].FrameID {
			return decisions[i].FrameID < decisions[j].FrameID
		}
		return decisions[i].Point < decisions[j].Point
	})

	confirmation := Confirmation{
		GenID: genID, BarrierPoint: barrier,
		Decisions: decisions,
	}
	d.mu.Unlock()
	return confirmation, nil
}

// condWaitLocked suspends Logout until an in-flight callback finishes. It is
// implemented with a condition variable allocated lazily so that Dispatcher
// stays usable as a zero-friendly value via New.
func (d *Dispatcher) condWaitLocked() {
	if d.cond == nil {
		d.cond = sync.NewCond(&d.mu)
	}
	d.cond.Wait()
}

func (d *Dispatcher) broadcastLocked() {
	if d.cond != nil {
		d.cond.Broadcast()
	}
}

// WaitScheduled blocks until the frame's admission or revocation decision has
// been taken under the dispatcher's shared linear order. Test drivers use it
// after starting an executor task so that the corresponding linearization
// point is settled before the next interleaving action.
func (r *Receipt) WaitScheduled() {
	<-r.frame.scheduled
}
