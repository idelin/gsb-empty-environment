package barrier

import "sort"

// Reference is the deliberately simple full-scan reference implementation.
// It keeps no wait index: every event re-scans every pending request. It is
// too slow for production but obviously correct, so tests replay identical
// event streams through Reference and Coordinator and demand identical
// results.
type Reference struct {
	clock   *ManualClock
	shards  map[ShardID]*refShard
	reqs    map[ReqID]*request
	next    ReqID
	results []Result
}

type refShard struct {
	epoch     Epoch
	known     bool
	online    bool
	watermark Commit
	buffered  map[Commit]struct{}
}

// NewReference builds a reference coordinator on the given clock.
func NewReference(clock *ManualClock) *Reference {
	return &Reference{
		clock:  clock,
		shards: make(map[ShardID]*refShard),
		reqs:   make(map[ReqID]*request),
	}
}

func (r *Reference) shardFor(shard ShardID) *refShard {
	s, ok := r.shards[shard]
	if !ok {
		s = &refShard{buffered: make(map[Commit]struct{})}
		r.shards[shard] = s
	}
	return s
}

func refDrain(s *refShard) {
	for {
		next := s.watermark + 1
		if _, ok := s.buffered[next]; !ok {
			return
		}
		delete(s.buffered, next)
		s.watermark = next
	}
}

// Report mirrors Coordinator.Report.
func (r *Reference) Report(shard ShardID, epoch Epoch, commit Commit) {
	s := r.shardFor(shard)
	if epoch < s.epoch {
		return
	}
	if epoch > s.epoch {
		s.epoch = epoch
		s.known = false
		s.online = true
		s.watermark = 0
		s.buffered = make(map[Commit]struct{})
	}
	if s.known && commit <= s.watermark {
		return
	}
	s.buffered[commit] = struct{}{}
	if !s.known {
		return
	}
	refDrain(s)
	r.scan()
}

// Recover mirrors Coordinator.Recover.
func (r *Reference) Recover(shard ShardID, epoch Epoch, base Commit) {
	s := r.shardFor(shard)
	if epoch < s.epoch {
		return
	}
	if epoch == s.epoch && s.known {
		return
	}
	if epoch > s.epoch {
		s.epoch = epoch
		s.buffered = make(map[Commit]struct{})
	}
	s.known = true
	s.online = true
	s.watermark = base
	refDrain(s)
	r.scan()
}

// Offline mirrors Coordinator.Offline.
func (r *Reference) Offline(shard ShardID) {
	s := r.shardFor(shard)
	s.online = false
	r.scan()
}

// Wait mirrors Coordinator.Wait.
func (r *Reference) Wait(target Commit, shards []ShardID, deadline Tick) ReqID {
	r.next++
	r.reqs[r.next] = &request{id: r.next, target: target, shards: normalize(shards), deadline: deadline}
	r.scan()
	return r.next
}

// Cancel mirrors Coordinator.Cancel.
func (r *Reference) Cancel(id ReqID) bool {
	if _, ok := r.reqs[id]; !ok {
		return false
	}
	delete(r.reqs, id)
	r.results = append(r.results, Result{Req: id, Reason: ReasonCancelled})
	return true
}

// JudgeTimeouts mirrors Coordinator.JudgeTimeouts: readiness is re-checked
// first, so progress applied at the deadline tick still wins.
func (r *Reference) JudgeTimeouts() {
	r.scan() // grant anything that became ready before judging
	now := r.clock.Now()
	for _, id := range r.sortedPending() {
		req := r.reqs[id]
		if now >= req.deadline {
			delete(r.reqs, id)
			r.results = append(r.results, Result{Req: id, Reason: ReasonTimeout, Lagging: r.lagging(req)})
		}
	}
}

// scan is the full scan: check every pending request, grant the ready ones
// and fail the ones waiting on an offline shard. It loops because a grant
// never changes another request's readiness, but one pass is enough.
func (r *Reference) scan() {
	for _, id := range r.sortedPending() {
		req := r.reqs[id]
		if r.ready(req) {
			delete(r.reqs, id)
			view := View{Commit: req.target, Shards: append([]ShardID(nil), req.shards...)}
			r.results = append(r.results, Result{Req: id, Granted: true, View: view})
			continue
		}
		if r.blockedOffline(req) {
			delete(r.reqs, id)
			r.results = append(r.results, Result{Req: id, Reason: ReasonOffline, Lagging: r.lagging(req)})
		}
	}
}

func (r *Reference) sortedPending() []ReqID {
	ids := make([]ReqID, 0, len(r.reqs))
	for id := range r.reqs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (r *Reference) ready(req *request) bool {
	for _, sh := range req.shards {
		s, ok := r.shards[sh]
		if !ok || !s.online || !s.known || s.watermark < req.target {
			return false
		}
	}
	return true
}

func (r *Reference) blockedOffline(req *request) bool {
	for _, sh := range req.shards {
		if s, ok := r.shards[sh]; ok && !s.online {
			return true
		}
	}
	return false
}

func (r *Reference) lagging(req *request) []Lagging {
	var out []Lagging
	for _, sh := range req.shards {
		s, ok := r.shards[sh]
		switch {
		case !ok || !s.online || !s.known:
			out = append(out, Lagging{Shard: sh, Known: false})
		case s.watermark < req.target:
			out = append(out, Lagging{Shard: sh, Known: true, Progress: s.watermark, Gap: req.target - s.watermark})
		}
	}
	return out
}

// DrainResults mirrors Coordinator.DrainResults.
func (r *Reference) DrainResults() []Result {
	out := r.results
	r.results = nil
	return out
}

// Stats mirrors Coordinator.Stats except it has no wait index to count.
func (r *Reference) Stats() Stats {
	st := Stats{Shards: len(r.shards), PendingRequests: len(r.reqs)}
	for _, s := range r.shards {
		st.BufferedReports += len(s.buffered)
	}
	return st
}
