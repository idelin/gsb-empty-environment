package gatetable

import (
	"fmt"
)

type candidate struct {
	offset int
	end    int // completion latency, offset+duration
	occ    bitset
}

type variable struct {
	frame int
	port  int
	cands []candidate
}

type solver struct {
	problem Problem
	vars    []variable
	base    []int // first variable index of each frame

	forbidden []bitset
	occ       []bitset // forbidden plus assigned copies
	busy      []bitset // assigned copies only
	assign    []int    // candidate index or -1
}

func newSolver(p Problem) (*solver, error) {
	s := &solver{
		problem:   p,
		base:      make([]int, len(p.Frames)),
		forbidden: make([]bitset, len(p.Ports)),
		occ:       make([]bitset, len(p.Ports)),
		busy:      make([]bitset, len(p.Ports)),
	}
	for pi, port := range p.Ports {
		for t, forbidden := range port.Forbidden {
			if forbidden {
				s.forbidden[pi].set(t)
			}
		}
	}

	for fi, frame := range p.Frames {
		s.base[fi] = len(s.vars)
		for i, port := range frame.Ports {
			dur := frame.Durations[i]
			v := variable{frame: fi, port: port}
			// Only [0,Period) is needed.  Replacing offset o by o-Period
			// leaves the circular port occupancy unchanged, strictly lowers
			// this copy's completion latency and cannot increase slot usage,
			// so offsets at or above one period are never optimal.
			for offset := 0; offset < p.Period; offset++ {
				end := offset + dur
				if end > frame.Deadline {
					continue
				}
				phase := (frame.Release + offset) % p.Period
				occ := circularBitMask(phase, dur, p.Period)
				if occ.intersects(s.forbidden[port]) {
					continue
				}
				v.cands = append(v.cands, candidate{
					offset: offset,
					end:    end,
					occ:    occ,
				})
			}
			if len(v.cands) == 0 {
				return nil, fmt.Errorf("%w: frame %d port %d has no legal offset",
					ErrUnschedulable, fi, port)
			}
			s.vars = append(s.vars, v)
		}
	}
	s.assign = make([]int, len(s.vars))
	return s, nil
}

func (s *solver) reset() {
	for i := range s.assign {
		s.assign[i] = -1
	}
	for p := range s.occ {
		s.occ[p] = s.forbidden[p]
		s.busy[p] = bitset{}
	}
}

func (s *solver) put(v, ci int) {
	s.assign[v] = ci
	c := s.vars[v].cands[ci]
	s.occ[s.vars[v].port].or(c.occ)
	s.busy[s.vars[v].port].or(c.occ)
}

func (s *solver) remove(v int) {
	ci := s.assign[v]
	c := s.vars[v].cands[ci]
	s.occ[s.vars[v].port].andNot(c.occ)
	s.busy[s.vars[v].port].andNot(c.occ)
	s.assign[v] = -1
}

func (s *solver) allowed(v, ci, lambda int) bool {
	c := s.vars[v].cands[ci]
	if c.end > lambda {
		return false
	}
	return !s.occ[s.vars[v].port].intersects(c.occ)
}

// lowerLatencies returns a componentwise lower bound on each frame's final
// completion latency.  For an unassigned port it uses the earliest completion
// among offsets that are still compatible with the current partial packing.
func (s *solver) lowerLatencies(lambda int) ([]int, bool) {
	n := len(s.problem.Frames)
	out := make([]int, n)
	seen := make([]bool, n)
	for v := range s.vars {
		f := s.vars[v].frame
		if s.assign[v] >= 0 {
			end := s.vars[v].cands[s.assign[v]].end
			if !seen[f] || end > out[f] {
				out[f] = end
			}
			seen[f] = true
			continue
		}
		best := -1
		for _, c := range s.vars[v].cands {
			if c.end > lambda {
				break
			}
			if !s.occ[s.vars[v].port].intersects(c.occ) {
				best = c.end
				break
			}
		}
		if best < 0 {
			return nil, false
		}
		if !seen[f] || best > out[f] {
			out[f] = best
		}
		seen[f] = true
	}
	for f := range out {
		if !seen[f] {
			return nil, false
		}
	}
	return out, true
}

func (s *solver) masks() []uint8 {
	t := s.problem.Period
	masks := make([]uint8, t)
	for p := range s.busy {
		for i := 0; i < t; i++ {
			if s.busy[p].test(i) {
				masks[i] |= uint8(1) << uint(p)
			}
		}
	}
	return masks
}

// tableLowerBound is a valid lower bound on the final number of circular mask
// runs.  At each tick the final mask must be a superset of the currently
// assigned bits.  Ignoring all remaining cross-tick coupling, a small DP finds
// the fewest internal transitions of any such completion.
func (s *solver) tableLowerBound() int {
	period := s.problem.Period
	portCount := len(s.problem.Ports)
	states := 1 << uint(portCount)
	cur := s.masks()

	const inf = int(^uint(0) >> 1)
	dp := make([]int, states)
	next := make([]int, states)
	for m := 0; m < states; m++ {
		if uint8(m)&cur[0] == cur[0] {
			dp[m] = 0
		} else {
			dp[m] = inf
		}
	}
	for t := 1; t < period; t++ {
		for m := range next {
			next[m] = inf
		}
		for m2 := 0; m2 < states; m2++ {
			if uint8(m2)&cur[t] != cur[t] {
				continue
			}
			for m1, cost := range dp {
				if cost == inf {
					continue
				}
				if m1 != m2 {
					cost++
				}
				if cost < next[m2] {
					next[m2] = cost
				}
			}
		}
		dp, next = next, dp
	}
	best := inf
	for _, cost := range dp {
		if cost < best {
			best = cost
		}
	}
	if best == inf {
		return inf
	}
	if best < 1 {
		best = 1
	}
	return best
}

// portRunLowerBound lower-bounds the number of transitions of the combined
// mask by looking at each port's one-bit bitmap.  A transition of any port bit
// necessarily changes the combined mask.  For a port we take the ticks that
// are already busy plus every still-allowed candidate tick as "possible".
// Maximal possible runs are separated by ticks that no completion can use.
// Each unassigned copy must choose a candidate inside one such run; a small
// set-cover DP finds the minimum number of runs that can cover all remaining
// copies plus the runs already made mandatory by assigned copies.
func (s *solver) portRunLowerBound(lambda int) int {
	period := s.problem.Period
	best := 0
	for port := range s.problem.Ports {
		possible := s.busy[port]
		var vars []int
		for v := range s.vars {
			if s.vars[v].port != port || s.assign[v] >= 0 {
				continue
			}
			vars = append(vars, v)
			for ci := range s.vars[v].cands {
				c := s.vars[v].cands[ci]
				if c.end > lambda {
					break
				}
				if c.occ.intersects(s.busy[port]) {
					continue
				}
				possible.or(c.occ)
			}
		}

		allTicks := true
		zero := -1
		for t := 0; t < period; t++ {
			if !possible.test(t) {
				allTicks = false
				zero = t
				break
			}
		}
		if allTicks {
			// One possible circular run; choosing all ticks busy would have
			// zero transitions.  This port contributes no positive bound.
			continue
		}
		if zero < 0 {
			continue
		}

		compID := make([]int, period)
		for i := range compID {
			compID[i] = -1
		}
		comps := 0
		for i := 0; i < period; i++ {
			pos := (zero + 1 + i) % period
			if !possible.test(pos) {
				continue
			}
			prev := (pos + period - 1) % period
			if i == 0 || !possible.test(prev) {
				compID[pos] = comps
				comps++
			} else {
				compID[pos] = comps - 1
			}
		}
		if comps == 0 {
			continue
		}

		m := len(vars)
		compMask := make([]uint16, comps)
		for vi, v := range vars {
			for ci := range s.vars[v].cands {
				c := s.vars[v].cands[ci]
				if c.end > lambda {
					break
				}
				if c.occ.intersects(s.busy[port]) {
					continue
				}
				cid := -1
				for t := 0; t < period; t++ {
					if c.occ.test(t) {
						cid = compID[t]
						break
					}
				}
				if cid >= 0 {
					compMask[cid] |= uint16(1) << uint(vi)
				}
			}
		}

		mandatory := make([]bool, comps)
		for t := 0; t < period; t++ {
			if s.busy[port].test(t) && compID[t] >= 0 {
				mandatory[compID[t]] = true
			}
		}
		covered := uint16(0)
		used := 0
		for c := 0; c < comps; c++ {
			if mandatory[c] {
				used++
				covered |= compMask[c]
			}
		}
		full := uint16(1)<<uint(m) - 1
		remaining := full &^ covered
		if remaining != 0 {
			const inf = int(^uint(0) >> 1)
			states := 1 << uint(m)
			dp := make([]int, states)
			for i := range dp {
				dp[i] = inf
			}
			dp[0] = 0
			for c := 0; c < comps; c++ {
				if mandatory[c] || compMask[c] == 0 {
					continue
				}
				next := append([]int(nil), dp...)
				cm := compMask[c]
				for mask, cost := range dp {
					if cost == inf {
						continue
					}
					nm := mask | int(cm)
					if cost+1 < next[nm] {
						next[nm] = cost + 1
					}
				}
				dp = next
			}
			if dp[remaining] == inf {
				return inf
			}
			used += dp[remaining]
		}
		if transitions := 2 * used; transitions > best {
			best = transitions
		}
	}
	return best
}

func (s *solver) boundsOK(lambda int) bool {
	latencies, ok := s.lowerLatencies(lambda)
	if !ok {
		return false
	}
	if s.problem.MaxSlots != Unlimited {
		if used := peakSlots(s.problem.Period, releasesOf(s.problem), latencies); used > s.problem.MaxSlots {
			return false
		}
	}
	if s.problem.MaxTableEntries != Unlimited {
		if s.portRunLowerBound(lambda) > s.problem.MaxTableEntries {
			return false
		}
		if s.tableLowerBound() > s.problem.MaxTableEntries {
			return false
		}
	}
	return true
}

func (s *solver) exactOK() bool {
	latencies := make([]int, len(s.problem.Frames))
	for v := range s.vars {
		ci := s.assign[v]
		if ci < 0 {
			return false
		}
		f := s.vars[v].frame
		end := s.vars[v].cands[ci].end
		if end > latencies[f] {
			latencies[f] = end
		}
	}
	if s.problem.MaxSlots != Unlimited {
		if used := peakSlots(s.problem.Period, releasesOf(s.problem), latencies); used > s.problem.MaxSlots {
			return false
		}
	}
	if s.problem.MaxTableEntries != Unlimited {
		if tableEntries(s.masks()) > s.problem.MaxTableEntries {
			return false
		}
	}
	return true
}

func releasesOf(p Problem) []int {
	out := make([]int, len(p.Frames))
	for i, f := range p.Frames {
		out[i] = f.Release
	}
	return out
}

// search decides feasibility.  It uses MRV variable ordering; the remaining
// fixed variables (if any) are already assigned.
func (s *solver) search(lambda int) bool {
	if !s.boundsOK(lambda) {
		return false
	}

	bestVar := -1
	bestCount := int(^uint(0) >> 1)
	for v := range s.vars {
		if s.assign[v] >= 0 {
			continue
		}
		count := 0
		for ci := range s.vars[v].cands {
			if s.allowed(v, ci, lambda) {
				count++
				if count >= bestCount {
					break
				}
			}
		}
		if count == 0 {
			return false
		}
		if count < bestCount {
			bestVar, bestCount = v, count
			if count == 1 {
				break
			}
		}
	}
	if bestVar < 0 {
		return s.exactOK()
	}

	for ci := range s.vars[bestVar].cands {
		if !s.allowed(bestVar, ci, lambda) {
			continue
		}
		s.put(bestVar, ci)
		if s.search(lambda) {
			s.remove(bestVar)
			return true
		}
		s.remove(bestVar)
	}
	return false
}

func (s *solver) feasible(lambda int) bool {
	s.reset()
	return s.search(lambda)
}

// lexSmallest returns the lexicographically smallest complete assignment at
// the given latency cap.  Variables are fixed in input order (frames, then the
// frame's egress-port order) and tried by ascending offset.
func (s *solver) lexSmallest(lambda int) []int {
	s.reset()
	for v := range s.vars {
		chosen := -1
		for ci := range s.vars[v].cands {
			if !s.allowed(v, ci, lambda) {
				continue
			}
			s.put(v, ci)
			if s.boundsOK(lambda) && s.search(lambda) {
				chosen = ci
				break
			}
			s.remove(v)
		}
		if chosen < 0 {
			return nil
		}
	}
	out := make([]int, len(s.vars))
	for v := range s.vars {
		out[v] = s.vars[v].cands[s.assign[v]].offset
	}
	return out
}

// solve finds the minimum achievable maximum frame latency, then the
// lexicographically smallest offsets attaining it.
func (s *solver) solve() ([]int, int, error) {
	lower := 0
	upper := 0
	for fi := range s.problem.Frames {
		frameMin := 0
		for v := range s.vars {
			if s.vars[v].frame != fi {
				continue
			}
			if s.vars[v].cands[0].end > frameMin {
				frameMin = s.vars[v].cands[0].end
			}
		}
		if frameMin > lower {
			lower = frameMin
		}
		if d := s.problem.Frames[fi].Deadline; d > upper {
			upper = d
		}
	}
	if lower < 1 {
		lower = 1
	}
	if !s.feasible(upper) {
		return nil, 0, fmt.Errorf("%w: budgets or port constraints exclude every schedule",
			ErrUnschedulable)
	}

	lo, hi := lower-1, upper
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if s.feasible(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	offsets := s.lexSmallest(hi)
	if offsets == nil {
		return nil, 0, fmt.Errorf("%w: internal search inconsistency", ErrUnschedulable)
	}
	return offsets, hi, nil
}
