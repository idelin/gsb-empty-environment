package barrier

import "sort"

// shardState tracks one shard incarnation.
type shardState struct {
	epoch     Epoch               // current incarnation
	known     bool                // recovery proof received for this epoch
	online    bool                // shard process is reachable
	watermark Commit              // contiguous applied commit, only valid when known
	buffered  map[Commit]struct{} // out-of-order reports waiting for gap fill
}

// request is a pending client wait.
type request struct {
	id       ReqID
	target   Commit
	shards   []ShardID
	deadline Tick
}

// Coordinator is a deterministic read-barrier coordinator. All methods are
// driven explicitly (events + clock); there is no wall-clock or goroutine,
// so replays are fully deterministic.
type Coordinator struct {
	clock   *ManualClock
	shards  map[ShardID]*shardState
	reqs    map[ReqID]*request
	index   map[ShardID]map[ReqID]struct{} // shared wait index: shard -> waiting requests
	next    ReqID
	results []Result
}

// NewCoordinator builds a coordinator driven by the given monotonic clock.
func NewCoordinator(clock *ManualClock) *Coordinator {
	return &Coordinator{
		clock:  clock,
		shards: make(map[ShardID]*shardState),
		reqs:   make(map[ReqID]*request),
		index:  make(map[ShardID]map[ReqID]struct{}),
	}
}

func (c *Coordinator) shardStateFor(shard ShardID) *shardState {
	s, ok := c.shards[shard]
	if !ok {
		s = &shardState{buffered: make(map[Commit]struct{})}
		c.shards[shard] = s
	}
	return s
}

// drain advances the watermark through any buffered contiguous commits.
func (c *Coordinator) drain(s *shardState) {
	for {
		next := s.watermark + 1
		if _, ok := s.buffered[next]; !ok {
			return
		}
		delete(s.buffered, next)
		s.watermark = next
	}
}

// Report records that a shard instance applied the contiguous prefix up to
// commit. Reports may arrive out of order; a higher number with a gap in
// between is buffered and never counted as progress until the gap fills.
// Duplicate reports are idempotent. Reports from an older epoch are stale
// and ignored; reports from a newer epoch start a fresh incarnation whose
// progress stays unknown until a recovery proof arrives.
func (c *Coordinator) Report(shard ShardID, epoch Epoch, commit Commit) {
	s := c.shardStateFor(shard)
	if epoch < s.epoch {
		return // stale report from a dead incarnation
	}
	if epoch > s.epoch {
		s.epoch = epoch
		s.known = false
		s.online = true
		s.watermark = 0
		s.buffered = make(map[Commit]struct{})
	}
	if s.known && commit <= s.watermark {
		return // duplicate or already-covered report
	}
	s.buffered[commit] = struct{}{}
	if !s.known {
		return // progress unknown until the recovery proof lands
	}
	before := s.watermark
	c.drain(s)
	if s.watermark != before {
		c.onProgress(shard)
	}
}

// Recover is the recovery proof for a shard incarnation: it certifies that
// the instance has contiguously applied up to base. Until this arrives the
// incarnation's progress is treated as unknown. Proofs for older epochs are
// stale; a repeated proof for the current epoch is idempotent.
func (c *Coordinator) Recover(shard ShardID, epoch Epoch, base Commit) {
	s := c.shardStateFor(shard)
	if epoch < s.epoch {
		return
	}
	if epoch == s.epoch && s.known {
		return // duplicate proof
	}
	if epoch > s.epoch {
		s.epoch = epoch
		s.buffered = make(map[Commit]struct{})
	}
	s.known = true
	s.online = true
	s.watermark = base
	c.drain(s) // flush reports that arrived before the proof
	c.onProgress(shard)
}

// Offline marks a shard unreachable and fails every request still waiting
// on it. Finished requests were already removed from the wait index, so an
// offline shard never pins them.
func (c *Coordinator) Offline(shard ShardID) {
	s := c.shardStateFor(shard)
	s.online = false
	for _, id := range sortedReqIDs(c.index[shard]) {
		if req, ok := c.reqs[id]; ok {
			c.finish(req, Result{Req: id, Reason: ReasonOffline, Lagging: c.lagging(req)})
		}
	}
}

// Wait registers a client request: grant a view pinned at target once every
// shard in shards has contiguously applied it, or fail at deadline.
func (c *Coordinator) Wait(target Commit, shards []ShardID, deadline Tick) ReqID {
	c.next++
	req := &request{id: c.next, target: target, shards: normalize(shards), deadline: deadline}
	c.reqs[req.id] = req
	for _, sh := range req.shards {
		set, ok := c.index[sh]
		if !ok {
			set = make(map[ReqID]struct{})
			c.index[sh] = set
		}
		set[req.id] = struct{}{}
	}
	c.evaluate(req) // may already be satisfiable
	return req.id
}

// Cancel removes exactly this request from the shared wait index. Other
// clients waiting on the same shards are unaffected.
func (c *Coordinator) Cancel(id ReqID) bool {
	req, ok := c.reqs[id]
	if !ok {
		return false
	}
	c.finish(req, Result{Req: id, Reason: ReasonCancelled})
	return true
}

// JudgeTimeouts expires requests whose deadline has been reached. Progress
// is always applied before this judgement, so a request whose target was
// reached at the same tick as its deadline is granted, not timed out.
func (c *Coordinator) JudgeTimeouts() {
	now := c.clock.Now()
	var due []ReqID
	for id, req := range c.reqs {
		if now >= req.deadline {
			due = append(due, id)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
	for _, id := range due {
		req, ok := c.reqs[id]
		if !ok {
			continue
		}
		if c.ready(req) {
			c.grant(req)
		} else {
			c.finish(req, Result{Req: id, Reason: ReasonTimeout, Lagging: c.lagging(req)})
		}
	}
}

// onProgress re-evaluates only the requests waiting on this shard.
func (c *Coordinator) onProgress(shard ShardID) {
	for _, id := range sortedReqIDs(c.index[shard]) {
		if req, ok := c.reqs[id]; ok {
			c.evaluate(req)
		}
	}
}

func (c *Coordinator) evaluate(req *request) {
	if c.ready(req) {
		c.grant(req)
		return
	}
	// A request that depends on an offline shard can never be served;
	// fail it instead of letting it occupy the wait index.
	for _, sh := range req.shards {
		if s, ok := c.shards[sh]; ok && !s.online {
			c.finish(req, Result{Req: req.id, Reason: ReasonOffline, Lagging: c.lagging(req)})
			return
		}
	}
}

// ready reports whether every required shard can serve the target commit.
func (c *Coordinator) ready(req *request) bool {
	for _, sh := range req.shards {
		s, ok := c.shards[sh]
		if !ok || !s.online || !s.known || s.watermark < req.target {
			return false
		}
	}
	return true
}

// lagging snapshots every shard that currently blocks the request.
func (c *Coordinator) lagging(req *request) []Lagging {
	var out []Lagging
	for _, sh := range req.shards {
		s, ok := c.shards[sh]
		switch {
		case !ok || !s.online || !s.known:
			out = append(out, Lagging{Shard: sh, Known: false})
		case s.watermark < req.target:
			out = append(out, Lagging{Shard: sh, Known: true, Progress: s.watermark, Gap: req.target - s.watermark})
		}
	}
	return out // req.shards is already sorted
}

func (c *Coordinator) grant(req *request) {
	view := View{Commit: req.target, Shards: append([]ShardID(nil), req.shards...)}
	c.finish(req, Result{Req: req.id, Granted: true, View: view})
}

// finish removes the request from every structure and records its result.
func (c *Coordinator) finish(req *request, res Result) {
	delete(c.reqs, req.id)
	for _, sh := range req.shards {
		if set, ok := c.index[sh]; ok {
			delete(set, req.id)
			if len(set) == 0 {
				delete(c.index, sh)
			}
		}
	}
	c.results = append(c.results, res)
}

// DrainResults returns and clears the finished results, in finish order.
func (c *Coordinator) DrainResults() []Result {
	out := c.results
	c.results = nil
	return out
}

// Stats counts live state for resource-leak assertions.
func (c *Coordinator) Stats() Stats {
	st := Stats{Shards: len(c.shards), PendingRequests: len(c.reqs)}
	for _, set := range c.index {
		st.IndexEntries += len(set)
	}
	for _, s := range c.shards {
		st.BufferedReports += len(s.buffered)
	}
	return st
}
