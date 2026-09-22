package causalslice

import "sort"

// fetchToken carries the per-request routing bookkeeping for one fetch.
// gen invalidates the timer left behind by a superseded attempt, and tried
// remembers every replica already consulted for this exact dependency.
type fetchToken struct {
	snap   int
	key    string
	dep    VersionID
	target string
	tried  []string
	gen    int
}

type waitEntry struct {
	key string
}

// Snapshot is the per-request read state. It is created fresh for every read,
// is never stored inside a replica, and is mutated only by its owning client
// inside event handlers: distinct requests share no mutable snapshot state.
type Snapshot struct {
	client         *Client
	id             int
	keys           []string
	timeout        int
	pinned         map[VersionID]*Version
	pinOrder       []VersionID
	waiting        map[VersionID]*waitEntry
	inflight       map[*fetchToken]bool
	genByKey       map[string]int
	outstanding    map[string]int
	depOutstanding map[VersionID]int
	closed         bool
	result         *Result
}

// BlockedDep explains one outstanding wait: the missing version, which key
// requires it, and which replicas the client has already consulted.
type BlockedDep struct {
	Missing    VersionID
	RequiredBy string
	Tried      []string
}

// Result is the consistent slice: selected version per requested key plus the
// merged version vector of the whole causal past.
type Result struct {
	Closed   bool
	Selected map[string]VersionID
	Vector   map[string]int
}

// Peek is a non-mutating observation of the in-progress slice.
type Peek struct {
	Closed   bool
	Selected map[string]VersionID
	Blocked  []BlockedDep
	Inflight []string
	Vector   map[string]int
}

func (sn *Snapshot) buildSelected() map[string]VersionID {
	sel := map[string]VersionID{}
	for _, k := range sn.keys {
		best, ok := sn.pinnedForKey(k)
		if ok {
			sel[k] = best
		}
	}
	return sel
}

func (sn *Snapshot) pinnedForKey(k string) (VersionID, bool) {
	var best VersionID
	found := false
	for id, v := range sn.pinned {
		if v.Key != k {
			continue
		}
		if !found {
			best, found = id, true
			continue
		}
		bv, cv := sn.pinned[best].VC, v.VC
		if dominates(cv, bv) || (!dominates(bv, cv) && lessVID(id, best)) {
			best = id
		}
	}
	return best, found
}

func (sn *Snapshot) vector() map[string]int {
	vc := map[string]int{}
	for _, v := range sn.pinned {
		vc = mergeVC(vc, v.VC)
	}
	return vc
}

// Peek returns a defensive copy of the current observation; it never reaches
// into or mutates another snapshot's state.
func (c *Client) Peek(snapID int) Peek {
	sn := c.snaps[snapID]
	out := Peek{Closed: sn.closed, Selected: map[string]VersionID{}, Vector: sn.vector()}
	for k, id := range sn.buildSelected() {
		out.Selected[k] = id
	}
	seen := map[VersionID]bool{}
	for tok := range sn.inflight {
		out.Inflight = append(out.Inflight, tok.target+":"+labelFetch(tok))
		if !tok.dep.IsZero() && !seen[tok.dep] {
			seen[tok.dep] = true
			out.Blocked = append(out.Blocked, BlockedDep{
				Missing: tok.dep, RequiredBy: tok.key, Tried: append([]string(nil), tok.tried...),
			})
		}
	}
	for _, id := range sortedWaitKeys(sn.waiting) {
		if seen[id] {
			continue
		}
		out.Blocked = append(out.Blocked, BlockedDep{
			Missing: id, RequiredBy: sn.waiting[id].key,
		})
	}
	sort.Slice(out.Blocked, func(i, j int) bool { return lessVID(out.Blocked[i].Missing, out.Blocked[j].Missing) })
	sort.Strings(out.Inflight)
	return out
}

// Client drives writes and per-request consistent reads.
type Client struct {
	sim     *Sim
	id      string
	context []VersionID
	snaps   map[int]*Snapshot
	next    int
	onClose func(snapID int, res Result)
}

// NewClient registers a client. onClose is invoked exactly once per snapshot,
// from inside the event handler that closes it.
func NewClient(sim *Sim, id string, onClose func(snapID int, res Result)) *Client {
	c := &Client{sim: sim, id: id, snaps: map[int]*Snapshot{}, onClose: onClose}
	sim.Register(c)
	return c
}

func (c *Client) ID() string { return c.id }

// Put issues a causal write to target. deps, when non-nil, is the explicit
// causal context; otherwise the client's accumulated context is used.
func (c *Client) Put(target, key, val string, deps []VersionID) {
	ctx := c.context
	if deps != nil {
		ctx = deps
	}
	c.sim.Send(Envelope{From: c.id, To: target, Msg: PutReq{
		Key: key, Val: val, KnownDeps: append([]VersionID(nil), ctx...),
	}})
}

// Read opens a fresh isolated snapshot for keys. timeout is the retransmit
// interval for outstanding fetches.
func (c *Client) Read(replica string, keys []string, timeout int) int {
	c.next++
	id := c.next
	sn := &Snapshot{
		client:         c,
		id:             id,
		keys:           append([]string(nil), keys...),
		timeout:        timeout,
		pinned:         map[VersionID]*Version{},
		waiting:        map[VersionID]*waitEntry{},
		inflight:       map[*fetchToken]bool{},
		genByKey:       map[string]int{},
		outstanding:    map[string]int{},
		depOutstanding: map[VersionID]int{},
	}
	c.snaps[id] = sn
	sort.Strings(sn.keys)
	c.sim.Log("%s opens snapshot #%d at %s for %v", c.id, id, replica, sn.keys)
	for _, k := range sn.keys {
		sn.outstanding[k]++
		c.sendFetch(sn, k, VersionID{}, replica, []string{replica})
	}
	return id
}

func (c *Client) sendFetch(sn *Snapshot, key string, dep VersionID, target string, tried []string) {
	sn.genByKey[key]++
	tok := &fetchToken{
		snap:   sn.id,
		key:    key,
		dep:    dep,
		target: target,
		tried:  append([]string(nil), tried...),
		gen:    sn.genByKey[key],
	}
	sn.inflight[tok] = true
	c.sim.Schedule(c.id, c.sim.Time()+sn.timeout, tok)
	c.sim.Send(Envelope{From: c.id, To: target, Msg: FetchReq{Key: key, Dep: dep, Token: tok}})
}

func (c *Client) Receive(sim *Sim, env Envelope) {
	switch m := env.Msg.(type) {
	case PutAck:
		if m.Err != nil {
			sim.Log("%s write of %s refused: %v", c.id, m.Key, m.Err)
			return
		}
		c.context = []VersionID{m.Ver.ID}
		sim.Log("%s advances causal context to %s", c.id, m.Ver.ID)
	case FetchResp:
		c.handleFetchResp(m)
	case Timer:
		if tok, ok := m.Tag.(*fetchToken); ok {
			c.handleTimeout(tok)
		}
	}
}

func (c *Client) handleFetchResp(m FetchResp) {
	tok, ok := m.Token.(*fetchToken)
	if !ok {
		return
	}
	sn := c.snaps[tok.snap]
	if sn == nil || sn.closed {
		return
	}
	if !sn.inflight[tok] || sn.genByKey[tok.key] != tok.gen {
		return
	}
	delete(sn.inflight, tok)

	if m.Ver != nil {
		for _, v := range m.Closure {
			c.pin(sn, v)
		}
		c.pin(sn, m.Ver)
	}
	// A precise dependency fetch answered with Ver==nil means this replica
	// lacks the version too: the dependency stays missing (re-enqueued wait),
	// it must never be treated as resolved.
	if !tok.dep.IsZero() && m.Ver == nil {
		sn.depOutstanding[tok.dep]--
		c.enqueueWait(sn, tok.dep, tok.key, tok.tried)
		c.advance(sn)
		return
	}
	c.settle(sn, tok, m.Missing)
	c.advance(sn)
}

// settle resolves one attempt: the key/dependency it served is no longer
// outstanding, and the reported missing frontier becomes wait state.
func (c *Client) settle(sn *Snapshot, tok *fetchToken, missing []VersionID) {
	if tok.dep.IsZero() {
		sn.outstanding[tok.key]--
	} else {
		sn.depOutstanding[tok.dep]--
	}
	for _, miss := range missing {
		c.enqueueWait(sn, miss, tok.key, tok.tried)
	}
}

// pin records a version in this snapshot only; the replica store is untouched.
func (c *Client) pin(sn *Snapshot, v *Version) {
	if v == nil || sn.pinned[v.ID] != nil {
		return
	}
	sn.pinned[v.ID] = v
	delete(sn.waiting, v.ID)
}

func (c *Client) enqueueWait(sn *Snapshot, dep VersionID, key string, tried []string) {
	if sn.pinned[dep] != nil || dep.IsZero() {
		return
	}
	if sn.waiting[dep] != nil {
		return
	}
	sn.waiting[dep] = &waitEntry{key: key}
	c.sim.Log("snapshot #%d marks %s waiting (needed for %s; tried %v)", sn.id, dep, key, tried)
}

// advance launches fetches for newly discovered missing causes and closes the
// slice only when every initial key and every fetched dependency is fully
// resolved: nothing in flight, nothing missing.
func (c *Client) advance(sn *Snapshot) {
	for _, dep := range sortedWaitKeys(sn.waiting) {
		w := sn.waiting[dep]
		tried := sn.triedFor(dep)
		target := c.pickReplica(tried)
		if target == "" {
			continue
		}
		delete(sn.waiting, dep)
		sn.depOutstanding[dep]++
		c.sim.Log("snapshot #%d fetches cause %s from %s", sn.id, dep, target)
		c.sendFetch(sn, w.key, dep, target, tried)
	}
	if sn.closed || !sn.resolved() || len(sn.inflight) > 0 {
		return
	}
	for id, n := range sn.depOutstanding {
		if n > 0 {
			return
		}
		if _, stillWaiting := sn.waiting[id]; stillWaiting {
			return
		}
	}
	c.close(sn)
}

func (sn *Snapshot) resolved() bool {
	for _, k := range sn.keys {
		if sn.outstanding[k] != 0 {
			return false
		}
	}
	return true
}

func (sn *Snapshot) triedFor(dep VersionID) []string {
	for tok := range sn.inflight {
		if tok.dep == dep {
			return tok.tried
		}
	}
	return nil
}

func (c *Client) handleTimeout(tok *fetchToken) {
	sn := c.snaps[tok.snap]
	if sn == nil || sn.closed {
		return
	}
	if !sn.inflight[tok] || sn.genByKey[tok.key] != tok.gen {
		return
	}
	delete(sn.inflight, tok)
	tried := append(append([]string(nil), tok.tried...), tok.target)
	target := c.pickReplica(tried)
	if target == "" {
		// Every replica was consulted and none answered: start a fresh
		// rotation; the dependency remains outstanding until one replies.
		target = c.sim.PeerIDs()[0]
		tried = nil
	}
	c.sim.Log("snapshot #%d timeout on %s via %s, retry via %s", sn.id, labelFetch(tok), tok.target, target)
	if tok.dep.IsZero() {
		c.sendFetch(sn, tok.key, tok.dep, target, tried)
	} else {
		// Keep the same outstanding counter for this dependency.
		c.sendFetchKeepingDep(sn, tok, target, tried)
	}
	c.advance(sn)
}

func (c *Client) sendFetchKeepingDep(sn *Snapshot, tok *fetchToken, target string, tried []string) {
	sn.genByKey[tok.key]++
	next := &fetchToken{
		snap: sn.id, key: tok.key, dep: tok.dep, target: target,
		tried: append([]string(nil), tried...), gen: sn.genByKey[tok.key],
	}
	sn.inflight[next] = true
	c.sim.Schedule(c.id, c.sim.Time()+sn.timeout, next)
	c.sim.Send(Envelope{From: c.id, To: target, Msg: FetchReq{Key: tok.key, Dep: tok.dep, Token: next}})
}

func labelFetch(tok *fetchToken) string {
	if tok.dep.IsZero() {
		return tok.key
	}
	return tok.dep.String()
}

// pickReplica returns a live replica not present in tried.
func (c *Client) pickReplica(tried []string) string {
	seen := map[string]bool{}
	for _, p := range tried {
		seen[p] = true
	}
	for _, p := range c.sim.PeerIDs() {
		r, _ := c.sim.Actor(p).(*Replica)
		if r != nil && !r.down() && !seen[p] {
			return p
		}
	}
	return ""
}

func (c *Client) close(sn *Snapshot) {
	sn.closed = true
	res := Result{Closed: true, Selected: sn.buildSelected(), Vector: sn.vector()}
	sn.result = &res
	c.sim.Log("snapshot #%d closes: selected=%s vector=%s", sn.id, formatSel(res.Selected), formatVC(res.Vector))
	if c.onClose != nil {
		c.onClose(sn.id, res)
	}
}

func sortedWaitKeys(m map[VersionID]*waitEntry) []VersionID {
	ids := make([]VersionID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return lessVID(ids[i], ids[j]) })
	return ids
}
