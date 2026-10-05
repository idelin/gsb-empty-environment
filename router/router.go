package router

import (
	"fmt"
	"hash/fnv"
	"sort"
)

// Router is the local session routing state machine. It is not safe for
// concurrent use; callers serialize access (the model is single-threaded).
type Router struct {
	clock    Clock
	store    *Store // nil means in-memory only
	nodes    map[string]bool
	sessions map[string]*Session
	requests map[string]RequestRecord
	events   []Event
}

// NewRouter creates an empty router. store may be nil for a volatile router.
func NewRouter(clock Clock, store *Store) *Router {
	return &Router{
		clock:    clock,
		store:    store,
		nodes:    make(map[string]bool),
		sessions: make(map[string]*Session),
		requests: make(map[string]RequestRecord),
	}
}

// RecoverRouter rebuilds a router by replaying the store's WAL in order.
// Because generation bumps are logged before being acknowledged, replay can
// never roll a generation back.
func RecoverRouter(clock Clock, store *Store) *Router {
	r := NewRouter(clock, store)
	for _, rec := range store.records {
		switch rec.Kind {
		case recNode:
			r.nodes[rec.Node] = rec.Healthy
		case recSession:
			s := *rec.Session
			r.sessions[s.ID] = &s
		case recRequest:
			r.requests[rec.Request.RequestID] = *rec.Request
		}
	}
	return r
}

// --- helpers ---------------------------------------------------------------

func (r *Router) logEvent(kind, node, session string, gen uint64, detail string) {
	r.events = append(r.events, Event{
		Tick:       r.clock.Now(),
		Kind:       kind,
		Node:       node,
		Session:    session,
		Generation: gen,
		Detail:     detail,
	})
}

// Events returns a copy of the event log.
func (r *Router) Events() []Event {
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

func (r *Router) persist(rec record) {
	if r.store != nil {
		// A store that cannot fsync cannot guarantee durability; panic so the
		// failure is loud rather than silently losing acknowledged state.
		if err := r.store.append(rec); err != nil {
			panic(fmt.Sprintf("router: WAL append failed: %v", err))
		}
	}
}

func (r *Router) persistSession(s *Session) {
	cp := *s
	r.persist(record{Kind: recSession, Session: &cp})
}

func (r *Router) persistNode(id string, healthy bool) {
	r.persist(record{Kind: recNode, Node: id, Healthy: healthy})
}

func (r *Router) persistRequest(rec RequestRecord) {
	r.persist(record{Kind: recRequest, Request: &rec})
}

// healthyNodes returns healthy node IDs in sorted order so that hashing is
// deterministic regardless of map iteration order.
func (r *Router) healthyNodes() []string {
	var out []string
	for id, ok := range r.nodes {
		if ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// pickOwner deterministically selects a healthy node for (session, generation).
func pickOwner(sessionID string, generation uint64, healthy []string) string {
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("%s#%d", sessionID, generation)))
	return healthy[h.Sum64()%uint64(len(healthy))]
}

func routeToken(sessionID string, generation uint64, owner string) string {
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("tok|%s|%d|%s", sessionID, generation, owner)))
	return fmt.Sprintf("rt-%016x", h.Sum64())
}

// activeCount counts sessions in StateActive.
func (r *Router) activeCount() int {
	n := 0
	for _, s := range r.sessions {
		if s.State == StateActive {
			n++
		}
	}
	return n
}

// ActiveCount reports the number of currently active sessions.
func (r *Router) ActiveCount() int {
	r.expire(r.clock.Now())
	return r.activeCount()
}

// Session returns a copy of the named session, if known.
func (r *Router) Session(id string) (Session, bool) {
	r.expire(r.clock.Now())
	s, ok := r.sessions[id]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

// expire transitions active sessions whose deadline has passed. It runs
// before any request at the same tick, so expiry always wins a same-tick
// race against renewal.
func (r *Router) expire(now uint64) {
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := r.sessions[id]
		if s.State == StateActive && now >= s.ExpiresAt {
			s.State = StateExpired
			r.persistSession(s)
			r.logEvent("expired", s.Owner, s.ID, s.Generation,
				fmt.Sprintf("deadline %d reached at tick %d", s.ExpiresAt, now))
		}
	}
}

// Tick processes expirations at the current clock reading.
func (r *Router) Tick() { r.expire(r.clock.Now()) }

// checkRequest implements idempotency: a seen request ID replays its stored
// decision; the same ID bound to a different session is a conflict.
func (r *Router) checkRequest(reqID, sessionID string) (Decision, bool) {
	rec, ok := r.requests[reqID]
	if !ok {
		return Decision{}, false
	}
	if rec.SessionID != sessionID {
		return Decision{
			Accepted:       false,
			Conflict:       true,
			Reason:         fmt.Sprintf("request %s already bound to session %s", reqID, rec.SessionID),
			ActiveSessions: r.activeCount(),
		}, true
	}
	return rec.Decision, true
}

func (r *Router) commit(reqID, sessionID string, d Decision) Decision {
	d.ActiveSessions = r.activeCount()
	rec := RequestRecord{RequestID: reqID, SessionID: sessionID, Decision: d}
	r.persistRequest(rec)
	r.requests[reqID] = rec
	return d
}

// --- node membership -------------------------------------------------------

// AddNode registers a node as healthy.
func (r *Router) AddNode(id string) {
	if _, ok := r.nodes[id]; ok {
		return
	}
	r.nodes[id] = true
	r.persistNode(id, true)
	r.logEvent("node_added", id, "", 0, "")
}

// NodeFailed marks a node unhealthy and migrates its active sessions,
// bumping each migrated session's generation and issuing a new route token.
// Re-failing an already failed node is a no-op (duplicate migration safe).
func (r *Router) NodeFailed(id string) {
	healthy, known := r.nodes[id]
	if !known || !healthy {
		r.logEvent("node_failure_ignored", id, "", 0, "node unknown or already failed")
		return
	}
	r.nodes[id] = false
	r.persistNode(id, false)
	r.logEvent("node_failed", id, "", 0, "")

	ids := make([]string, 0, len(r.sessions))
	for sid := range r.sessions {
		ids = append(ids, sid)
	}
	sort.Strings(ids)
	for _, sid := range ids {
		s := r.sessions[sid]
		if s.State != StateActive || s.Owner != id {
			continue
		}
		healthy := r.healthyNodes()
		if len(healthy) == 0 {
			r.logEvent("migration_deferred", id, s.ID, s.Generation, "no healthy nodes")
			continue
		}
		s.Generation++
		s.Owner = pickOwner(s.ID, s.Generation, healthy)
		s.Token = routeToken(s.ID, s.Generation, s.Owner)
		r.persistSession(s)
		r.logEvent("migrated", s.Owner, s.ID, s.Generation,
			fmt.Sprintf("from failed node %s", id))
	}
}

// NodeRecovered marks a node healthy again. Sessions are NOT moved back:
// anything the recovered node still serves carries a stale generation and
// will be rejected on renew/complete/writeback.
func (r *Router) NodeRecovered(id string) {
	healthy, known := r.nodes[id]
	if !known || healthy {
		r.logEvent("node_recovery_ignored", id, "", 0, "node unknown or already healthy")
		return
	}
	r.nodes[id] = true
	r.persistNode(id, true)
	r.logEvent("node_recovered", id, "", 0, "")
}

// --- requests --------------------------------------------------------------

// Create routes a new session. If the sessionID was previously closed or
// expired, it is rebuilt with a strictly higher generation.
func (r *Router) Create(reqID, sessionID string, ttl uint64) Decision {
	r.expire(r.clock.Now())
	if d, ok := r.checkRequest(reqID, sessionID); ok {
		return d
	}
	if ttl == 0 {
		return r.commit(reqID, sessionID, Decision{Reason: "ttl must be positive"})
	}
	if s, ok := r.sessions[sessionID]; ok && s.State == StateActive {
		return r.commit(reqID, sessionID, Decision{
			Reason:    "session already active",
			SessionID: sessionID,
		})
	}
	healthy := r.healthyNodes()
	if len(healthy) == 0 {
		return r.commit(reqID, sessionID, Decision{
			Reason:    "no healthy nodes",
			SessionID: sessionID,
		})
	}
	var gen uint64 = 1
	if prev, ok := r.sessions[sessionID]; ok {
		gen = prev.Generation + 1 // rebuild after close/expiry: higher generation
	}
	owner := pickOwner(sessionID, gen, healthy)
	s := &Session{
		ID:         sessionID,
		Generation: gen,
		Owner:      owner,
		ExpiresAt:  r.clock.Now() + ttl,
		State:      StateActive,
		Token:      routeToken(sessionID, gen, owner),
	}
	r.persistSession(s)
	r.sessions[sessionID] = s
	r.logEvent("created", owner, sessionID, gen, fmt.Sprintf("ttl=%d", ttl))
	return r.commit(reqID, sessionID, Decision{
		Accepted:   true,
		Reason:     "created",
		SessionID:  sessionID,
		Generation: gen,
		Owner:      owner,
		Token:      s.Token,
	})
}

// validateActive checks the common preconditions of renew/complete/writeback.
func (r *Router) validateActive(sessionID string, gen uint64) (Decision, *Session, bool) {
	s, ok := r.sessions[sessionID]
	if !ok || s.State != StateActive {
		return Decision{Reason: "no active session", SessionID: sessionID}, nil, false
	}
	if gen != s.Generation {
		return Decision{
			Reason:     fmt.Sprintf("stale generation: got %d, current %d", gen, s.Generation),
			SessionID:  sessionID,
			Generation: s.Generation,
		}, nil, false
	}
	if healthy, known := r.nodes[s.Owner]; !known || !healthy {
		return Decision{
			Reason:     "owner node unhealthy",
			SessionID:  sessionID,
			Generation: s.Generation,
		}, nil, false
	}
	return Decision{}, s, true
}

// Renew extends a session's deadline. It must carry the current generation.
func (r *Router) Renew(reqID, sessionID string, gen uint64, ttl uint64) Decision {
	r.expire(r.clock.Now())
	if d, ok := r.checkRequest(reqID, sessionID); ok {
		return d
	}
	if ttl == 0 {
		return r.commit(reqID, sessionID, Decision{Reason: "ttl must be positive", SessionID: sessionID})
	}
	rej, s, ok := r.validateActive(sessionID, gen)
	if !ok {
		return r.commit(reqID, sessionID, rej)
	}
	s.ExpiresAt = r.clock.Now() + ttl
	r.persistSession(s)
	r.logEvent("renewed", s.Owner, sessionID, gen, fmt.Sprintf("new deadline %d", s.ExpiresAt))
	return r.commit(reqID, sessionID, Decision{
		Accepted:   true,
		Reason:     "renewed",
		SessionID:  sessionID,
		Generation: gen,
		Owner:      s.Owner,
		Token:      s.Token,
	})
}

// Complete is the owner's completion notification; it closes the session.
func (r *Router) Complete(reqID, sessionID string, gen uint64) Decision {
	r.expire(r.clock.Now())
	if d, ok := r.checkRequest(reqID, sessionID); ok {
		return d
	}
	rej, s, ok := r.validateActive(sessionID, gen)
	if !ok {
		return r.commit(reqID, sessionID, rej)
	}
	s.State = StateClosed
	r.persistSession(s)
	r.logEvent("completed", s.Owner, sessionID, gen, "")
	return r.commit(reqID, sessionID, Decision{
		Accepted:   true,
		Reason:     "completed",
		SessionID:  sessionID,
		Generation: gen,
		Owner:      s.Owner,
		Token:      s.Token,
	})
}

// Writeback records an owner writeback for the session.
func (r *Router) Writeback(reqID, sessionID string, gen uint64, data string) Decision {
	r.expire(r.clock.Now())
	if d, ok := r.checkRequest(reqID, sessionID); ok {
		return d
	}
	rej, s, ok := r.validateActive(sessionID, gen)
	if !ok {
		return r.commit(reqID, sessionID, rej)
	}
	s.Writebacks = append(s.Writebacks, data)
	r.persistSession(s)
	r.logEvent("writeback", s.Owner, sessionID, gen, fmt.Sprintf("%d bytes", len(data)))
	return r.commit(reqID, sessionID, Decision{
		Accepted:   true,
		Reason:     "writeback recorded",
		SessionID:  sessionID,
		Generation: gen,
		Owner:      s.Owner,
		Token:      s.Token,
	})
}
