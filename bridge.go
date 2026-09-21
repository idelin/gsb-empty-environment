package bridge

import (
	"errors"
	"fmt"
	"sync"
)

// Bridge holds the currently published plan set and its epoch. Hot reloads
// bump the epoch; frames already in flight keep the plan they started with.
type Bridge struct {
	mu    sync.RWMutex
	ps    *PlanSet
	epoch uint64
}

func NewBridge(ps *PlanSet) *Bridge { return &Bridge{ps: ps, epoch: 1} }

// HotReload publishes a new plan set. It only affects frames that begin
// after this call; in-flight frames stay bound to their original epoch.
func (b *Bridge) HotReload(ps *PlanSet) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ps = ps
	b.epoch++
	return b.epoch
}

func (b *Bridge) Epoch() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.epoch
}

// Binding is a snapshot of (epoch, plan set) taken when a frame starts.
type Binding struct {
	Epoch uint64
	ps    *PlanSet
}

// Bind captures the current epoch and plan set.
func (b *Bridge) Bind() *Binding {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return &Binding{Epoch: b.epoch, ps: b.ps}
}

func (bd *Binding) Digest() [32]byte { return bd.ps.digest }

// Negotiate validates a peer's plan-set digest against this binding. A
// mismatch means the peers compiled incompatible plans and the session is
// refused.
func (bd *Binding) Negotiate(peerDigest [32]byte) error {
	if peerDigest != bd.ps.digest {
		return fmt.Errorf("bridge: negotiation refused: plan digest mismatch (local %x, peer %x)",
			bd.ps.digest[:8], peerDigest[:8])
	}
	return nil
}

// Convert applies the bound plan to src. The sink is invoked only after a
// fully successful conversion, so a failure never publishes a
// half-converted frame.
func (bd *Binding) Convert(src *Frame, lim Limits, sink func(*Frame, *Report)) error {
	var plan *Plan
	switch {
	case src.Version == bd.ps.S1.Version:
		plan = bd.ps.Fwd
	case src.Version == bd.ps.S2.Version:
		plan = bd.ps.Rev
	default:
		return fmt.Errorf("bridge: no plan for frame version %d", src.Version)
	}
	out, rep, err := plan.Convert(src, bd.Epoch, lim)
	if err != nil {
		return err
	}
	if sink != nil {
		sink(out, rep)
	}
	return nil
}

// Receiver reassembles chunked byte input into frames and converts them.
// The epoch/plan binding happens when the first byte of a frame arrives.
type Receiver struct {
	b      *Bridge
	lim    Limits
	parser *Parser
	bind   *Binding
}

func NewReceiver(b *Bridge, lim Limits) *Receiver {
	return &Receiver{b: b, lim: lim}
}

// Feed ingests one arbitrary chunk. Completed frames are converted and
// delivered to sink atomically. Invalid input yields ErrBadData; a valid
// but incomplete prefix is not an error.
func (r *Receiver) Feed(chunk []byte, sink func(*Frame, *Report)) error {
	if r.parser == nil {
		r.parser = NewParser(r.lim)
		r.bind = r.b.Bind()
	}
	if err := r.parser.Feed(chunk); err != nil {
		r.reset()
		return err
	}
	version, payload, err := r.parser.Next()
	if errors.Is(err, ErrNeedMoreData) {
		return nil
	}
	if err != nil {
		r.reset()
		return err
	}
	bind := r.bind
	r.reset()
	fields, err := parseFields(payload, r.lim)
	if err != nil {
		return err
	}
	frame := &Frame{Version: version, Epoch: bind.Epoch, Fields: fields}
	return bind.Convert(frame, r.lim, sink)
}

func (r *Receiver) reset() {
	r.parser = nil
	r.bind = nil
}
