package sessiontakeover

import (
	"context"
	"sync"
	"time"
)

type sessionKey struct {
	tenant   string
	clientID string
	subject  string
}

type clientKey struct {
	tenant   string
	clientID string
}

type ownerEntry struct {
	subject string
	session *Session
}

// Registry owns all MQTT sessions for a broker. Sessions are keyed by
// (tenant, client ID, subject); within a tenant a client ID can be owned by at
// most one subject, and ownership can never be silently overwritten.
//
// Registry is safe for concurrent use; concurrent Connect calls on the same
// client ID are serialised so exactly one takeover wins.
type Registry struct {
	mu       sync.Mutex
	sessions map[sessionKey]*Session
	owners   map[clientKey]*ownerEntry

	conflict   ConflictPolicy
	defaultTTL time.Duration
	maxOffline int
	inboundWin int
	clock      Clock
	inspector  CredentialInspector
	closed     bool
	stopReaper chan struct{}
	reaperDone chan struct{}
}

// NewRegistry builds a Registry from cfg.
func NewRegistry(cfg Config) *Registry {
	clock := cfg.Clock
	if clock == nil {
		clock = wallClock{}
	}
	inspector := cfg.Inspector
	if inspector == nil {
		inspector = AllowAllInspector{}
	}
	r := &Registry{
		sessions:   make(map[sessionKey]*Session),
		owners:     make(map[clientKey]*ownerEntry),
		conflict:   cfg.ConflictPolicy,
		defaultTTL: cfg.DefaultSessionExpiry,
		maxOffline: cfg.MaxOfflineMessages,
		inboundWin: cfg.InboundQoS2Window,
		clock:      clock,
		inspector:  inspector,
		stopReaper: make(chan struct{}),
	}
	if cfg.ReapInterval > 0 {
		r.reaperDone = make(chan struct{})
		go r.reapLoop(cfg.ReapInterval)
	}
	return r
}

// Close stops the background reaper and marks the registry unusable. Already
// issued handles remain usable until displaced or disconnected.
func (r *Registry) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRegistryClosed
	}
	r.closed = true
	reaperDone := r.reaperDone
	close(r.stopReaper)
	r.mu.Unlock()
	if reaperDone != nil {
		<-reaperDone
	}
	return nil
}

// Connect processes an incoming MQTT CONNECT and returns the connection
// handle together with a decision record. On any rejection the handle is nil
// and the error wraps ErrTakeoverRejected (or a more specific sentinel).
func (r *Registry) Connect(ctx context.Context, req ConnectRequest) (*Handle, ConnectResult, error) {
	if req.ClientID == "" || req.Presented.Identity.Subject == "" {
		return nil, ConnectResult{Outcome: OutcomeRejected, Reason: ReasonInvalidRequest}, ErrInvalidRequest
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ConnectResult{Outcome: OutcomeRejected}, ErrRegistryClosed
	}

	now := r.clock.Now()
	ck := clientKey{tenant: req.Tenant, clientID: req.ClientID}
	sk := sessionKey{tenant: req.Tenant, clientID: req.ClientID, subject: req.Presented.Identity.Subject}

	// Ownership check: another authenticated subject already owns this
	// client ID inside the tenant. Never overwrite it silently.
	isolated := false
	if owner, ok := r.owners[ck]; ok && owner.subject != req.Presented.Identity.Subject {
		// If the recorded owner no longer has any session (expired or
		// terminated), the client ID is free again: ownership transfers
		// instead of being treated as a conflict.
		ownerGone := false
		if owner.session != nil {
			owner.session.mu.RLock()
			ownerGone = owner.session.closed
			owner.session.mu.RUnlock()
		} else {
			ownerGone = true
		}
		if ownerGone {
			delete(r.owners, ck)
		} else {
			if r.conflict == ConflictReject {
				r.mu.Unlock()
				return nil, ConnectResult{Outcome: OutcomeRejected, Reason: ReasonDifferentPrincipal}, ErrTakeoverRejected
			}
			// Isolate: fall through to a subject-scoped session and leave
			// the original owner entry untouched.
			isolated = true
		}
	}
	if !isolated {
		// No live conflicting owner: the connecting subject claims ownership.
		if _, ok := r.owners[ck]; !ok {
			r.owners[ck] = &ownerEntry{subject: req.Presented.Identity.Subject}
		}
	}

	// Revocation check applies to every (re)connection, including resumes.
	revoked, err := r.inspector.IsRevoked(ctx, req.Presented)
	if err != nil {
		r.mu.Unlock()
		return nil, ConnectResult{Outcome: OutcomeRejected, Reason: ReasonInspectorUnavailable}, ErrTakeoverRejected
	}
	if revoked {
		r.mu.Unlock()
		return nil, ConnectResult{Outcome: OutcomeRejected, Reason: ReasonCredentialRevoked}, ErrCredentialRevoked
	}

	existing, sessionExists := r.sessions[sk]
	createReason := ReasonNone
	if sessionExists {
		if existing.isExpired(now) {
			r.deleteSessionLocked(sk, existing, DisplaceExpired)
			sessionExists = false
			createReason = ReasonSessionExpired
		} else if req.CleanStart {
			// Clean start discards retained state but keeps ownership.
			r.deleteSessionLocked(sk, existing, DisplaceTerminated)
			sessionExists = false
			createReason = ReasonCleanStart
		}
	}

	var (
		handle  *Handle
		outcome Outcome
		reason  Reason
		present bool
	)

	if sessionExists {
		handle = r.resumeLocked(existing, req, now)
		if owner, ok := r.owners[ck]; ok && owner.subject == sk.subject && owner.session == nil {
			owner.session = existing
		}
		outcome = OutcomeResumed
		present = true
	} else {
		session := r.newSessionLocked(sk, req, now)
		r.sessions[sk] = session
		if !isolated {
			// Record the session on the owner entry for clean removal later.
			if owner, ok := r.owners[ck]; ok && owner.session == nil {
				owner.session = session
			}
		}
		handle = r.bindLocked(session)
		if isolated {
			outcome = OutcomeIsolated
			reason = ReasonDifferentPrincipal
		} else {
			outcome = OutcomeCreated
			reason = createReason
		}
	}

	result := ConnectResult{
		Outcome:        outcome,
		Reason:         reason,
		SessionPresent: present,
		Generation:     handle.generation,
	}
	r.mu.Unlock()
	return handle, result, nil
}

func (r *Registry) newSessionLocked(sk sessionKey, req ConnectRequest, now time.Time) *Session {
	expiry := r.defaultTTL
	if req.Expiry > 0 {
		expiry = req.Expiry
	}
	var expiresAt time.Time
	if expiry > 0 {
		expiresAt = now.Add(expiry)
	}
	return &Session{
		tenant:     sk.tenant,
		clientID:   sk.clientID,
		subject:    sk.subject,
		generation: 0,
		subs:       make(map[string]Subscription),
		offline:    make([]queuedItem, 0),
		outbound:   newOutboundLedger(),
		inbound:    newInboundLedger(r.inboundWin),
		will:       cloneWill(req.Will),
		expiresAt:  expiresAt,
		createdAt:  now,
		lastSeen:   now,
		active:     false,
		maxOffline: r.maxOffline,
	}
}

// resumeLocked migrates an existing session onto a new connection generation:
// the old handle is invalidated, generation increments, will/subscriptions/
// ledgers/queue all stay attached to the same session object.
func (r *Registry) resumeLocked(s *Session, req ConnectRequest, now time.Time) *Handle {
	s.mu.Lock()
	old := s.boundHandle
	s.generation++
	s.active = true
	s.lastSeen = now
	if req.Will != nil {
		s.will = cloneWill(req.Will)
	}
	// Refresh expiry according to the new CONNECT, if requested.
	if req.Expiry > 0 {
		s.expiresAt = now.Add(req.Expiry)
	}
	handle := &Handle{session: s, generation: s.generation}
	s.boundHandle = handle
	s.mu.Unlock()

	if old != nil {
		old.closed.Store(true)
		if old.OnDisplaced != nil {
			old.OnDisplaced(DisplaceTakeover)
		}
	}
	return handle
}

func (r *Registry) bindLocked(s *Session) *Handle {
	s.mu.Lock()
	old := s.boundHandle
	s.generation++
	s.active = true
	handle := &Handle{session: s, generation: s.generation}
	s.boundHandle = handle
	s.mu.Unlock()

	if old != nil {
		old.closed.Store(true)
		if old.OnDisplaced != nil {
			old.OnDisplaced(DisplaceTakeover)
		}
	}
	return handle
}

// deleteSessionLocked removes a session and invalidates its handle. The
// owner entry is cleared only when it still points at the removed session.
func (r *Registry) deleteSessionLocked(sk sessionKey, s *Session, why DisplaceReason) {
	delete(r.sessions, sk)
	ck := clientKey{tenant: sk.tenant, clientID: sk.clientID}
	if owner, ok := r.owners[ck]; ok && owner.session == s {
		owner.session = nil
	}
	s.mu.Lock()
	old := s.boundHandle
	s.closed = true
	s.active = false
	s.boundHandle = nil
	s.mu.Unlock()
	if old != nil {
		old.closed.Store(true)
		if old.OnDisplaced != nil {
			old.OnDisplaced(why)
		}
	}
}

// Offline queues a message for a disconnected session without requiring a
// live handle. It returns false when the session is unknown, closed or
// expired; cross-tenant callers never see each other's sessions.
func (r *Registry) Offline(tenant, clientID, subject string, msg Message) bool {
	sk := sessionKey{tenant: tenant, clientID: clientID, subject: subject}
	r.mu.Lock()
	s, ok := r.sessions[sk]
	if !ok || s.isExpired(r.clock.Now()) {
		r.mu.Unlock()
		return false
	}
	now := r.clock.Now()
	r.mu.Unlock()
	return s.EnqueueOffline(msg, now)
}

// Lookup returns the session for an exact identity triple. It never returns a
// session owned by another tenant or subject.
func (r *Registry) Lookup(tenant, clientID, subject string) (*Session, bool) {
	sk := sessionKey{tenant: tenant, clientID: clientID, subject: subject}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[sk]
	if !ok || s.isExpired(r.clock.Now()) {
		return nil, false
	}
	return s, true
}

// Sweep removes all expired sessions and returns how many were deleted.
func (r *Registry) Sweep() int {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return 0
	}
	now := r.clock.Now()
	type doomed struct {
		key sessionKey
		s   *Session
	}
	var victims []doomed
	for key, s := range r.sessions {
		if s.isExpired(now) {
			victims = append(victims, doomed{key, s})
		}
	}
	for _, v := range victims {
		r.deleteSessionLocked(v.key, v.s, DisplaceExpired)
	}
	r.mu.Unlock()
	return len(victims)
}

// TerminateSession permanently closes and removes the session owned by
// (tenant, clientID, subject). It returns true when a session was removed.
// A live handle for the session is invalidated with DisplaceTerminated.
func (r *Registry) TerminateSession(tenant, clientID, subject string) bool {
	sk := sessionKey{tenant: tenant, clientID: clientID, subject: subject}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	s, ok := r.sessions[sk]
	if !ok {
		return false
	}
	r.deleteSessionLocked(sk, s, DisplaceTerminated)
	return true
}

// Stats returns a point-in-time occupancy snapshot.
func (r *Registry) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	active := 0
	for _, s := range r.sessions {
		if s.Active() {
			active++
		}
	}
	return Stats{Sessions: len(r.sessions), ActiveSessions: active}
}

func (r *Registry) reapLoop(interval time.Duration) {
	defer close(r.reaperDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopReaper:
			return
		case <-ticker.C:
			r.Sweep()
		}
	}
}
