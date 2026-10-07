package netsim

// EnumReport summarizes an exhaustive exploration of the reachable state
// space of a small network under all possible send schedules.
type EnumReport struct {
	States            int
	Transitions       int
	SuccessTerminals  int // states where every packet is delivered
	DeadlockTerminals int // stuck states with undelivered packets
	DeadlockExample   *State
	MinLatency        map[int]int // per packet, over all successful terminals
	MaxLatency        map[int]int
	InvariantErrors   []string
	Overflow          bool // maxStates exceeded; exploration incomplete
}

// Enumerate explores every reachable state of the simulation by trying,
// at each tick, every non-empty compatible subset of candidate sends
// (the receive phase is deterministic and applied first). Requiring at
// least one send per tick models a scheduler that never idles while
// progress is possible; it keeps the explored space finite and does not
// hide deadlocks, which are states with no candidate sends at all.
//
// Intended for small networks: the state space grows quickly with edge
// capacity and packet count. Exploration stops and reports Overflow when
// maxStates is exceeded.
func (s *Sim) Enumerate(maxStates int) *EnumReport {
	rep := &EnumReport{
		MinLatency: map[int]int{},
		MaxLatency: map[int]int{},
	}
	visited := map[string]bool{}
	work := []*State{s.Receive(s.Initial())}
	for len(work) > 0 {
		st := work[len(work)-1]
		work = work[:len(work)-1]
		// Normalize: the receive phase is deterministic, so every
		// expanded state is post-receive. Receive is idempotent.
		st = s.Receive(st)
		if visited[s.Hash(st)] {
			continue
		}
		visited[s.Hash(st)] = true
		rep.States++
		if rep.States > maxStates {
			rep.Overflow = true
			return rep
		}
		rep.InvariantErrors = append(rep.InvariantErrors, s.CheckInvariants(st)...)
		if s.allDelivered(st) {
			rep.SuccessTerminals++
			for _, p := range s.Packets {
				lat := st.Delivered[p.ID] - p.InjectTick
				if cur, ok := rep.MinLatency[p.ID]; !ok || lat < cur {
					rep.MinLatency[p.ID] = lat
				}
				if lat > rep.MaxLatency[p.ID] {
					rep.MaxLatency[p.ID] = lat
				}
			}
			continue
		}
		cands := s.Candidates(st)
		if len(cands) == 0 {
			if nt, ok := s.nextInjectTick(st); ok {
				ns := st.Clone()
				ns.Tick = nt
				work = append(work, ns)
				rep.Transitions++
			} else {
				rep.DeadlockTerminals++
				if rep.DeadlockExample == nil {
					rep.DeadlockExample = st
				}
			}
			continue
		}
		for _, subset := range nonEmptySubsets(cands) {
			if !s.Compatible(st, subset) {
				continue
			}
			work = append(work, s.Apply(st, subset))
			rep.Transitions++
		}
	}
	return rep
}

// nonEmptySubsets enumerates all non-empty subsets of candidates. For
// more than 20 candidates it degrades to singletons to bound the blowup.
func nonEmptySubsets(cands []Move) [][]Move {
	n := len(cands)
	if n > 20 {
		out := make([][]Move, 0, n)
		for _, c := range cands {
			out = append(out, []Move{c})
		}
		return out
	}
	out := make([][]Move, 0, 1<<uint(n))
	for mask := 1; mask < (1 << uint(n)); mask++ {
		var sub []Move
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				sub = append(sub, cands[i])
			}
		}
		out = append(out, sub)
	}
	return out
}
