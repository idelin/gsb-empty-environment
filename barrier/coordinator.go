package barrier

import "sort"

// shardState tracks one shard incarnation. contig is the contiguous
// applied commit number; pending holds applied commits above a hole and
// never counts as progress until the hole is filled.
type shardState struct {
	epoch    Epoch
	known    bool // recovery proof received (or first report, trusted)
	contig   uint64
	pending  map[uint64]struct{} // applied commits waiting on a gap
	preProof []uint64            // applied commits reported before the proof
	online   bool
}

func (s *shardState) reset(epoch Epoch) {
	s.epoch = epoch
	s.known = false
	s.contig = 0
	s.pending = make(map[uint64]struct{})
	s.preProof = nil
	s.online = true
}

// apply folds one applied commit into contiguous progress. Duplicates
// and already-covered commits are no-ops (idempotency).
func (s *shardState) apply(n uint64) {
	if n <= s.contig {
		return
	}
	if n > s.contig+1 {
		s.pending[n] = struct{}{}
		return
	}
	s.contig++
	for {
		if _, ok := s.pending[s.contig+1]; !ok {
			return
		}
		delete(s.pending, s.contig+1)
		s.contig++
	}
}

// Coordinator is the read-barrier coordinator. All clients share one
// wait index; events are processed synchronously and deterministically.
type Coordinator struct {
	clock   *TestClock
	shards  map[ShardID]*shardState
	reqs    map[ReqID]*Request
	byShard map[ShardID]map[ReqID]struct{}
	nextReq ReqID
}

func NewCoordinator(clock *TestClock) *Coordinator {
	return &Coordinator{
		clock:   clock,
		shards:  make(map[ShardID]*shardState),
		reqs:    make(map[ReqID]*Request),
		byShard: make(map[ShardID]map[ReqID]struct{}),
	}
}

// Submit registers a barrier request. It returns the request id and,
// when the barrier is already satisfied (or the deadline has already
// passed), the immediate outcome; otherwise nil and the outcome arrives
// via HandleReport/Advance.
func (c *Coordinator) Submit(target uint64, shards []ShardID, deadline Tick) (ReqID, *Outcome) {
	c.nextReq++
	id := c.nextReq
	cp := make([]ShardID, len(shards))
	copy(cp, shards)
	req := &Request{ID: id, Target: target, Shards: cp, Deadline: deadline}
	c.reqs[id] = req
	for _, s := range cp {
		set := c.byShard[s]
		if set == nil {
			set = make(map[ReqID]struct{})
			c.byShard[s] = set
		}
		set[id] = struct{}{}
	}
	if c.satisfied(req) {
		c.remove(id)
		return id, grantOutcome(req)
	}
	if deadline <= c.clock.Now() {
		out := c.timeoutOutcome(req)
		c.remove(id)
		return id, &out
	}
	return id, nil
}

// HandleReport folds one shard report into the progress state and
// grants any requests on that shard whose barrier is now satisfied.
// Stale-epoch reports are ignored; duplicate reports are no-ops.
func (c *Coordinator) HandleReport(r Report) []Outcome {
	st := c.shards[r.Shard]
	switch {
	case st == nil:
		st = &shardState{online: true, pending: make(map[uint64]struct{})}
		st.epoch = r.Epoch
		st.known = true // first incarnation is trusted
		c.shards[r.Shard] = st
	case r.Epoch < st.epoch:
		return nil // old epoch cannot advance the new instance
	case r.Epoch > st.epoch:
		st.reset(r.Epoch) // implicit restart
	}
	if r.Proof {
		st.known = true
		if r.ProofContig > st.contig {
			st.contig = r.ProofContig
		}
		for n := range st.pending {
			if n <= st.contig {
				delete(st.pending, n)
			}
		}
		buffered := st.preProof
		st.preProof = nil
		for _, n := range buffered {
			st.apply(n)
		}
	}
	if st.known {
		for _, n := range r.Applied {
			st.apply(n)
		}
	} else {
		st.preProof = append(st.preProof, r.Applied...)
	}
	return c.evaluateShard(r.Shard)
}

// Restart registers a shard restart under a new epoch. Progress becomes
// unknown until a recovery proof arrives.
func (c *Coordinator) Restart(shard ShardID, epoch Epoch) {
	st := c.shards[shard]
	if st == nil {
		st = &shardState{}
		c.shards[shard] = st
	}
	st.reset(epoch)
}

// SetOffline marks a shard online/offline. It never touches finished
// requests: those are already out of the wait index.
func (c *Coordinator) SetOffline(shard ShardID, online bool) {
	st := c.shards[shard]
	if st == nil {
		st = &shardState{pending: make(map[uint64]struct{})}
		c.shards[shard] = st
	}
	st.online = online
}

// Advance moves the clock and times out requests whose deadline tick
// has been reached. Reports at the same tick are handled before this
// call, so progress and deadline arriving together grant first.
func (c *Coordinator) Advance(to Tick) []Outcome {
	c.clock.Advance(to)
	now := c.clock.Now()
	var due []ReqID
	for id, req := range c.reqs {
		if req.Deadline <= now {
			due = append(due, id)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
	var out []Outcome
	for _, id := range due {
		req := c.reqs[id]
		if req == nil {
			continue
		}
		out = append(out, c.timeoutOutcome(req))
		c.remove(id)
	}
	return out
}

// Cancel removes exactly one request from the shared wait index.
func (c *Coordinator) Cancel(id ReqID) bool {
	if _, ok := c.reqs[id]; !ok {
		return false
	}
	c.remove(id)
	return true
}

// Stats reports live coordinator state for resource accounting.
type Stats struct {
	PendingRequests  int // requests still waiting
	TrackedShards    int // shard states retained
	WaitIndexEntries int // shard -> request index entries
	PendingHoles     int // applied commits buffered above gaps
	PreProofBuffered int // applied commits buffered before recovery proofs
}

func (c *Coordinator) Stats() Stats {
	var st Stats
	st.PendingRequests = len(c.reqs)
	st.TrackedShards = len(c.shards)
	for _, set := range c.byShard {
		st.WaitIndexEntries += len(set)
	}
	for _, s := range c.shards {
		st.PendingHoles += len(s.pending)
		st.PreProofBuffered += len(s.preProof)
	}
	return st
}

func (c *Coordinator) satisfied(req *Request) bool {
	for _, s := range req.Shards {
		st := c.shards[s]
		if st == nil || !st.online || !st.known || st.contig < req.Target {
			return false
		}
	}
	return true
}

func (c *Coordinator) lagging(req *Request) []ShardGap {
	var gaps []ShardGap
	for _, s := range req.Shards {
		st := c.shards[s]
		switch {
		case st == nil:
			gaps = append(gaps, ShardGap{Shard: s, Unknown: true})
		case !st.online:
			gaps = append(gaps, ShardGap{Shard: s, Contig: st.contig, Offline: true, Unknown: !st.known})
		case !st.known:
			gaps = append(gaps, ShardGap{Shard: s, Contig: st.contig, Unknown: true})
		case st.contig < req.Target:
			gaps = append(gaps, ShardGap{Shard: s, Contig: st.contig, Missing: req.Target - st.contig})
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Shard < gaps[j].Shard })
	return gaps
}

func (c *Coordinator) evaluateShard(shard ShardID) []Outcome {
	set := c.byShard[shard]
	if len(set) == 0 {
		return nil
	}
	ids := make([]ReqID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []Outcome
	for _, id := range ids {
		req := c.reqs[id]
		if req == nil || !c.satisfied(req) {
			continue
		}
		out = append(out, *grantOutcome(req))
		c.remove(id)
	}
	return out
}

func (c *Coordinator) remove(id ReqID) {
	req := c.reqs[id]
	if req == nil {
		return
	}
	delete(c.reqs, id)
	for _, s := range req.Shards {
		set := c.byShard[s]
		delete(set, id)
		if len(set) == 0 {
			delete(c.byShard, s)
		}
	}
}

func grantOutcome(req *Request) *Outcome {
	shards := make([]ShardID, len(req.Shards))
	copy(shards, req.Shards)
	return &Outcome{Req: req.ID, Kind: Granted, View: &View{Target: req.Target, Shards: shards}}
}

func (c *Coordinator) timeoutOutcome(req *Request) Outcome {
	return Outcome{Req: req.ID, Kind: TimedOut, Lagging: c.lagging(req)}
}
