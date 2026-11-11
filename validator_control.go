package netsec

import (
	"net/netip"
	"sort"
	"time"
)

// HandleResponse processes a PATH_RESPONSE datagram. conn is the connection
// instance carried by the datagram, local is the socket that received it and
// peer is the datagram's actual source address. A response validates only when
// all three challenge bindings (connection instance, candidate path, nonce)
// match an outstanding challenge.
func (v *Validator) HandleResponse(conn ConnID, local, peer netip.AddrPort, nonce Nonce) error {
	v.mu.Lock()
	var acts actions
	err := v.handleResponseLocked(conn, local, peer, nonce, &acts)
	v.mu.Unlock()
	acts.run()
	return err
}

func (v *Validator) handleResponseLocked(
	conn ConnID,
	local, peer netip.AddrPort,
	nonce Nonce,
	acts *actions,
) error {
	if v.closed {
		return ErrClosed
	}
	// Defense in depth: even if a peer replays a perfectly valid old-instance
	// response, it can never validate the current instance.
	if conn != v.conn {
		return ErrStaleConnection
	}
	key := Path{Conn: conn, Local: local, Peer: peer}
	c, ok := v.candidates[key]
	if !ok {
		// The datagram arrived on a different local/peer pair than any
		// registered candidate: another path's response cannot authenticate
		// this address.
		return ErrUnknownPath
	}
	if _, ok := c.nonces[nonce]; !ok {
		return ErrNonceMismatch
	}

	// One successful round trip clears all outstanding challenges for this
	// path: older retransmitted responses must not validate anything again.
	c.nonces = make(map[Nonce]struct{})
	c.validated = true
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	path := c.path
	acts.add(func() {
		v.emit(Event{Kind: EventValidated, Path: path})
	})
	v.reselectLocked(acts)
	return nil
}

// reselectLocked asks the strategy to choose the active path among all
// validated candidates. A validated path that is not selected stays validated
// but remains ineligible for sending until the strategy picks it.
func (v *Validator) reselectLocked(acts *actions) {
	chosen, ok := v.strategy.Select(v.validatedPathsLocked())
	if !ok {
		if v.active != nil {
			prev := *v.active
			v.active = nil
			acts.add(func() {
				v.emit(Event{Kind: EventDeactivated, Path: prev})
			})
		}
		return
	}
	if v.active == nil || *v.active != chosen {
		v.active = &chosen
		acts.add(func() {
			v.emit(Event{Kind: EventActivated, Path: chosen})
		})
	}
}

// Migrate moves the host to a new connection instance. All candidates and the
// active path of the previous instance are invalidated, so validation
// material captured under an old connection ID cannot be reused.
func (v *Validator) Migrate(newConn ConnID) {
	v.mu.Lock()
	var acts actions
	if !v.closed && newConn != v.conn {
		v.conn = newConn
		v.dropAllLocked(ReasonMigrated, &acts)
	}
	v.mu.Unlock()
	acts.run()
}

// Cancel tears down a single candidate, for example because the host abandoned
// that address. It reports [ErrUnknownPath] if no such candidate exists.
func (v *Validator) Cancel(path Path) error {
	v.mu.Lock()
	var acts actions
	if v.closed {
		v.mu.Unlock()
		return ErrClosed
	}
	c, ok := v.candidates[path]
	if !ok {
		v.mu.Unlock()
		return ErrUnknownPath
	}
	v.failLocked(c, ReasonCanceled, &acts)
	if v.active != nil && *v.active == path {
		prev := *v.active
		v.active = nil
		acts.add(func() {
			v.emit(Event{Kind: EventDeactivated, Path: prev})
		})
		v.reselectLocked(&acts)
	}
	v.mu.Unlock()
	acts.run()
	return nil
}

// Close invalidates every candidate and rejects further operations.
func (v *Validator) Close() {
	v.mu.Lock()
	var acts actions
	if !v.closed {
		v.closed = true
		v.dropAllLocked(ReasonCanceled, &acts)
	}
	v.mu.Unlock()
	acts.run()
}

func (v *Validator) dropAllLocked(reason FailureReason, acts *actions) {
	var activePath Path
	hadActive := v.active != nil
	if hadActive {
		activePath = *v.active
	}
	for _, c := range v.candidates {
		v.failLocked(c, reason, acts)
	}
	if hadActive {
		v.active = nil
		acts.add(func() {
			v.emit(Event{Kind: EventDeactivated, Path: activePath})
		})
	}
}

// Conn reports the current connection instance.
func (v *Validator) Conn() ConnID {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.conn
}

// Active returns the active path and whether one exists.
func (v *Validator) Active() (Path, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.active == nil {
		return Path{}, false
	}
	return *v.active, true
}

// IsValidated reports whether path completed a successful challenge round trip.
func (v *Validator) IsValidated(path Path) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	c, ok := v.candidates[path]
	return ok && c.validated
}

// CheckSend enforces the sending restriction: application data may leave only
// on the single active path, which by construction is validated and selected
// by the strategy.
func (v *Validator) CheckSend(path Path) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return ErrClosed
	}
	c, ok := v.candidates[path]
	if !ok {
		return ErrUnknownPath
	}
	if !c.validated {
		return ErrPathNotValidated
	}
	if v.active == nil || *v.active != path {
		return ErrPathNotActive
	}
	return nil
}

// Snapshot describes a registered candidate.
type Snapshot struct {
	Path      Path
	Validated bool
	Active    bool
	Deadline  time.Time
}

// Snapshots returns the current candidate states, ordered by registration.
func (v *Validator) Snapshots() []Snapshot {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []Snapshot
	for _, c := range v.candidates {
		out = append(out, Snapshot{
			Path:      c.path,
			Validated: c.validated,
			Active:    v.active != nil && *v.active == c.path,
			Deadline:  c.deadline,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return v.candidates[out[i].Path].seq < v.candidates[out[j].Path].seq
	})
	return out
}
