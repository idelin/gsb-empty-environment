package lpc

import (
	"math"
	"math/big"
	"sort"
	"testing"
)

func ratFloat(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

func TestClassicFiveSymbols(t *testing.T) {
	// Frequencies {1,2,4,8,16}: the Kraft-complete optimum is the
	// classic Huffman tree {4,4,3,2,1}; e.g. {3,3,3,2,1} would be
	// cheaper-looking but sums to 9/8 > 1 and is not a prefix code.
	weights := []uint64{1, 2, 4, 8, 16}
	rep, err := Build(weights, 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []int{4, 4, 3, 2, 1}
	if !intsEqual(rep.Code.Lengths, want) {
		t.Fatalf("lengths = %v, want %v", rep.Code.Lengths, want)
	}
	if ratFloat(rep.Code.Kraft) != 1 {
		t.Fatalf("Kraft = %v, want 1", rep.Code.Kraft)
	}
	// Weighted cost = (1*4+2*4+4*3+8*2+16*1)/31 = 56/31.
	if rep.CostNum.Cmp(big.NewInt(56)) != 0 || rep.CostDen.Cmp(big.NewInt(31)) != 0 {
		t.Fatalf("cost = %v/%v, want 56/31", rep.CostNum, rep.CostDen)
	}
	assertPrefixFree(t, rep.Code)
}

func TestInfeasible(t *testing.T) {
	cases := []struct {
		n, L int
	}{
		{5, 2},  // 2^2 = 4 < 5
		{9, 3},  // 2^3 = 8 < 9
		{2, 0},  // L = 0 holds only one symbol
		{63, 5}, // 2^5 = 32 < 63
	}
	for _, tc := range cases {
		weights := make([]uint64, tc.n)
		for i := range weights {
			weights[i] = uint64(i + 1)
		}
		if _, err := Build(weights, tc.L); err != ErrInfeasible {
			t.Fatalf("n=%d L=%d: err = %v, want ErrInfeasible", tc.n, tc.L, err)
		}
	}
}

func TestLengthBoundBinds(t *testing.T) {
	// Skewed weights {100,1,1,1} want lengths {1,3,3,2} from plain Huffman,
	// but L=2 forces {2,2,2,2}; a truncated Huffman tree would instead
	// collide on the short code words.
	weights := []uint64{100, 1, 1, 1}
	rep, err := Build(weights, 2)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := []int{2, 2, 2, 2}
	if !intsEqual(rep.Code.Lengths, want) {
		t.Fatalf("lengths = %v, want %v", rep.Code.Lengths, want)
	}
	if ratFloat(rep.Code.Kraft) != 1 {
		t.Fatalf("Kraft = %v", rep.Code.Kraft)
	}
	if rep.CostNum.Cmp(big.NewInt(206)) != 0 { // 2*(100+1+1+1)
		t.Fatalf("cost num = %v, want 206", rep.CostNum)
	}
	assertPrefixFree(t, rep.Code)
}

func TestMatchesPlainHuffmanWhenLimitLoose(t *testing.T) {
	weights := []uint64{5, 9, 12, 13, 16, 45}
	rep, err := Build(weights, MaxLength)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	want, _ := huffmanLengths(weights)
	if !intsEqual(rep.Code.Lengths, want) {
		t.Fatalf("lengths = %v, want Huffman %v", rep.Code.Lengths, want)
	}
	if ratFloat(rep.Code.Kraft) != 1 {
		t.Fatalf("Kraft = %v", rep.Code.Kraft)
	}
	assertPrefixFree(t, rep.Code)
}

func TestOptimalByExhaustiveDP(t *testing.T) {
	// Enumerate every feasible length vector and confirm package-merge
	// attains the minimum weighted sum. Kept to small alphabets.
	type tc struct {
		weights []uint64
		L       int
	}
	cases := []tc{
		{[]uint64{100, 1, 1, 1}, 2},
		{[]uint64{100, 1, 1, 1}, 3},
		{[]uint64{3, 1, 4, 1, 5}, 3},
		{[]uint64{1, 1, 1, 1, 1, 1}, 2}, // infeasible
		{[]uint64{2, 7, 1, 8, 2, 8}, 3},
		{[]uint64{1, 2, 3}, 1},
	}
	for _, c := range cases {
		rep, err := Build(c.weights, c.L)
		best := bruteForceMinCost(c.weights, c.L)
		if best == nil {
			if err != ErrInfeasible {
				t.Fatalf("weights=%v L=%d: want ErrInfeasible, got %v", c.weights, c.L, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("weights=%v L=%d: %v", c.weights, c.L, err)
		}
		if rep.CostNum.Cmp(best) != 0 {
			t.Fatalf("weights=%v L=%d: cost %v, optimal %v", c.weights, c.L, rep.CostNum, best)
		}
		if ratFloat(rep.Code.Kraft) != 1 {
			t.Fatalf("Kraft = %v", rep.Code.Kraft)
		}
		for s, l := range rep.Code.Lengths {
			if l > c.L {
				t.Fatalf("symbol %d length %d exceeds L=%d", s, l, c.L)
			}
		}
	}
}

func TestZeroWeightSymbols(t *testing.T) {
	weights := []uint64{10, 0, 5, 0}
	rep, err := Build(weights, 2)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(rep.Code.Lengths) != 4 {
		t.Fatalf("missing symbols: %v", rep.Code.Lengths)
	}
	if ratFloat(rep.Code.Kraft) != 1 {
		t.Fatalf("Kraft = %v", rep.Code.Kraft)
	}
	assertPrefixFree(t, rep.Code)
}

func TestBadArguments(t *testing.T) {
	if _, err := Build(nil, 3); err != ErrBadArgument {
		t.Fatalf("empty alphabet: %v", err)
	}
	if _, err := Build([]uint64{0, 0}, 3); err != ErrBadArgument {
		t.Fatalf("all-zero weights: %v", err)
	}
	if _, err := Build([]uint64{1}, MaxLength+1); err != ErrBadArgument {
		t.Fatalf("limit too large: %v", err)
	}
	if _, err := Build([]uint64{1}, -1); err != ErrBadArgument {
		t.Fatalf("negative limit: %v", err)
	}
}

func TestSingleSymbol(t *testing.T) {
	rep, err := Build([]uint64{42}, 0)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rep.Code.Lengths[0] != 0 {
		t.Fatalf("length = %d, want 0", rep.Code.Lengths[0])
	}
	if ratFloat(rep.Code.Kraft) != 1 {
		t.Fatalf("Kraft = %v", rep.Code.Kraft)
	}
	if rep.CostNum.Sign() != 0 {
		t.Fatalf("cost = %v, want 0", rep.CostNum)
	}
}

// bruteForceMinCost enumerates all length vectors (each length in 1..L,
// plus the single-symbol special case) and returns the minimum weighted
// sum over exactly-complete prefix codes (sum 2^-l == 1). Returns nil when
// no feasible vector exists.
func bruteForceMinCost(weights []uint64, L int) *big.Int {
	n := len(weights)
	if n == 1 && L >= 0 {
		return big.NewInt(0)
	}
	if L == 0 {
		return nil
	}
	lengths := make([]int, n)
	for i := range lengths {
		lengths[i] = 1
	}
	var best *big.Int
	var rec func(i int, kraft float64)
	rec = func(i int, kraft float64) {
		if kraft > 1+1e-12 {
			return
		}
		if i == n {
			if math.Abs(kraft-1) > 1e-12 {
				return
			}
			cost := new(big.Int)
			for j, w := range weights {
				cost.Add(cost, new(big.Int).Mul(new(big.Int).SetUint64(w), big.NewInt(int64(lengths[j]))))
			}
			if best == nil || cost.Cmp(best) < 0 {
				best = new(big.Int).Set(cost)
			}
			return
		}
		for l := 1; l <= L; l++ {
			lengths[i] = l
			rec(i+1, kraft+math.Pow(0.5, float64(l)))
		}
	}
	rec(0, 0)
	return best
}

func assertPrefixFree(t *testing.T, c *Code) {
	t.Helper()
	type wc struct {
		w uint64
		l int
	}
	var all []wc
	for i := range c.Lengths {
		w, l, err := c.Word(i)
		if err != nil {
			t.Fatalf("Word(%d): %v", i, err)
		}
		all = append(all, wc{w, l})
	}
	for i := range all {
		for j := range all {
			if i == j || all[i].l != all[j].l {
				continue
			}
			if all[i].w == all[j].w {
				t.Fatalf("duplicate code word for %d and %d", i, j)
			}
		}
		for j := range all {
			if i == j || all[i].l >= all[j].l {
				continue
			}
			shift := uint(all[j].l - all[i].l)
			if all[i].w == (all[j].w >> shift) {
				t.Fatalf("word for %d is a prefix of word for %d", i, j)
			}
		}
	}
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- reference ordinary Huffman (for the loose-limit comparison) ----

type huffNode struct {
	sym      int
	freq     uint64
	children []*huffNode
}

func huffmanLengths(weights []uint64) ([]int, error) {
	n := len(weights)
	lengths := make([]int, n)
	if n == 1 {
		lengths[0] = 0
		return lengths, nil
	}
	var forest []*huffNode
	for i, w := range weights {
		forest = append(forest, &huffNode{sym: i, freq: w})
	}
	for len(forest) > 1 {
		sort.SliceStable(forest, func(i, j int) bool {
			if forest[i].freq != forest[j].freq {
				return forest[i].freq < forest[j].freq
			}
			return minSym(forest[i]) < minSym(forest[j])
		})
		a, b := forest[0], forest[1]
		merged := &huffNode{freq: a.freq + b.freq, children: []*huffNode{a, b}}
		forest = append(forest[2:], merged)
	}
	var walk func(nd *huffNode, depth int)
	walk = func(nd *huffNode, depth int) {
		if len(nd.children) == 0 {
			lengths[nd.sym] = depth
			return
		}
		for _, ch := range nd.children {
			walk(ch, depth+1)
		}
	}
	walk(forest[0], 0)
	return lengths, nil
}

func minSym(nd *huffNode) int {
	if len(nd.children) == 0 {
		return nd.sym
	}
	m := minSym(nd.children[0])
	for _, ch := range nd.children[1:] {
		if s := minSym(ch); s < m {
			m = s
		}
	}
	return m
}
