package sessionrouter

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
)

// Router is the local session-routing state machine. All mutating
// operations are persisted write-through so a crash never loses a
// generation bump or an idempotent decision.
type Router struct {
	clock Clock
	store Store
	state *PersistedState
}

// NewRouter loads persisted state (if any) and enforces the generation
// high-water marks so recovery can never roll a generation back.
func NewRouter(store Store, clock Clock) (*Router, error) {
	st, err := store.Load()
	if err != nil {
		return nil, err
	}
	if st == nil {
		st = emptyState()
	}
	for id, s := range st.Sessions {
		if hw := st.GenHighWater[id]; s.Generation < hw {
			s.Generation = hw
		}
	}
	return &Router{clock: clock, store: store, state: st}, nil
}

func (r *Router) Now() Tick { return r.clock.Now() }

func (r *Router) ActiveCount() int {
	n := 0
	for _, s := range r.state.Sessions {
		if !s.Closed {
			n++
		}
	}
	return n
}

func (r *Router) Lookup(sid SessionID) (Session, bool) {
	s, ok := r.state.Sessions[sid]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

func (r *Router) Events() []Event {
	out := make([]Event, len(r.state.Events))
	copy(out, r.state.Events)
	return out
}

// AddNode registers a node as known and healthy.
func (r *Router) AddNode(n NodeID) { r.NodeUp(n) }

// NodeUp marks a node healthy. A recovered node does not get its old
// sessions back: they were migrated with a bumped generation, and any
// late reports it sends carry a stale generation and are rejected.
func (r *Router) NodeUp(n NodeID) {
	if r.state.Healthy[n] {
		return
	}
	r.state.Healthy[n] = true
	r.log("node-up", "", "node="+string(n))
	r.persist()
}

// NodeDown marks a node unhealthy and fails its active sessions over to
// the remaining healthy nodes, bumping the generation and issuing a new
// routing token for each. If no healthy node remains, the sessions are
// force-closed because they cannot be served anywhere.
func (r *Router) NodeDown(n NodeID) {
	if !r.state.Healthy[n] {
		return
	}
	r.state.Healthy[n] = false
	r.log("node-down", "", "node="+string(n))
	r.expireDue()
	healthy := sortedHealthy(r.state.Healthy)
	for _, s := range r.sortedSessions() {
		if s.Closed || s.Owner != n {
			continue
		}
		if len(healthy) == 0 {
			s.Closed = true
			s.CloseReason = "force-closed: no healthy node available"
			r.log("force-closed", s.ID, s.CloseReason)
			continue
		}
		s.Generation++
		s.Owner = pickOwner(s.ID, s.Generation, healthy)
		s.Token = tokenFor(s.ID, s.Generation, s.Owner)
		r.bumpHighWater(s.ID, s.Generation)
		r.log("migrated", s.ID, fmt.Sprintf("failed-node=%s new-owner=%s generation=%d", n, s.Owner, s.Generation))
	}
	r.persist()
}

// Create routes a new session to a healthy node chosen by deterministic
// hash. A closed session may be recreated, always with a higher
// generation than any it has ever had.
func (r *Router) Create(reqID RequestID, sid SessionID, ttl int64) Decision {
	return r.execute(reqID, fpCreate(sid, ttl), func() Decision {
		d := Decision{ReqID: reqID, Op: OpCreate, SessionID: sid}
		if ttl <= 0 {
			d.Code = CodeInvalidTTL
			d.Reason = fmt.Sprintf("rejected: ttl %d is not positive", ttl)
			return d
		}
		if s, ok := r.state.Sessions[sid]; ok && !s.Closed {
			d.Code = CodeSessionExists
			d.Reason = fmt.Sprintf("rejected: session already active on %s generation %d", s.Owner, s.Generation)
			return d
		}
		healthy := sortedHealthy(r.state.Healthy)
		if len(healthy) == 0 {
			d.Code = CodeNoHealthyNodes
			d.Reason = "rejected: no healthy nodes available"
			return d
		}
		gen := uint64(1)
		if s, ok := r.state.Sessions[sid]; ok {
			gen = s.Generation + 1
		}
		if hw := r.state.GenHighWater[sid]; gen <= hw {
			gen = hw + 1
		}
		owner := pickOwner(sid, gen, healthy)
		tok := tokenFor(sid, gen, owner)
		r.state.Sessions[sid] = &Session{
			ID:         sid,
			Generation: gen,
			Owner:      owner,
			Token:      tok,
			ExpiresAt:  r.clock.Now() + Tick(ttl),
		}
		r.bumpHighWater(sid, gen)
		r.log("created", sid, fmt.Sprintf("owner=%s generation=%d ttl=%d", owner, gen, ttl))
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = gen
		d.Owner = owner
		d.Token = tok
		d.Reason = fmt.Sprintf("created on %s via deterministic hash over %d healthy node(s), generation %d", owner, len(healthy), gen)
		return d
	})
}

// Renew extends a session's TTL. It must carry the current generation;
// anything older (e.g. from a recovered node) is rejected.
func (r *Router) Renew(reqID RequestID, sid SessionID, generation uint64, ttl int64) Decision {
	return r.execute(reqID, fpRenew(sid, generation, ttl), func() Decision {
		d := Decision{ReqID: reqID, Op: OpRenew, SessionID: sid, Generation: generation}
		if ttl <= 0 {
			d.Code = CodeInvalidTTL
			d.Reason = fmt.Sprintf("rejected: ttl %d is not positive", ttl)
			return d
		}
		s, ok := r.state.Sessions[sid]
		if !ok {
			d.Code = CodeUnknownSession
			d.Reason = "rejected: unknown session"
			return d
		}
		if s.Closed {
			d.Code = CodeSessionClosed
			d.Reason = fmt.Sprintf("rejected: session closed (%s)", s.CloseReason)
			return d
		}
		if generation != s.Generation {
			d.Code = CodeStaleGeneration
			d.Reason = fmt.Sprintf("rejected: stale generation %d, current is %d", generation, s.Generation)
			return d
		}
		s.ExpiresAt = r.clock.Now() + Tick(ttl)
		r.log("renewed", sid, fmt.Sprintf("generation=%d new-expiry=%d", generation, s.ExpiresAt))
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = s.Generation
		d.Owner = s.Owner
		d.Token = s.Token
		d.Reason = fmt.Sprintf("renewed on %s, expires at tick %d", s.Owner, s.ExpiresAt)
		return d
	})
}

// Complete closes a session. Stale-generation completions are rejected.
func (r *Router) Complete(reqID RequestID, sid SessionID, generation uint64) Decision {
	return r.execute(reqID, fpComplete(sid, generation), func() Decision {
		d := Decision{ReqID: reqID, Op: OpComplete, SessionID: sid, Generation: generation}
		s, ok := r.state.Sessions[sid]
		if !ok {
			d.Code = CodeUnknownSession
			d.Reason = "rejected: unknown session"
			return d
		}
		if s.Closed {
			d.Code = CodeSessionClosed
			d.Reason = fmt.Sprintf("rejected: session already closed (%s)", s.CloseReason)
			return d
		}
		if generation != s.Generation {
			d.Code = CodeStaleGeneration
			d.Reason = fmt.Sprintf("rejected: stale generation %d, current is %d", generation, s.Generation)
			return d
		}
		s.Closed = true
		s.CloseReason = "completed"
		r.log("completed", sid, fmt.Sprintf("generation=%d", generation))
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = s.Generation
		d.Owner = s.Owner
		d.Token = s.Token
		d.Reason = "completed and closed"
		return d
	})
}

// Writeback accepts a state writeback only for the current generation.
func (r *Router) Writeback(reqID RequestID, sid SessionID, generation uint64, data string) Decision {
	return r.execute(reqID, fpWriteback(sid, generation, data), func() Decision {
		d := Decision{ReqID: reqID, Op: OpWriteback, SessionID: sid, Generation: generation}
		s, ok := r.state.Sessions[sid]
		if !ok {
			d.Code = CodeUnknownSession
			d.Reason = "rejected: unknown session"
			return d
		}
		if s.Closed {
			d.Code = CodeSessionClosed
			d.Reason = fmt.Sprintf("rejected: session closed (%s)", s.CloseReason)
			return d
		}
		if generation != s.Generation {
			d.Code = CodeStaleGeneration
			d.Reason = fmt.Sprintf("rejected: stale generation %d, current is %d", generation, s.Generation)
			return d
		}
		s.Writebacks = append(s.Writebacks, data)
		r.log("writeback", sid, fmt.Sprintf("generation=%d bytes=%d", generation, len(data)))
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = s.Generation
		d.Owner = s.Owner
		d.Token = s.Token
		d.Reason = "writeback accepted"
		return d
	})
}

// Migrate moves a session to a (possibly different) healthy node,
// incrementing the generation and issuing a new routing token. Repeating
// the same request ID returns the same decision without migrating again.
func (r *Router) Migrate(reqID RequestID, sid SessionID) Decision {
	return r.execute(reqID, fpMigrate(sid), func() Decision {
		d := Decision{ReqID: reqID, Op: OpMigrate, SessionID: sid}
		s, ok := r.state.Sessions[sid]
		if !ok {
			d.Code = CodeUnknownSession
			d.Reason = "rejected: unknown session"
			return d
		}
		if s.Closed {
			d.Code = CodeSessionClosed
			d.Reason = fmt.Sprintf("rejected: session closed (%s)", s.CloseReason)
			return d
		}
		healthy := sortedHealthy(r.state.Healthy)
		if len(healthy) == 0 {
			d.Code = CodeNoHealthyNodes
			d.Reason = "rejected: no healthy nodes available for migration"
			return d
		}
		s.Generation++
		s.Owner = pickOwner(s.ID, s.Generation, healthy)
		s.Token = tokenFor(s.ID, s.Generation, s.Owner)
		r.bumpHighWater(s.ID, s.Generation)
		r.log("migrated", sid, fmt.Sprintf("new-owner=%s generation=%d", s.Owner, s.Generation))
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = s.Generation
		d.Owner = s.Owner
		d.Token = s.Token
		d.Reason = fmt.Sprintf("migrated to %s with generation %d and new routing token", s.Owner, s.Generation)
		return d
	})
}

// execute is the shared pipeline: expire sessions due at the current tick
// before handling the request, serve idempotent replays, detect request-ID
// conflicts, run the operation, then persist state plus the decision.
func (r *Router) execute(reqID RequestID, fingerprint string, op func() Decision) Decision {
	r.expireDue()
	if rec, ok := r.state.Idem[reqID]; ok {
		if rec.Fingerprint != fingerprint {
			return Decision{
				ReqID:          reqID,
				Allowed:        false,
				Code:           CodeConflict,
				Reason:         "rejected: request ID reused with different parameters",
				ActiveSessions: r.ActiveCount(),
			}
		}
		d := rec.Decision
		d.Replayed = true
		return d
	}
	d := op()
	d.ActiveSessions = r.ActiveCount()
	r.state.Idem[reqID] = IdemRecord{Fingerprint: fingerprint, Decision: d}
	r.persist()
	return d
}

// expireDue closes every active session whose expiry is at or before the
// current tick. It runs before request handling so that an expiry and a
// request on the same tick are ordered expiry-first.
func (r *Router) expireDue() {
	now := r.clock.Now()
	for _, s := range r.sortedSessions() {
		if !s.Closed && s.ExpiresAt <= now {
			s.Closed = true
			s.CloseReason = "expired"
			r.log("expired", s.ID, fmt.Sprintf("expired-at=%d tick=%d", s.ExpiresAt, now))
		}
	}
}

func (r *Router) bumpHighWater(sid SessionID, gen uint64) {
	if r.state.GenHighWater[sid] < gen {
		r.state.GenHighWater[sid] = gen
	}
}

func (r *Router) log(typ string, sid SessionID, detail string) {
	r.state.Events = append(r.state.Events, Event{
		Tick:      r.clock.Now(),
		Type:      typ,
		SessionID: sid,
		Detail:    detail,
	})
}

func (r *Router) persist() {
	r.state.Tick = r.clock.Now()
	// In this single-process state machine the store is written
	// synchronously; a failure is fatal to consistency, so panic.
	if err := r.store.Save(r.state); err != nil {
		panic(fmt.Sprintf("sessionrouter: persist failed: %v", err))
	}
}

func (r *Router) sortedSessions() []*Session {
	out := make([]*Session, 0, len(r.state.Sessions))
	for _, s := range r.state.Sessions {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// --- deterministic placement spec (shared with the reference model) ---

func hashKey(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

func pickOwner(sid SessionID, generation uint64, healthy []NodeID) NodeID {
	if len(healthy) == 0 {
		return ""
	}
	idx := hashKey(string(sid), strconv.FormatUint(generation, 10)) % uint64(len(healthy))
	return healthy[idx]
}

func tokenFor(sid SessionID, generation uint64, owner NodeID) string {
	sum := hashKey(string(sid), strconv.FormatUint(generation, 10), string(owner))
	return fmt.Sprintf("rt-%s-%d-%016x", owner, generation, sum)
}

func sortedHealthy(healthy map[NodeID]bool) []NodeID {
	out := make([]NodeID, 0, len(healthy))
	for n, ok := range healthy {
		if ok {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// --- request fingerprints (shared with the reference model) ---

func fpCreate(sid SessionID, ttl int64) string {
	return fmt.Sprintf("create|%s|%d", sid, ttl)
}

func fpRenew(sid SessionID, generation uint64, ttl int64) string {
	return fmt.Sprintf("renew|%s|%d|%d", sid, generation, ttl)
}

func fpComplete(sid SessionID, generation uint64) string {
	return fmt.Sprintf("complete|%s|%d", sid, generation)
}

func fpWriteback(sid SessionID, generation uint64, data string) string {
	return fmt.Sprintf("writeback|%s|%d|%s", sid, generation, data)
}

func fpMigrate(sid SessionID) string {
	return fmt.Sprintf("migrate|%s", sid)
}
