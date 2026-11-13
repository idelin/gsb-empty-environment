package netsec

import (
	"container/heap"
	"errors"
	"fmt"
	"math/big"
)

// Trail is a single concrete differential trail: Differences[i] is the
// difference entering round i (Differences[0] is the input difference)
// and Differences[len-1] is the output difference after the last round.
// Score is the exact rational product, over all active S-boxes of all
// rounds, of (DDT transition count)/16. Inactive S-boxes contribute the
// factor 16/16 = 1.
type Trail struct {
	Differences []uint16
	Score       *big.Rat
}

func (t *Trail) String() string {
	return fmt.Sprintf("trail %v score=%s (~%s)", t.Differences, t.Score.RatString(), t.Score.FloatString(8))
}

// SearchRequest parameterises a best-trail search.
//
// Starts is the non-empty set of allowed non-zero input differences and
// Targets the non-empty set of acceptable output differences. Budget is
// the maximum number of search nodes to expand; 0 means unlimited.
type SearchRequest struct {
	Starts  []uint16
	Targets []uint16
	Budget  uint64
}

// SearchResult reports the outcome of a best-trail search.
//
// Best is the highest-scoring trail found (nil if none exists). Among
// trails with equal maximal score the lexicographically smallest
// per-round difference integer sequence is reported; Ties counts how
// many trails share the maximal score.
//
// Optimal is true only when the search space was exhausted or every
// remaining branch was proven strictly worse than Best. UpperBound is
// an upper bound on the score of every trail not yet excluded; when
// the budget ran out (Optimal == false) Best and UpperBound have not
// closed and Best must not be called optimal.
type SearchResult struct {
	Optimal    bool
	Best       *Trail
	Ties       int
	UpperBound *big.Rat
	Expanded   uint64
	Remaining  int
}

// SearchTrails finds the maximum-score differential trail from any
// start difference to any target difference using best-first
// branch-and-bound with exact rational arithmetic.
func (c *Cipher) SearchTrails(req SearchRequest) (*SearchResult, error) {
	if len(req.Starts) == 0 {
		return nil, errors.New("netsec: start difference set must be non-empty")
	}
	for _, d := range req.Starts {
		if d == 0 {
			return nil, errors.New("netsec: start differences must be non-zero")
		}
	}
	if len(req.Targets) == 0 {
		return nil, errors.New("netsec: target difference set must be non-empty")
	}

	s := newSearcher(c, req)
	return s.run(), nil
}

type nibChoice struct {
	b   uint16
	cnt int64
}

type searchNode struct {
	depth int
	diff  uint16
	score *big.Rat
	bound *big.Rat
	path  []uint16
}

type nodeHeap []*searchNode

func (h nodeHeap) Len() int            { return len(h) }
func (h nodeHeap) Less(i, j int) bool  { return h[i].bound.Cmp(h[j].bound) > 0 }
func (h nodeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *nodeHeap) Push(x interface{}) { *h = append(*h, x.(*searchNode)) }
func (h *nodeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

type searcher struct {
	c       *Cipher
	rounds  int
	targets map[uint16]struct{}
	budget  uint64

	maxCnt  [16]int64
	maxOne  [StateSize]*big.Rat
	lastMax map[uint16]*big.Rat
	gMaxPow [MaxRounds]*big.Rat

	pq       nodeHeap
	best     *searchNode
	ties     int
	expanded uint64
}

func newSearcher(c *Cipher, req SearchRequest) *searcher {
	s := &searcher{
		c:       c,
		rounds:  c.rounds,
		targets: make(map[uint16]struct{}, len(req.Targets)),
		budget:  req.Budget,
		lastMax: make(map[uint16]*big.Rat),
	}
	for _, t := range req.Targets {
		s.targets[t] = struct{}{}
	}
	for a := 0; a < 16; a++ {
		var m uint8
		for b := 0; b < 16; b++ {
			if c.ddt[a][b] > m {
				m = c.ddt[a][b]
			}
		}
		s.maxCnt[a] = int64(m)
	}
	// gMax bounds the one-round transition probability of any non-zero
	// difference. Because intermediate differences are always non-zero
	// (the S-box is a bijection and the start differences are non-zero),
	// gMax = max over non-zero nibbles a of maxCnt[a]/16.
	var gNum int64
	for a := 1; a < 16; a++ {
		if s.maxCnt[a] > gNum {
			gNum = s.maxCnt[a]
		}
	}
	gMax := big.NewRat(gNum, 16)
	s.gMaxPow[0] = big.NewRat(1, 1)
	for k := 1; k < MaxRounds; k++ {
		s.gMaxPow[k] = new(big.Rat).Mul(s.gMaxPow[k-1], gMax)
	}

	one := big.NewRat(1, 1)
	for _, d := range req.Starts {
		n := &searchNode{
			depth: 0,
			diff:  d,
			score: new(big.Rat).Set(one),
			path:  []uint16{d},
		}
		n.bound = s.bound(n)
		heap.Push(&s.pq, n)
	}
	return s
}

// maxOneFor returns the maximum one-round transition probability of
// difference d, maximised over all possible output differences. The
// S-box layer decomposes per nibble, so the maximum is the product of
// the per-nibble maxima; the bit permutation does not change it.
func (s *searcher) maxOneFor(d uint16) *big.Rat {
	if r := s.maxOne[d]; r != nil {
		return r
	}
	prod := int64(1)
	for n := 0; n < NibbleCount; n++ {
		prod *= s.maxCnt[(d>>(4*n))&0xF]
	}
	r := big.NewRat(prod, StateSize)
	s.maxOne[d] = r
	return r
}

// lastMaxFor returns the exact maximum one-round transition probability
// of difference d into the target set.
func (s *searcher) lastMaxFor(d uint16) *big.Rat {
	if r, ok := s.lastMax[d]; ok {
		return r
	}
	best := new(big.Rat)
	for t := range s.targets {
		u := s.c.unpermTab[t]
		prod := int64(1)
		for n := 0; n < NibbleCount; n++ {
			prod *= int64(s.c.ddt[(d>>(4*n))&0xF][(u>>(4*n))&0xF])
		}
		if prod == 0 {
			continue
		}
		if r := big.NewRat(prod, StateSize); r.Cmp(best) > 0 {
			best = r
		}
	}
	s.lastMax[d] = best
	return best
}

// bound returns an admissible upper bound on the score of any trail
// extending the partial trail ending at n.
func (s *searcher) bound(n *searchNode) *big.Rat {
	rem := s.rounds - n.depth
	if rem <= 0 {
		return new(big.Rat).Set(n.score)
	}
	if rem == 1 {
		return new(big.Rat).Mul(n.score, s.lastMaxFor(n.diff))
	}
	b := new(big.Rat).Mul(n.score, s.maxOneFor(n.diff))
	return b.Mul(b, s.gMaxPow[rem-1])
}

func lessSeq(a, b []uint16) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func (s *searcher) run() *SearchResult {
	exhausted := false
	for s.pq.Len() > 0 {
		if s.budget > 0 && s.expanded >= s.budget {
			break
		}
		n := heap.Pop(&s.pq).(*searchNode)
		s.expanded++
		if s.best != nil && n.bound.Cmp(s.best.score) < 0 {
			// The queue is ordered by bound, so every remaining node is
			// provably strictly worse than the incumbent.
			exhausted = true
			break
		}
		if n.depth == s.rounds {
			s.acceptLeaf(n)
			continue
		}
		s.expand(n)
	}
	if s.pq.Len() == 0 {
		exhausted = true
	}

	res := &SearchResult{
		Optimal:   exhausted,
		Ties:      s.ties,
		Expanded:  s.expanded,
		Remaining: s.pq.Len(),
	}
	if exhausted {
		// Any nodes still queued after a pruning exit were proven
		// strictly worse than the incumbent, so nothing is left
		// unexcluded.
		res.Remaining = 0
	}
	ub := new(big.Rat)
	if s.best != nil {
		ub.Set(s.best.score)
		res.Best = &Trail{
			Differences: append([]uint16(nil), s.best.path...),
			Score:       new(big.Rat).Set(s.best.score),
		}
	}
	if !exhausted && s.pq.Len() > 0 {
		if top := s.pq[0].bound; top.Cmp(ub) > 0 {
			ub.Set(top)
		}
	}
	res.UpperBound = ub
	return res
}

func (s *searcher) acceptLeaf(n *searchNode) {
	switch {
	case s.best == nil || n.score.Cmp(s.best.score) > 0:
		s.best = n
		s.ties = 1
	case n.score.Cmp(s.best.score) == 0:
		s.ties++
		if lessSeq(n.path, s.best.path) {
			s.best = n
		}
	}
}

func (s *searcher) expand(n *searchNode) {
	var opts [NibbleCount][]nibChoice
	for nib := 0; nib < NibbleCount; nib++ {
		a := (n.diff >> (4 * nib)) & 0xF
		for b := 0; b < 16; b++ {
			if cnt := s.c.ddt[a][b]; cnt > 0 {
				opts[nib] = append(opts[nib], nibChoice{b: uint16(b), cnt: int64(cnt)})
			}
		}
	}
	last := n.depth+1 == s.rounds
	for _, c0 := range opts[0] {
		for _, c1 := range opts[1] {
			for _, c2 := range opts[2] {
				for _, c3 := range opts[3] {
					u := c0.b | c1.b<<4 | c2.b<<8 | c3.b<<12
					nd := s.c.permTab[u]
					if last {
						if _, ok := s.targets[nd]; !ok {
							continue
						}
					}
					prod := c0.cnt * c1.cnt * c2.cnt * c3.cnt
					child := &searchNode{
						depth: n.depth + 1,
						diff:  nd,
						score: new(big.Rat).Mul(n.score, big.NewRat(prod, StateSize)),
						path:  append(append([]uint16(nil), n.path...), nd),
					}
					child.bound = s.bound(child)
					if s.best != nil && child.bound.Cmp(s.best.score) < 0 {
						continue
					}
					heap.Push(&s.pq, child)
				}
			}
		}
	}
}
