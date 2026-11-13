package netsec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Session is one transport incarnation of a BGP neighbor.  Its fields are
// protected by the owning Ledger's mutex.
type Session struct {
	ledger   *Ledger
	identity SessionIdentity
	caps     Capabilities
	active   bool
	eor      bool
}

// SessionOptions is accepted by OpenSession alongside Peer/SessionIdentity
// and Capabilities arguments.
type SessionOptions struct {
	Peer                Peer
	NeighborAS          ASN
	RouterID            RouterID
	TransportGeneration uint64
	GracefulRestart     bool
	Capabilities        Capabilities
	PolicyVersion       PolicyVersion
	VRPVersion          VRPVersion
}

// OpenSession establishes a transport incarnation.  Accepted arguments
// include SessionIdentity, Peer, Capabilities, bool (graceful restart), ASN,
// RouterID string, and a transport generation (int or uint64).  Examples:
//
//	OpenSession(SessionIdentity{...}, Capabilities{GracefulRestart: true})
//	OpenSession(Peer{NeighborAS: 65001, RouterID: "192.0.2.1"}, uint64(7), true)
//	OpenSession(65001, "192.0.2.1", 7, true)
//
// A reconnect for the same (NeighborAS, RouterID) requires a strictly larger
// transport generation.  A different neighbor AS reusing the same router ID is
// a different identity and cannot refresh the first peer's stale routes.
func (l *Ledger) OpenSession(args ...any) (*Session, error) {
	opts, err := parseOpenArgs(args)
	if err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowLocked()
	l.pruneLocked(now)

	identity := SessionIdentity{
		NeighborAS:          opts.NeighborAS,
		RouterID:            strings.TrimSpace(opts.RouterID),
		TransportGeneration: opts.TransportGeneration,
		TransportGen:        opts.TransportGeneration,
		PolicyVersion:       l.policyVersion,
		VRPVersion:          l.vrpVersion,
	}.normalized()
	if identity.RouterID == "" && opts.Peer.RouterID != "" {
		identity.RouterID = strings.TrimSpace(opts.Peer.RouterID)
	}
	if identity.NeighborAS == 0 && opts.Peer.NeighborAS != 0 {
		identity.NeighborAS = opts.Peer.NeighborAS
	}
	if identity.RouterID == "" {
		return nil, fmt.Errorf("%w: missing router ID", ErrInvalidSession)
	}

	peer := identity.peer()
	if current, ok := l.sessions[peer.key()]; ok {
		return nil, fmt.Errorf("%w: active generation %d", ErrSessionExists, current.identity.generation())
	}
	if l.hasLastGen[peer.key()] && identity.TransportGeneration <= l.lastGen[peer.key()] {
		return nil, fmt.Errorf("%w: got %d, last %d", ErrStaleGeneration,
			identity.TransportGeneration, l.lastGen[peer.key()])
	}

	caps := opts.Capabilities
	caps.GracefulRestart = caps.GracefulRestart || caps.Restart || caps.Graceful || caps.GR || opts.GracefulRestart
	caps.Restart = caps.GracefulRestart
	caps.Graceful = caps.GracefulRestart
	caps.GR = caps.GracefulRestart

	s := &Session{
		ledger:   l,
		identity: identity,
		caps:     caps,
		active:   true,
	}
	l.setSessionLocked(peer, s)
	return s, nil
}

// Connect is an alias for OpenSession.
func (l *Ledger) Connect(args ...any) (*Session, error) { return l.OpenSession(args...) }

// NewSession is an alias for OpenSession.
func (l *Ledger) NewSession(args ...any) (*Session, error) { return l.OpenSession(args...) }

func parseOpenArgs(args []any) (SessionOptions, error) {
	var opts SessionOptions
	var haveAS, haveRID, haveGen bool

	setIdentity := func(id SessionIdentity) {
		if id.effectiveAS() != 0 || !haveAS {
			opts.NeighborAS = id.effectiveAS()
			haveAS = id.effectiveAS() != 0 || haveAS
		}
		if id.RouterID != "" {
			opts.RouterID = id.RouterID
			haveRID = true
		}
		if id.generation() != 0 || id.TransportGeneration != 0 || id.TransportGen != 0 || id.TransportID != 0 {
			opts.TransportGeneration = id.generation()
			haveGen = true
		}
		if id.PolicyVersion != 0 {
			opts.PolicyVersion = id.PolicyVersion
		}
		if id.VRPVersion != 0 {
			opts.VRPVersion = id.VRPVersion
		}
	}

	for _, arg := range args {
		switch v := arg.(type) {
		case SessionIdentity:
			setIdentity(v)
		case *SessionIdentity:
			if v != nil {
				setIdentity(*v)
			}
		case Peer:
			opts.Peer = v.normalized()
			if v.effectiveAS() != 0 || !haveAS {
				opts.NeighborAS = v.effectiveAS()
				haveAS = v.effectiveAS() != 0 || haveAS
			}
			if v.RouterID != "" {
				opts.RouterID = v.RouterID
				haveRID = true
			}
		case *Peer:
			if v != nil {
				opts.Peer = v.normalized()
				if v.effectiveAS() != 0 || !haveAS {
					opts.NeighborAS = v.effectiveAS()
					haveAS = v.effectiveAS() != 0 || haveAS
				}
				if v.RouterID != "" {
					opts.RouterID = v.RouterID
					haveRID = true
				}
			}
		case SessionOptions:
			opts = mergeSessionOptions(opts, v)
			haveAS = haveAS || v.NeighborAS != 0
			haveRID = haveRID || v.RouterID != ""
			haveGen = haveGen || v.TransportGeneration != 0
		case *SessionOptions:
			if v != nil {
				opts = mergeSessionOptions(opts, *v)
				haveAS = haveAS || v.NeighborAS != 0
				haveRID = haveRID || v.RouterID != ""
				haveGen = haveGen || v.TransportGeneration != 0
			}
		case Capabilities:
			opts.Capabilities = mergeCapabilities(opts.Capabilities, v)
		case *Capabilities:
			if v != nil {
				opts.Capabilities = mergeCapabilities(opts.Capabilities, *v)
			}
		case bool:
			opts.GracefulRestart = opts.GracefulRestart || v
		case ASN:
			if !haveAS {
				opts.NeighborAS = v
				haveAS = true
			} else if !haveGen {
				opts.TransportGeneration = uint64(v)
				haveGen = true
			} else {
				return opts, fmt.Errorf("%w: unexpected ASN %d", ErrInvalidSession, v)
			}
		case uint64:
			if !haveGen {
				opts.TransportGeneration = v
				haveGen = true
			} else if !haveAS {
				opts.NeighborAS = ASN(v)
				haveAS = true
			} else {
				return opts, fmt.Errorf("%w: unexpected uint64 %d", ErrInvalidSession, v)
			}
		case int:
			if v < 0 {
				return opts, fmt.Errorf("%w: negative integer %d", ErrInvalidSession, v)
			}
			if !haveAS {
				opts.NeighborAS = ASN(v)
				haveAS = true
			} else if !haveGen {
				opts.TransportGeneration = uint64(v)
				haveGen = true
			} else {
				return opts, fmt.Errorf("%w: unexpected integer %d", ErrInvalidSession, v)
			}
		case string:
			if opts.RouterID == "" {
				opts.RouterID = v
				haveRID = true
			}
		case nil:
		default:
			return opts, fmt.Errorf("%w: unsupported argument %T", ErrInvalidSession, arg)
		}
	}
	if !haveAS && opts.Peer.NeighborAS != 0 {
		opts.NeighborAS = opts.Peer.NeighborAS
	}
	if !haveRID && opts.Peer.RouterID != "" {
		opts.RouterID = opts.Peer.RouterID
	}
	return opts, nil
}

func mergeSessionOptions(a, b SessionOptions) SessionOptions {
	if b.NeighborAS != 0 {
		a.NeighborAS = b.NeighborAS
	}
	if b.RouterID != "" {
		a.RouterID = b.RouterID
	}
	if b.TransportGeneration != 0 {
		a.TransportGeneration = b.TransportGeneration
	}
	if b.Peer.NeighborAS != 0 || b.Peer.RouterID != "" {
		a.Peer = b.Peer
	}
	a.GracefulRestart = a.GracefulRestart || b.GracefulRestart
	a.Capabilities = mergeCapabilities(a.Capabilities, b.Capabilities)
	if b.PolicyVersion != 0 {
		a.PolicyVersion = b.PolicyVersion
	}
	if b.VRPVersion != 0 {
		a.VRPVersion = b.VRPVersion
	}
	return a
}

func mergeCapabilities(a, b Capabilities) Capabilities {
	a.GracefulRestart = a.GracefulRestart || b.GracefulRestart || b.Restart || b.Graceful || b.GR
	a.Graceful = a.GracefulRestart
	a.Restart = a.GracefulRestart
	a.GR = a.GracefulRestart
	if b.RestartTime != 0 {
		a.RestartTime = b.RestartTime
	}
	return a
}

// Identity returns a copy of the session identity.
func (s *Session) Identity() SessionIdentity {
	s.ledger.mu.Lock()
	defer s.ledger.mu.Unlock()
	return s.identity
}

// Peer returns the bound neighbor identity.
func (s *Session) Peer() Peer {
	return s.Identity().peer()
}

// TransportGeneration returns the session's transport generation.
func (s *Session) TransportGeneration() uint64 {
	return s.Identity().generation()
}

// GracefulRestart reports whether graceful restart was negotiated.
func (s *Session) GracefulRestart() bool {
	s.ledger.mu.Lock()
	defer s.ledger.mu.Unlock()
	return s.caps.graceful()
}

// Active reports whether the transport is still established.
func (s *Session) Active() bool {
	s.ledger.mu.Lock()
	defer s.ledger.mu.Unlock()
	return s.active
}

// EORReceived reports whether End-of-RIB has been processed.
func (s *Session) EORReceived() bool {
	s.ledger.mu.Lock()
	defer s.ledger.mu.Unlock()
	return s.eor
}

// Announce installs or refreshes a route.  Before End-of-RIB, an announcement
// for an older generation's stale route replaces that stale record.  The new
// announcement is always checked against the current import policy and the
// current VRP.
func (s *Session) Announce(args ...any) error {
	r, err := parseRouteArgs(args)
	if err != nil {
		return err
	}

	l := s.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if !s.active {
		return ErrSessionClosed
	}
	now := l.nowLocked()
	l.pruneLocked(now)

	r = r.normalized()
	if r.Prefix == "" {
		return fmt.Errorf("%w: missing prefix", ErrInvalidRoute)
	}

	policyOK, vrpOK := l.checkLocked(r)
	if !policyOK {
		return fmt.Errorf("%w: %s", ErrPolicyRejected, r.Prefix)
	}
	if !vrpOK {
		return fmt.Errorf("%w: %s", ErrVRPRejected, r.Prefix)
	}

	peer := s.identity.peer()
	generation := s.identity.generation()
	if k, stale, ok := l.findRefreshTargetLocked(peer, generation, r, !s.eor); ok {
		if r.ID == "" {
			r.ID = stale.route.ID
			r = r.normalized()
		}
		delete(l.stale, k)
		l.addFreshLocked(r, peer, generation, now)
		l.emitLocked(Event{
			Type:          EventRefreshed,
			At:            now,
			RouteID:       r.ID,
			Prefix:        r.Prefix,
			AttrsDigest:   r.AttrsDigest,
			Peer:          peer,
			Generation:    generation,
			PolicyVersion: l.policyVersion,
			VRPVersion:    l.vrpVersion,
		})
		return nil
	}

	if r.ID == "" {
		r.ID = generatedRouteID(s.identity, r.Prefix)
		r = r.normalized()
	}
	l.addFreshLocked(r, peer, generation, now)
	return nil
}

// Advertise is an alias for Announce.
func (s *Session) Advertise(args ...any) error { return s.Announce(args...) }

func generatedRouteID(id SessionIdentity, prefix Prefix) string {
	return prefix + "@" + id.RouterID + "#" + strconv.FormatUint(id.generation(), 10)
}

// Withdraw removes a fresh route and any matching stale record for this peer.
// An explicit withdrawal is effective even before End-of-RIB.
func (s *Session) Withdraw(args ...any) bool {
	id, prefix := parseWithdrawArgs(args)
	l := s.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if !s.active {
		return false
	}
	now := l.nowLocked()
	l.pruneLocked(now)
	peer := s.identity.peer()

	removed := false
	for k, e := range l.routes {
		if k.peerKey != peer.key() {
			continue
		}
		if (id != "" && k.routeID == id) || (id == "" && prefix != "" && e.route.Prefix == prefix) {
			l.removeFreshLocked(k, WithdrawExplicit, now)
			removed = true
		}
	}
	keys := make([]staleKey, 0)
	for k, e := range l.stale {
		if k.peerKey != peer.key() {
			continue
		}
		if (id != "" && k.routeID == id) || (id == "" && prefix != "" && e.route.Prefix == prefix) {
			keys = append(keys, k)
		}
	}
	sortStaleKeys(keys)
	for _, k := range keys {
		l.removeStaleLocked(k, WithdrawExplicit, now, false)
		removed = true
	}
	return removed
}

// WithdrawPrefix removes all fresh and stale routes for prefix from this peer.
func (s *Session) WithdrawPrefix(prefix Prefix) bool {
	return s.Withdraw(Prefix(strings.TrimSpace(prefix)))
}

func parseWithdrawArgs(args []any) (RouteID, Prefix) {
	var id RouteID
	var prefix Prefix
	for _, arg := range args {
		switch v := arg.(type) {
		case Route:
			if v.ID != "" {
				id = v.ID
			}
			if v.Prefix != "" {
				prefix = v.Prefix
			}
		case *Route:
			if v != nil {
				if v.ID != "" {
					id = v.ID
				}
				if v.Prefix != "" {
					prefix = v.Prefix
				}
			}
		case string:
			if strings.Contains(v, "/") {
				prefix = strings.TrimSpace(v)
			} else if id == "" {
				id = strings.TrimSpace(v)
			}
		}
	}
	return id, prefix
}

// EndOfRIB completes the initial route exchange.  All stale routes from older
// generations of the same peer that were not refreshed are withdrawn.  A
// duplicate End-of-RIB is harmless.
func (s *Session) EndOfRIB() error {
	l := s.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if s.eor {
		return nil
	}
	if !s.active {
		return ErrSessionClosed
	}
	now := l.nowLocked()
	l.pruneLocked(now)
	s.eor = true

	peer := s.identity.peer()
	generation := s.identity.generation()
	keys := make([]staleKey, 0)
	for k := range l.stale {
		if k.peerKey == peer.key() && k.generation < generation {
			keys = append(keys, k)
		}
	}
	sortStaleKeys(keys)
	for _, k := range keys {
		l.removeStaleLocked(k, WithdrawEOR, now, false)
	}
	l.emitLocked(Event{
		Type:          EventEOR,
		At:            now,
		Peer:          peer,
		Generation:    generation,
		PolicyVersion: l.policyVersion,
		VRPVersion:    l.vrpVersion,
	})
	return nil
}

// EOR is an alias for EndOfRIB.
func (s *Session) EOR() error { return s.EndOfRIB() }

// EndOfRib is an alias for EndOfRIB.
func (s *Session) EndOfRib() error { return s.EndOfRIB() }

// Disconnect tears down the transport.  Routes are marked stale only when the
// peer negotiated graceful restart, the configured restart time is positive,
// and authentication did not fail.  An authentication failure also revokes any
// older stale records for the same peer: a failed reauthentication is not a
// valid basis for keeping a withdrawn peer's paths alive.
func (s *Session) Disconnect(args ...any) error {
	reason := parseDisconnectReason(args)
	l := s.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if !s.active {
		return nil
	}
	now := l.nowLocked()
	l.pruneLocked(now)

	authFailed := reason.AuthenticationFailed()
	grace := !authFailed && s.caps.graceful() && l.restartTime > 0
	peer := s.identity.peer()

	keys := make([]routeKey, 0)
	for k, e := range l.routes {
		if k.peerKey == peer.key() && e.generation == s.identity.generation() {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].routeID != keys[j].routeID {
			return keys[i].routeID < keys[j].routeID
		}
		return keys[i].neighborAS < keys[j].neighborAS
	})
	for _, k := range keys {
		e := l.routes[k]
		delete(l.routes, k)
		if grace {
			sk := staleKey{
				peerKey:    peer.key(),
				prefix:     e.route.Prefix,
				generation: s.identity.generation(),
				routeID:    e.route.ID,
			}
			stale := &staleEntry{
				route:                 e.route,
				peer:                  peer,
				generation:            e.generation,
				originalPolicyVersion: e.policyVersion,
				originalVRPVersion:    e.vrpVersion,
				validatedPolicy:       l.policyVersion,
				validatedVRP:          l.vrpVersion,
				staleSince:            now,
				deadline:              now.Add(l.restartTime),
			}
			l.stale[sk] = stale
			l.emitLocked(Event{
				Type:          EventStaled,
				At:            now,
				RouteID:       e.route.ID,
				Prefix:        e.route.Prefix,
				AttrsDigest:   e.route.AttrsDigest,
				Peer:          peer,
				Generation:    e.generation,
				Deadline:      stale.deadline,
				PolicyVersion: l.policyVersion,
				VRPVersion:    l.vrpVersion,
			})
			continue
		}
		wr := WithdrawNoRestart
		if authFailed {
			wr = WithdrawAuthFailure
		}
		l.history = append(l.history, Withdrawal{
			RouteID:       e.route.ID,
			Prefix:        e.route.Prefix,
			AttrsDigest:   e.route.AttrsDigest,
			Peer:          peer,
			Generation:    e.generation,
			Reason:        wr,
			At:            now,
			PolicyVersion: e.policyVersion,
			VRPVersion:    e.vrpVersion,
		})
		l.emitLocked(Event{
			Type:          EventWithdrawn,
			At:            now,
			RouteID:       e.route.ID,
			Prefix:        e.route.Prefix,
			AttrsDigest:   e.route.AttrsDigest,
			Peer:          peer,
			Generation:    e.generation,
			Reason:        wr,
			PolicyVersion: e.policyVersion,
			VRPVersion:    e.vrpVersion,
		})
	}

	if authFailed {
		staleKeys := make([]staleKey, 0)
		for k := range l.stale {
			if k.peerKey == peer.key() {
				staleKeys = append(staleKeys, k)
			}
		}
		sortStaleKeys(staleKeys)
		for _, k := range staleKeys {
			l.removeStaleLocked(k, WithdrawAuthFailure, now, false)
		}
	}

	// A route may have been installed under an older policy/VRP and only
	// become stale now.  Never expose it for forwarding unless the current
	// revisions still accept it.
	l.revalidateStaleLocked(now)

	s.active = false
	l.clearSessionLocked(peer, s)
	return nil
}

// DisconnectAuthenticationFailure is a convenience wrapper.
func (s *Session) DisconnectAuthenticationFailure() error {
	return s.Disconnect(DisconnectAuthenticationFailure)
}

func parseDisconnectReason(args []any) DisconnectReason {
	reason := DisconnectOther
	set := func(r DisconnectReason) {
		if reason != DisconnectAuthenticationFailure {
			reason = r
		}
	}
	for _, arg := range args {
		switch v := arg.(type) {
		case DisconnectReason:
			set(v)
		case *DisconnectReason:
			if v != nil {
				set(*v)
			}
		case bool:
			if v {
				reason = DisconnectAuthenticationFailure
			}
		case string:
			s := strings.ToLower(v)
			switch {
			case strings.Contains(s, "auth"):
				reason = DisconnectAuthenticationFailure
			case strings.Contains(s, "hold"):
				set(DisconnectHoldTimerExpired)
			case strings.Contains(s, "admin"):
				set(DisconnectAdministrative)
			case strings.Contains(s, "notif"):
				set(DisconnectNotification)
			case strings.Contains(s, "transport") || strings.Contains(s, "tcp"):
				set(DisconnectTransport)
			}
		case error:
			if strings.Contains(strings.ToLower(v.Error()), "auth") {
				reason = DisconnectAuthenticationFailure
			}
		}
	}
	return reason
}

func parseRouteArgs(args []any) (Route, error) {
	var r Route
	var stringsSeen []string
	for _, arg := range args {
		switch v := arg.(type) {
		case Route:
			r = mergeRoute(r, v)
		case *Route:
			if v != nil {
				r = mergeRoute(r, *v)
			}
		case string:
			stringsSeen = append(stringsSeen, v)
		case []byte:
			r.AttrsDigest = fmt.Sprintf("%x", v)
		}
	}
	for i, v := range stringsSeen {
		switch {
		case i == 0 && r.ID == "" && !strings.Contains(v, "/"):
			r.ID = v
		case i == 0 && strings.Contains(v, "/") && r.Prefix == "":
			r.Prefix = v
		case i == 1 && r.Prefix == "":
			r.Prefix = v
		case i >= 1 && r.Prefix != "" && r.AttrsDigest == "":
			r.AttrsDigest = v
		}
	}
	r = r.normalized()
	if r.Prefix == "" {
		return Route{}, fmt.Errorf("%w: missing prefix", ErrInvalidRoute)
	}
	return r, nil
}

func mergeRoute(a, b Route) Route {
	an, bn := a.normalized(), b.normalized()
	if bn.ID != "" {
		an.ID = bn.ID
	}
	if bn.Prefix != "" {
		an.Prefix = bn.Prefix
	}
	if bn.AttrsDigest != "" {
		an.AttrsDigest = bn.AttrsDigest
	}
	return an.normalized()
}

func sortStaleKeys(keys []staleKey) {
	sort.Slice(keys, func(i, j int) bool { return staleKeyLess(keys[i], keys[j]) })
}
