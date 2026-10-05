package sessionrouter

// model is an independent, deliberately simple serial implementation of
// the routing spec. The randomized cross-check test drives the Router and
// this model with identical operation sequences and requires identical
// decisions and identical resulting state.

import (
	"math/rand"
	"sort"
	"testing"
)

type mSess struct {
	gen    uint64
	owner  NodeID
	token  string
	exp    Tick
	closed bool
}

type mRec struct {
	fp  string
	dec Decision
}

type model struct {
	t       Tick
	sess    map[SessionID]*mSess
	idem    map[RequestID]mRec
	healthy map[NodeID]bool
	hw      map[SessionID]uint64
}

func newModel() *model {
	return &model{
		sess:    map[SessionID]*mSess{},
		idem:    map[RequestID]mRec{},
		healthy: map[NodeID]bool{},
		hw:      map[SessionID]uint64{},
	}
}

func (m *model) active() int {
	n := 0
	for _, s := range m.sess {
		if !s.closed {
			n++
		}
	}
	return n
}

func (m *model) expire() {
	ids := make([]string, 0, len(m.sess))
	for id := range m.sess {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := m.sess[SessionID(id)]
		if !s.closed && s.exp <= m.t {
			s.closed = true
		}
	}
}

func (m *model) run(reqID RequestID, fp string, op func() Decision) Decision {
	m.expire()
	if rec, ok := m.idem[reqID]; ok {
		if rec.fp != fp {
			return Decision{ReqID: reqID, Allowed: false, Code: CodeConflict, ActiveSessions: m.active()}
		}
		d := rec.dec
		d.Replayed = true
		return d
	}
	d := op()
	d.ActiveSessions = m.active()
	m.idem[reqID] = mRec{fp: fp, dec: d}
	return d
}

func (m *model) bump(sid SessionID, gen uint64) {
	if m.hw[sid] < gen {
		m.hw[sid] = gen
	}
}

func (m *model) NodeUp(n NodeID) {
	m.healthy[n] = true
}

func (m *model) NodeDown(n NodeID) {
	if !m.healthy[n] {
		return
	}
	m.healthy[n] = false
	m.expire()
	hs := sortedHealthy(m.healthy)
	ids := make([]string, 0, len(m.sess))
	for id := range m.sess {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		s := m.sess[SessionID(id)]
		if s.closed || s.owner != n {
			continue
		}
		if len(hs) == 0 {
			s.closed = true
			continue
		}
		s.gen++
		s.owner = pickOwner(SessionID(id), s.gen, hs)
		s.token = tokenFor(SessionID(id), s.gen, s.owner)
		m.bump(SessionID(id), s.gen)
	}
}

func (m *model) Create(reqID RequestID, sid SessionID, ttl int64) Decision {
	return m.run(reqID, fpCreate(sid, ttl), func() Decision {
		d := Decision{ReqID: reqID, Op: OpCreate, SessionID: sid}
		if ttl <= 0 {
			d.Code = CodeInvalidTTL
			return d
		}
		if s, ok := m.sess[sid]; ok && !s.closed {
			d.Code = CodeSessionExists
			return d
		}
		hs := sortedHealthy(m.healthy)
		if len(hs) == 0 {
			d.Code = CodeNoHealthyNodes
			return d
		}
		gen := uint64(1)
		if s, ok := m.sess[sid]; ok {
			gen = s.gen + 1
		}
		if m.hw[sid] >= gen {
			gen = m.hw[sid] + 1
		}
		owner := pickOwner(sid, gen, hs)
		tok := tokenFor(sid, gen, owner)
		m.sess[sid] = &mSess{gen: gen, owner: owner, token: tok, exp: m.t + Tick(ttl)}
		m.bump(sid, gen)
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = gen
		d.Owner = owner
		d.Token = tok
		return d
	})
}

func (m *model) lookupLive(sid SessionID, generation uint64, d *Decision) *mSess {
	s, ok := m.sess[sid]
	if !ok {
		d.Code = CodeUnknownSession
		return nil
	}
	if s.closed {
		d.Code = CodeSessionClosed
		return nil
	}
	if generation != s.gen {
		d.Code = CodeStaleGeneration
		return nil
	}
	return s
}

func (m *model) Renew(reqID RequestID, sid SessionID, generation uint64, ttl int64) Decision {
	return m.run(reqID, fpRenew(sid, generation, ttl), func() Decision {
		d := Decision{ReqID: reqID, Op: OpRenew, SessionID: sid, Generation: generation}
		if ttl <= 0 {
			d.Code = CodeInvalidTTL
			return d
		}
		s := m.lookupLive(sid, generation, &d)
		if s == nil {
			return d
		}
		s.exp = m.t + Tick(ttl)
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = s.gen
		d.Owner = s.owner
		d.Token = s.token
		return d
	})
}

func (m *model) Complete(reqID RequestID, sid SessionID, generation uint64) Decision {
	return m.run(reqID, fpComplete(sid, generation), func() Decision {
		d := Decision{ReqID: reqID, Op: OpComplete, SessionID: sid, Generation: generation}
		s := m.lookupLive(sid, generation, &d)
		if s == nil {
			return d
		}
		s.closed = true
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = s.gen
		d.Owner = s.owner
		d.Token = s.token
		return d
	})
}

func (m *model) Writeback(reqID RequestID, sid SessionID, generation uint64, data string) Decision {
	return m.run(reqID, fpWriteback(sid, generation, data), func() Decision {
		d := Decision{ReqID: reqID, Op: OpWriteback, SessionID: sid, Generation: generation}
		s := m.lookupLive(sid, generation, &d)
		if s == nil {
			return d
		}
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = s.gen
		d.Owner = s.owner
		d.Token = s.token
		return d
	})
}

func (m *model) Migrate(reqID RequestID, sid SessionID) Decision {
	return m.run(reqID, fpMigrate(sid), func() Decision {
		d := Decision{ReqID: reqID, Op: OpMigrate, SessionID: sid}
		s, ok := m.sess[sid]
		if !ok {
			d.Code = CodeUnknownSession
			return d
		}
		if s.closed {
			d.Code = CodeSessionClosed
			return d
		}
		hs := sortedHealthy(m.healthy)
		if len(hs) == 0 {
			d.Code = CodeNoHealthyNodes
			return d
		}
		s.gen++
		s.owner = pickOwner(sid, s.gen, hs)
		s.token = tokenFor(sid, s.gen, s.owner)
		m.bump(sid, s.gen)
		d.Allowed = true
		d.Code = CodeOK
		d.Generation = s.gen
		d.Owner = s.owner
		d.Token = s.token
		return d
	})
}

// sameDecision compares everything except the human-readable Reason text.
func sameDecision(a, b Decision) bool {
	return a.ReqID == b.ReqID &&
		a.Op == b.Op &&
		a.Allowed == b.Allowed &&
		a.Code == b.Code &&
		a.SessionID == b.SessionID &&
		a.Generation == b.Generation &&
		a.Owner == b.Owner &&
		a.Token == b.Token &&
		a.Replayed == b.Replayed &&
		a.ActiveSessions == b.ActiveSessions
}

func TestModelCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	clk := NewTestClock()
	r, err := NewRouter(NewMemStore(), clk)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel()

	nodes := []NodeID{"n0", "n1", "n2"}
	sessions := []SessionID{"s0", "s1", "s2", "s3", "s4"}

	check := func(step int, what string, got, want Decision) {
		if !sameDecision(got, want) {
			t.Fatalf("step %d (%s):\n router: %+v\n model:  %+v", step, what, got, want)
		}
		if r.ActiveCount() != m.active() {
			t.Fatalf("step %d (%s): active count router=%d model=%d", step, what, r.ActiveCount(), m.active())
		}
	}

	for i := 0; i < 4000; i++ {
		clk.Advance(int64(rng.Intn(3)))
		m.t = clk.Now()
		reqID := RequestID("rq" + string(rune('0'+rng.Intn(40))))
		sid := sessions[rng.Intn(len(sessions))]
		switch rng.Intn(9) {
		case 0:
			n := nodes[rng.Intn(len(nodes))]
			r.NodeDown(n)
			m.NodeDown(n)
		case 1:
			n := nodes[rng.Intn(len(nodes))]
			r.NodeUp(n)
			m.NodeUp(n)
		case 2, 3:
			ttl := int64(rng.Intn(9) - 1) // occasionally non-positive
			check(i, "create", r.Create(reqID, sid, ttl), m.Create(reqID, sid, ttl))
		case 4:
			gen := uint64(rng.Intn(6))
			ttl := int64(rng.Intn(8) + 1)
			check(i, "renew", r.Renew(reqID, sid, gen, ttl), m.Renew(reqID, sid, gen, ttl))
		case 5:
			gen := uint64(rng.Intn(6))
			check(i, "complete", r.Complete(reqID, sid, gen), m.Complete(reqID, sid, gen))
		case 6:
			gen := uint64(rng.Intn(6))
			data := "d" + string(rune('0'+rng.Intn(3)))
			check(i, "writeback", r.Writeback(reqID, sid, gen, data), m.Writeback(reqID, sid, gen, data))
		case 7, 8:
			check(i, "migrate", r.Migrate(reqID, sid), m.Migrate(reqID, sid))
		}
	}

	// Final full-state comparison.
	for _, sid := range sessions {
		rs, rok := r.Lookup(sid)
		ms, mok := m.sess[sid]
		if rok != mok {
			t.Fatalf("session %s existence mismatch: router=%v model=%v", sid, rok, mok)
		}
		if !rok {
			continue
		}
		if rs.Generation != ms.gen || rs.Owner != ms.owner || rs.Token != ms.token ||
			rs.ExpiresAt != ms.exp || rs.Closed != ms.closed {
			t.Fatalf("session %s state mismatch:\n router: %+v\n model:  %+v", sid, rs, ms)
		}
	}
}
