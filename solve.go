package plansel

import (
	"encoding/json"
	"math/bits"
	"sort"
	"time"
)

// Status describes how conclusive a Result is.
type Status int

const (
	// Optimal means the search completed; Lower == Upper == optimum.
	Optimal Status = iota
	// Feasible means the deadline stopped the search; Lower and Upper
	// bracket the optimum and the result carries a resume token.
	Feasible
)

func (s Status) String() string {
	if s == Optimal {
		return "Optimal"
	}
	return "Feasible"
}

// Result is the outcome of one Solve or Resume call.
type Result struct {
	Status   Status
	Selected []int // sorted IDs of all selected plans (committed included)
	Profit   int64 // total profit of Selected (committed plans included)
	Cost     int64 // cost charged against the current budget (sunk costs excluded)
	Lower    int64 // best proven total profit
	Upper    int64 // optimistic bound on total profit
	token    []byte
}

// ResumeToken returns an opaque token that continues an interrupted search.
// It is nil when Status is Optimal. The token is invalidated by any
// checkpoint revision (Commit, Prepare, Revise): old optimality proofs and
// bounds are never reused after the instance changes.
func (r Result) ResumeToken() []byte { return r.token }

// Solve solves the instance in one shot, without any committed state.
func Solve(inst Instance, deadline time.Time) (Result, error) {
	sess, err := NewSession(inst)
	if err != nil {
		return Result{}, err
	}
	return sess.Solve(deadline)
}

// frame is one unexplored node of the depth-first search frontier.
type frame struct {
	Idx    int    // next search position to decide
	Chosen uint64 // bitmask over search positions
	Cost   int64  // cost charged to the current budget so far
	Profit int64  // profit of chosen search plans so far
	Groups uint64 // activated group bits
}

// solver is a branch-and-bound over the non-forced plans. Costs of forced
// (committed) plans and prepared groups are sunk and never charged.
type solver struct {
	n          int
	ids        []int    // plan ID per search position
	cost       []int64  // plan cost per search position
	profit     []int64  // plan profit per search position
	reqMask    []uint64 // requirements within the search set, per position
	groupBit   []uint64 // group bit per search position
	fee        []int64  // group fee per search position
	free       []bool   // group already prepared per search position
	budget     int64
	posSum     []int64  // suffix sums of positive profits (relaxation bound)
	ahead      []uint64 // ahead[i] = bitmask of positions >= i
	baseProfit int64
	baseIDs    []int // sorted forced plan IDs

	stack []frame
	steps int

	hasBest    bool
	bestProfit int64
	bestCost   int64
	bestMask   uint64
}

// newSolver builds the search. committed plans (and their transitive
// requirements) are forced into every solution with sunk costs; prepared
// groups never charge a fee again.
func newSolver(inst Instance, committed, prepared map[int]bool) (*solver, error) {
	if err := inst.Validate(); err != nil {
		return nil, err
	}
	byID := make(map[int]Plan, len(inst.Plans))
	for _, p := range inst.Plans {
		byID[p.ID] = p
	}
	// Dependency closure of the committed set.
	forced := make(map[int]bool, len(committed))
	queue := make([]int, 0, len(committed))
	for id := range committed {
		if _, ok := byID[id]; !ok {
			return nil, errUnknownPlan(id)
		}
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if forced[id] {
			continue
		}
		forced[id] = true
		queue = append(queue, byID[id].Requires...)
	}

	s := &solver{budget: inst.Budget}
	groupBitOf := make(map[int]uint64)
	bitOf := func(g int) uint64 {
		b, ok := groupBitOf[g]
		if !ok {
			b = uint64(1) << uint(len(groupBitOf))
			groupBitOf[g] = b
		}
		return b
	}

	var initGroups uint64
	posOf := make(map[int]int) // plan ID -> search position
	for _, p := range inst.Plans {
		if forced[p.ID] {
			s.baseProfit += p.Profit
			s.baseIDs = append(s.baseIDs, p.ID)
			initGroups |= bitOf(p.Group)
			continue
		}
		posOf[p.ID] = s.n
		s.n++
		s.ids = append(s.ids, p.ID)
		s.cost = append(s.cost, p.Cost)
		s.profit = append(s.profit, p.Profit)
		s.groupBit = append(s.groupBit, bitOf(p.Group))
		s.fee = append(s.fee, inst.GroupFee[p.Group])
		s.free = append(s.free, prepared[p.Group])
	}
	sort.Ints(s.baseIDs)

	// Requirements restricted to the search set; requirements satisfied by
	// forced plans are dropped.
	s.reqMask = make([]uint64, s.n)
	for _, p := range inst.Plans {
		if forced[p.ID] {
			continue
		}
		i := posOf[p.ID]
		for _, r := range p.Requires {
			if !forced[r] {
				s.reqMask[i] |= uint64(1) << uint(posOf[r])
			}
		}
	}

	s.posSum = make([]int64, s.n+1)
	s.ahead = make([]uint64, s.n+1)
	for i := s.n - 1; i >= 0; i-- {
		s.posSum[i] = s.posSum[i+1]
		if s.profit[i] > 0 {
			s.posSum[i] += s.profit[i]
		}
		s.ahead[i] = s.ahead[i+1] | uint64(1)<<uint(i)
	}

	s.stack = []frame{{Idx: 0, Groups: initGroups}}
	// The empty selection (forced plans only) is always feasible and closed.
	s.hasBest = true
	return s, nil
}

// run explores the frontier until exhaustion or deadline. A zero deadline
// means no time limit.
func (s *solver) run(deadline time.Time) {
	timed := !deadline.IsZero()
	for len(s.stack) > 0 {
		f := s.stack[len(s.stack)-1]
		s.stack = s.stack[:len(s.stack)-1]
		if f.Profit+s.posSum[f.Idx] < s.bestProfit {
			continue // cannot reach nor tie the incumbent
		}
		if f.Idx == s.n {
			s.consider(f)
			continue
		}
		i := f.Idx
		// Exclude branch (pushed first so the include branch is explored first).
		s.stack = append(s.stack, frame{Idx: i + 1, Chosen: f.Chosen, Cost: f.Cost, Profit: f.Profit, Groups: f.Groups})
		// Include branch: every requirement must be chosen already or still ahead.
		if s.reqMask[i]&^(f.Chosen|s.ahead[i+1]) == 0 {
			nc := f.Cost + s.cost[i]
			ng := f.Groups
			if gb := s.groupBit[i]; ng&gb == 0 {
				if !s.free[i] {
					nc += s.fee[i] // shared preparation fee, charged once
				}
				ng |= gb
			}
			if nc <= s.budget {
				s.stack = append(s.stack, frame{Idx: i + 1, Chosen: f.Chosen | uint64(1)<<uint(i), Cost: nc, Profit: f.Profit + s.profit[i], Groups: ng})
			}
		}
		s.steps++
		if timed && s.steps&1023 == 0 && time.Now().After(deadline) {
			return
		}
	}
}

// consider evaluates a complete selection.
func (s *solver) consider(f frame) {
	for m := f.Chosen; m != 0; m &= m - 1 {
		j := bits.TrailingZeros64(m)
		if s.reqMask[j]&^f.Chosen != 0 {
			return // dependency closure violated
		}
	}
	if s.better(f.Profit, f.Cost, f.Chosen) {
		s.bestProfit, s.bestCost, s.bestMask = f.Profit, f.Cost, f.Chosen
	}
}

// better implements the dominance order: higher profit, then lower cost,
// then lexicographically smaller full ID set.
func (s *solver) better(profit, cost int64, mask uint64) bool {
	if profit != s.bestProfit {
		return profit > s.bestProfit
	}
	if cost != s.bestCost {
		return cost < s.bestCost
	}
	return lessIDs(s.fullIDs(mask), s.fullIDs(s.bestMask))
}

// fullIDs materializes the sorted ID set of forced plans plus the mask.
func (s *solver) fullIDs(mask uint64) []int {
	out := make([]int, 0, len(s.baseIDs)+s.n)
	out = append(out, s.baseIDs...)
	for m := mask; m != 0; m &= m - 1 {
		out = append(out, s.ids[bits.TrailingZeros64(m)])
	}
	sort.Ints(out)
	return out
}

// result assembles the Result from the current search state.
func (s *solver) result() Result {
	res := Result{
		Selected: s.fullIDs(s.bestMask),
		Profit:   s.baseProfit + s.bestProfit,
		Cost:     s.bestCost,
		Lower:    s.baseProfit + s.bestProfit,
	}
	if len(s.stack) == 0 {
		res.Status = Optimal
		res.Upper = res.Lower
		return res
	}
	res.Status = Feasible
	up := s.bestProfit
	for _, f := range s.stack {
		if u := f.Profit + s.posSum[f.Idx]; u > up {
			up = u
		}
	}
	res.Upper = s.baseProfit + up
	return res
}

// token is the serializable search frontier used to resume.
type token struct {
	Epoch      uint64
	Hash       uint64
	Stack      []frame
	BestProfit int64
	BestCost   int64
	BestMask   uint64
}

func (t token) encode() []byte {
	b, _ := json.Marshal(t)
	return b
}

func decodeToken(b []byte) (token, error) {
	var t token
	err := json.Unmarshal(b, &t)
	return t, err
}

// lessIDs compares sorted ID sets lexicographically.
func lessIDs(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
