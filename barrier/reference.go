package barrier

import "sort"

// RefCoordinator is the deliberately simple full-scan reference. It
// keeps the raw event log and, on every evaluation, replays the whole
// log and recomputes each shard's contiguous progress by scanning the
// full applied set from scratch. It exists to cross-check the indexed
// Coordinator; correctness over speed.

type refEventKind int

const (
	evReport refEventKind = iota
	evRestart
	evOffline
)

type refEvent struct {
	kind   refEventKind
	report Report
	shard  ShardID
	epoch  Epoch
	online bool
}

// naiveShard is the reference shard state: a plain set of applied
// commits plus the proof-guaranteed base.
type naiveShard struct {
	epoch    Epoch
	known    bool
	base     uint64
	applied  map[uint64]bool
	preProof []uint64
	online   bool
}

func (s *naiveShard) reset(epoch Epoch) {
	s.epoch = epoch
	s.known = false
	s.base = 0
	s.applied = make(map[uint64]bool)
	s.preProof = nil
	s.online = true
}

// contig recomputes contiguous progress with a full scan of the set.
func (s *naiveShard) contig() uint64 {
	n := s.base
	for s.applied[n+1] {
		n++
	}
	return n
}

type RefCoordinator struct {
	clock   *TestClock
	events  []refEvent
	reqs    map[ReqID]*Request
	nextReq ReqID
}

func NewRefCoordinator(clock *TestClock) *RefCoordinator {
	return &RefCoordinator{clock: clock, reqs: make(map[ReqID]*Request)}
}

func (r *RefCoordinator) Submit(target uint64, shards []ShardID, deadline Tick) (ReqID, *Outcome) {
	r.nextReq++
	id := r.nextReq
	cp := make([]ShardID, len(shards))
	copy(cp, shards)
	req := &Request{ID: id, Target: target, Shards: cp, Deadline: deadline}
	states := r.rebuild()
	if refSatisfied(states, req) {
		return id, grantOutcome(req)
	}
	if deadline <= r.clock.Now() {
		out := Outcome{Req: id, Kind: TimedOut, Lagging: refLagging(states, req)}
		return id, &out
	}
	r.reqs[id] = req
	return id, nil
}

func (r *RefCoordinator) HandleReport(rep Report) []Outcome {
	r.events = append(r.events, refEvent{kind: evReport, report: rep})
	return r.evaluate()
}

func (r *RefCoordinator) Restart(shard ShardID, epoch Epoch) {
	r.events = append(r.events, refEvent{kind: evRestart, shard: shard, epoch: epoch})
}

func (r *RefCoordinator) SetOffline(shard ShardID, online bool) {
	r.events = append(r.events, refEvent{kind: evOffline, shard: shard, online: online})
}

func (r *RefCoordinator) Advance(to Tick) []Outcome {
	r.clock.Advance(to)
	states := r.rebuild()
	now := r.clock.Now()
	var due []ReqID
	for id, req := range r.reqs {
		if req.Deadline <= now {
			due = append(due, id)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
	var out []Outcome
	for _, id := range due {
		req := r.reqs[id]
		out = append(out, Outcome{Req: id, Kind: TimedOut, Lagging: refLagging(states, req)})
		delete(r.reqs, id)
	}
	return out
}

func (r *RefCoordinator) Cancel(id ReqID) bool {
	if _, ok := r.reqs[id]; !ok {
		return false
	}
	delete(r.reqs, id)
	return true
}

// rebuild replays the entire event log from scratch.
func (r *RefCoordinator) rebuild() map[ShardID]*naiveShard {
	states := make(map[ShardID]*naiveShard)
	get := func(s ShardID) *naiveShard {
		st := states[s]
		if st == nil {
			st = &naiveShard{applied: make(map[uint64]bool)}
			states[s] = st
		}
		return st
	}
	for _, ev := range r.events {
		switch ev.kind {
		case evReport:
			rep := ev.report
			st := states[rep.Shard]
			switch {
			case st == nil:
				st = &naiveShard{online: true, applied: make(map[uint64]bool)}
				st.epoch = rep.Epoch
				st.known = true
				states[rep.Shard] = st
			case rep.Epoch < st.epoch:
				continue
			case rep.Epoch > st.epoch:
				st.reset(rep.Epoch)
			}
			if rep.Proof {
				st.known = true
				if rep.ProofContig > st.base {
					st.base = rep.ProofContig
				}
				for _, n := range st.preProof {
					st.applied[n] = true
				}
				st.preProof = nil
			}
			if st.known {
				for _, n := range rep.Applied {
					st.applied[n] = true
				}
			} else {
				st.preProof = append(st.preProof, rep.Applied...)
			}
		case evRestart:
			get(ev.shard).reset(ev.epoch)
		case evOffline:
			get(ev.shard).online = ev.online
		}
	}
	return states
}

func (r *RefCoordinator) evaluate() []Outcome {
	states := r.rebuild()
	ids := make([]ReqID, 0, len(r.reqs))
	for id := range r.reqs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []Outcome
	for _, id := range ids {
		req := r.reqs[id]
		if refSatisfied(states, req) {
			out = append(out, *grantOutcome(req))
			delete(r.reqs, id)
		}
	}
	return out
}

func refSatisfied(states map[ShardID]*naiveShard, req *Request) bool {
	for _, s := range req.Shards {
		st := states[s]
		if st == nil || !st.online || !st.known || st.contig() < req.Target {
			return false
		}
	}
	return true
}

func refLagging(states map[ShardID]*naiveShard, req *Request) []ShardGap {
	var gaps []ShardGap
	for _, s := range req.Shards {
		st := states[s]
		switch {
		case st == nil:
			gaps = append(gaps, ShardGap{Shard: s, Unknown: true})
		case !st.online:
			gaps = append(gaps, ShardGap{Shard: s, Contig: st.contig(), Offline: true, Unknown: !st.known})
		case !st.known:
			gaps = append(gaps, ShardGap{Shard: s, Contig: st.contig(), Unknown: true})
		case st.contig() < req.Target:
			gaps = append(gaps, ShardGap{Shard: s, Contig: st.contig(), Missing: req.Target - st.contig()})
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Shard < gaps[j].Shard })
	return gaps
}
