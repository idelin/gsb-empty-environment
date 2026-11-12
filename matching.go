package taskpkg

import (
	"math/bits"
	"sort"
)

type solver struct {
	old     []*blockInfo
	new     []*blockInfo
	base    [][]int
	opts    Options
	budget  int
	states  int
	aborted bool

	// Suffix lower-bound tables, ignoring move penalties and column conflicts.
	suffixOldLB [][]int // [i][j]: min base[k][j] for k in [i,nOld)
	sumOldMin   []int   // sum over k in [i,nOld) of min(delete, all new)

	assign  []int
	used    uint64
	best    int
	bestSet bool
	bestA   []int
}

func newSolver(oldNodes, newNodes []*blockInfo, base [][]int, opts Options, initial []int, initialCost int) *solver {
	nO, nN := len(oldNodes), len(newNodes)
	s := &solver{
		old:     oldNodes,
		new:     newNodes,
		base:    base,
		opts:    opts,
		budget:  opts.Budget,
		assign:  make([]int, nO),
		bestA:   append([]int(nil), initial...),
		best:    initialCost,
		bestSet: true,
	}
	for i := range s.assign {
		s.assign[i] = -1
	}

	s.sumOldMin = make([]int, nO+1)
	for i := nO - 1; i >= 0; i-- {
		m := opts.DeleteCost
		for j := 0; j < nN; j++ {
			if base[i][j] < m {
				m = base[i][j]
			}
		}
		s.sumOldMin[i] = s.sumOldMin[i+1] + m
	}

	s.suffixOldLB = make([][]int, nO+1)
	s.suffixOldLB[nO] = make([]int, nN)
	const inf = int(^uint(0) >> 2)
	for j := 0; j < nN; j++ {
		s.suffixOldLB[nO][j] = inf
	}
	for i := nO - 1; i >= 0; i-- {
		s.suffixOldLB[i] = make([]int, nN)
		for j := 0; j < nN; j++ {
			m := s.suffixOldLB[i+1][j]
			if base[i][j] < m {
				m = base[i][j]
			}
			s.suffixOldLB[i][j] = m
		}
	}
	return s
}

func (s *solver) run() {
	s.search(0, 0, 0)
}

func (s *solver) lowerBound(i int, used uint64, remainingNew int) int {
	oldCount := len(s.old) - i
	lb := s.sumOldMin[i]

	// Every unused new block is either inserted or matched to a remaining old
	// block. The latter costs at least suffixOldLB[i][j]. This is a relaxation.
	newLB := 0
	for j := 0; j < len(s.new); j++ {
		if used&(uint64(1)<<uint(j)) != 0 {
			continue
		}
		c := s.opts.InsertCost
		if s.suffixOldLB[i][j] < c {
			c = s.suffixOldLB[i][j]
		}
		newLB += c
	}
	if newLB > lb {
		lb = newLB
	}
	if remainingNew > oldCount {
		c := (remainingNew - oldCount) * s.opts.InsertCost
		if c > lb {
			lb = c
		}
	}
	return lb
}

func (s *solver) search(i int, used uint64, cost int) {
	if s.aborted {
		return
	}
	s.states++
	if s.states > s.budget {
		s.aborted = true
		return
	}

	nN := len(s.new)
	remainingNew := nN - bits.OnesCount64(used)
	if cost+s.lowerBound(i, used, remainingNew) >= s.best {
		return
	}
	if i == len(s.old) {
		total := cost + remainingNew*s.opts.InsertCost
		if s.bestSet && total >= s.best {
			return
		}
		s.best = total
		s.bestSet = true
		copy(s.bestA, s.assign)
		return
	}

	type candidate struct {
		j    int
		cost int
	}
	candidates := make([]candidate, 0, remainingNew)
	for j := range s.new {
		if used&(uint64(1)<<uint(j)) != 0 {
			continue
		}
		mc := s.base[i][j]
		if s.parentMoved(i, j) {
			mc += s.opts.MoveCost
		}
		candidates = append(candidates, candidate{j: j, cost: mc})
	}
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].cost != candidates[b].cost {
			return candidates[a].cost < candidates[b].cost
		}
		return candidates[a].j < candidates[b].j
	})

	for _, c := range candidates {
		s.assign[i] = c.j
		s.search(i+1, used|(uint64(1)<<uint(c.j)), cost+c.cost)
		s.assign[i] = -1
		if s.aborted {
			return
		}
	}

	// Deletion is the final branch. Parents are always visited before children
	// in preorder, so parent assignments needed by parentMoved are available.
	s.assign[i] = -1
	s.search(i+1, used, cost+s.opts.DeleteCost)
}

// parentMoved reports whether matching old i to new j changes the parent
// relationship. Parent matching is the identity relation: the old parent must
// itself be matched to the new parent.
func (s *solver) parentMoved(i, j int) bool {
	oldParent := s.old[i].activeParent
	newParent := s.new[j].activeParent
	if oldParent < 0 && newParent < 0 {
		return false
	}
	if oldParent < 0 || newParent < 0 {
		return true
	}
	return s.assign[oldParent] != newParent
}
