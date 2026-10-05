package router

// This file holds an INDEPENDENT serial reference model of the routing
// spec, plus a randomized cross-check that runs identical operation
// sequences against the real Router and the model and requires every
// decision to match exactly.

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"testing"
)

// --- serial reference model -------------------------------------------------
// Naive by design: slices with linear scans, one op at a time, no WAL.

type modelSession struct {
	id         string
	generation uint64
	owner      string
	expiresAt  uint64
	state      SessionState
	token      string
}

type modelRequest struct {
	reqID     string
	sessionID string
	decision  Decision
}

type model struct {
	now      uint64
	nodes    map[string]bool
	sessions []*modelSession
	requests []modelRequest
}

func newModel() *model { return &model{nodes: make(map[string]bool)} }

func (m *model) activeCount() int {
	n := 0
	for _, s := range m.sessions {
		if s.state == StateActive {
			n++
		}
	}
	return n
}

func (m *model) find(id string) *modelSession {
	for _, s := range m.sessions {
		if s.id == id {
			return s
		}
	}
	return nil
}

func (m *model) expire() {
	for _, s := range m.sessions {
		if s.state == StateActive && m.now >= s.expiresAt {
			s.state = StateExpired
		}
	}
}

func (m *model) healthy() []string {
	var out []string
	for id, ok := range m.nodes {
		if ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func modelPick(sessionID string, gen uint64, healthy []string) string {
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("%s#%d", sessionID, gen)))
	return healthy[h.Sum64()%uint64(len(healthy))]
}

func modelToken(sessionID string, gen uint64, owner string) string {
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("tok|%s|%d|%s", sessionID, gen, owner)))
	return fmt.Sprintf("rt-%016x", h.Sum64())
}

func (m *model) checkRequest(reqID, sessionID string) (Decision, bool) {
	for _, r := range m.requests {
		if r.reqID == reqID {
			if r.sessionID != sessionID {
				return Decision{
					Accepted:       false,
					Conflict:       true,
					Reason:         fmt.Sprintf("request %s already bound to session %s", reqID, r.sessionID),
					ActiveSessions: m.activeCount(),
				}, true
			}
			return r.decision, true
		}
	}
	return Decision{}, false
}

func (m *model) commit(reqID, sessionID string, d Decision) Decision {
	d.ActiveSessions = m.activeCount()
	m.requests = append(m.requests, modelRequest{reqID, sessionID, d})
	return d
}

func (m *model) addNode(id string) {
	if _, ok := m.nodes[id]; !ok {
		m.nodes[id] = true
	}
}

func (m *model) nodeFailed(id string) {
	healthy, known := m.nodes[id]
	if !known || !healthy {
		return
	}
	m.nodes[id] = false
	var owned []*modelSession
	for _, s := range m.sessions {
		if s.state == StateActive && s.owner == id {
			owned = append(owned, s)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].id < owned[j].id })
	for _, s := range owned {
		h := m.healthy()
		if len(h) == 0 {
			continue
		}
		s.generation++
		s.owner = modelPick(s.id, s.generation, h)
		s.token = modelToken(s.id, s.generation, s.owner)
	}
}

func (m *model) nodeRecovered(id string) {
	healthy, known := m.nodes[id]
	if !known || healthy {
		return
	}
	m.nodes[id] = true
}

func (m *model) create(reqID, sessionID string, ttl uint64) Decision {
	m.expire()
	if d, ok := m.checkRequest(reqID, sessionID); ok {
		return d
	}
	if ttl == 0 {
		return m.commit(reqID, sessionID, Decision{Reason: "ttl must be positive"})
	}
	if s := m.find(sessionID); s != nil && s.state == StateActive {
		return m.commit(reqID, sessionID, Decision{Reason: "session already active", SessionID: sessionID})
	}
	h := m.healthy()
	if len(h) == 0 {
		return m.commit(reqID, sessionID, Decision{Reason: "no healthy nodes", SessionID: sessionID})
	}
	var gen uint64 = 1
	if prev := m.find(sessionID); prev != nil {
		gen = prev.generation + 1
	}
	owner := modelPick(sessionID, gen, h)
	tok := modelToken(sessionID, gen, owner)
	s := &modelSession{sessionID, gen, owner, m.now + ttl, StateActive, tok}
	for i, old := range m.sessions {
		if old.id == sessionID {
			m.sessions[i] = s
			s = nil
			break
		}
	}
	if s != nil {
		m.sessions = append(m.sessions, s)
	}
	return m.commit(reqID, sessionID, Decision{
		Accepted: true, Reason: "created", SessionID: sessionID,
		Generation: gen, Owner: owner, Token: tok,
	})
}

func (m *model) validate(sessionID string, gen uint64) (Decision, *modelSession, bool) {
	s := m.find(sessionID)
	if s == nil || s.state != StateActive {
		return Decision{Reason: "no active session", SessionID: sessionID}, nil, false
	}
	if gen != s.generation {
		return Decision{
			Reason:     fmt.Sprintf("stale generation: got %d, current %d", gen, s.generation),
			SessionID:  sessionID,
			Generation: s.generation,
		}, nil, false
	}
	if healthy, known := m.nodes[s.owner]; !known || !healthy {
		return Decision{Reason: "owner node unhealthy", SessionID: sessionID, Generation: s.generation}, nil, false
	}
	return Decision{}, s, true
}

func (m *model) renew(reqID, sessionID string, gen, ttl uint64) Decision {
	m.expire()
	if d, ok := m.checkRequest(reqID, sessionID); ok {
		return d
	}
	if ttl == 0 {
		return m.commit(reqID, sessionID, Decision{Reason: "ttl must be positive", SessionID: sessionID})
	}
	rej, s, ok := m.validate(sessionID, gen)
	if !ok {
		return m.commit(reqID, sessionID, rej)
	}
	s.expiresAt = m.now + ttl
	return m.commit(reqID, sessionID, Decision{
		Accepted: true, Reason: "renewed", SessionID: sessionID,
		Generation: gen, Owner: s.owner, Token: s.token,
	})
}

func (m *model) complete(reqID, sessionID string, gen uint64) Decision {
	m.expire()
	if d, ok := m.checkRequest(reqID, sessionID); ok {
		return d
	}
	rej, s, ok := m.validate(sessionID, gen)
	if !ok {
		return m.commit(reqID, sessionID, rej)
	}
	s.state = StateClosed
	return m.commit(reqID, sessionID, Decision{
		Accepted: true, Reason: "completed", SessionID: sessionID,
		Generation: gen, Owner: s.owner, Token: s.token,
	})
}

func (m *model) writeback(reqID, sessionID string, gen uint64) Decision {
	m.expire()
	if d, ok := m.checkRequest(reqID, sessionID); ok {
		return d
	}
	rej, s, ok := m.validate(sessionID, gen)
	if !ok {
		return m.commit(reqID, sessionID, rej)
	}
	return m.commit(reqID, sessionID, Decision{
		Accepted: true, Reason: "writeback recorded", SessionID: sessionID,
		Generation: gen, Owner: s.owner, Token: s.token,
	})
}

// --- randomized cross-check -------------------------------------------------

type op struct {
	kind    string
	reqID   string
	session string
	gen     uint64
	ttl     uint64
	node    string
	advance uint64
}

func genOps(rnd *rand.Rand, nodes, sessions, reqIDs []string, n int) []op {
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		sess := sessions[rnd.Intn(len(sessions))]
		switch rnd.Intn(8) {
		case 0:
			ops = append(ops, op{kind: "create", reqID: reqIDs[rnd.Intn(len(reqIDs))], session: sess, ttl: uint64(rnd.Intn(15))})
		case 1:
			ops = append(ops, op{kind: "renew", reqID: reqIDs[rnd.Intn(len(reqIDs))], session: sess, gen: uint64(rnd.Intn(5)), ttl: uint64(1 + rnd.Intn(15))})
		case 2:
			ops = append(ops, op{kind: "complete", reqID: reqIDs[rnd.Intn(len(reqIDs))], session: sess, gen: uint64(rnd.Intn(5))})
		case 3:
			ops = append(ops, op{kind: "writeback", reqID: reqIDs[rnd.Intn(len(reqIDs))], session: sess, gen: uint64(rnd.Intn(5))})
		case 4:
			ops = append(ops, op{kind: "fail", node: nodes[rnd.Intn(len(nodes))]})
		case 5:
			ops = append(ops, op{kind: "recover", node: nodes[rnd.Intn(len(nodes))]})
		default:
			ops = append(ops, op{kind: "tick", advance: uint64(rnd.Intn(6))})
		}
	}
	return ops
}

func TestSerialModelCrossCheck(t *testing.T) {
	nodes := []string{"n1", "n2", "n3", "n4"}
	sessions := []string{"s1", "s2", "s3", "s4", "s5"}
	reqIDs := []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8"}

	for seed := int64(0); seed < 200; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		clk := NewManualClock()
		rt := NewRouter(clk, nil)
		md := newModel()
		for _, n := range nodes {
			rt.AddNode(n)
			md.addNode(n)
		}

		ops := genOps(rnd, nodes, sessions, reqIDs, 300)
		for i, o := range ops {
			var got, want Decision
			switch o.kind {
			case "create":
				got = rt.Create(o.reqID, o.session, o.ttl)
				want = md.create(o.reqID, o.session, o.ttl)
			case "renew":
				got = rt.Renew(o.reqID, o.session, o.gen, o.ttl)
				want = md.renew(o.reqID, o.session, o.gen, o.ttl)
			case "complete":
				got = rt.Complete(o.reqID, o.session, o.gen)
				want = md.complete(o.reqID, o.session, o.gen)
			case "writeback":
				got = rt.Writeback(o.reqID, o.session, o.gen, "x")
				want = md.writeback(o.reqID, o.session, o.gen)
			case "fail":
				rt.NodeFailed(o.node)
				md.nodeFailed(o.node)
				continue
			case "recover":
				rt.NodeRecovered(o.node)
				md.nodeRecovered(o.node)
				continue
			case "tick":
				clk.Advance(o.advance)
				md.now += o.advance
				rt.Tick()
				md.expire()
				continue
			}
			if got != want {
				t.Fatalf("seed %d op %d (%+v):\nrouter: %+v\nmodel:  %+v", seed, i, o, got, want)
			}
		}

		// Final state must agree on every session.
		for _, sid := range sessions {
			rs, rok := rt.Session(sid)
			ms := md.find(sid)
			if rok != (ms != nil) {
				t.Fatalf("seed %d: session %s presence mismatch", seed, sid)
			}
			if !rok {
				continue
			}
			if rs.Generation != ms.generation || rs.Owner != ms.owner ||
				rs.ExpiresAt != ms.expiresAt || rs.State != ms.state || rs.Token != ms.token {
				t.Fatalf("seed %d: session %s diverged:\nrouter: %+v\nmodel:  %+v", seed, sid, rs, ms)
			}
		}
		if got, want := rt.ActiveCount(), md.activeCount(); got != want {
			t.Fatalf("seed %d: active count %d, model says %d", seed, got, want)
		}
	}
}
