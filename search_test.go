package fuzzdict

import (
	"container/heap"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// ---------- 基准：字符串编辑图上的 Dijkstra 最短路 ----------

type distItem struct {
	s    string
	dist int
}

type distHeap []distItem

func (h distHeap) Len() int            { return len(h) }
func (h distHeap) Less(i, j int) bool  { return h[i].dist < h[j].dist }
func (h distHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *distHeap) Push(x interface{}) { *h = append(*h, x.(distItem)) }
func (h *distHeap) Pop() interface{} {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

// editGraphDistance 在字符串编辑图上求 src 到 dst 的最短路径。
// 图的顶点是所有长度 <= len(src)+len(dst) 的字符串，
// 边是单步插入、删除、替换、相邻交换（零代价边合法，可能成环）。
func editGraphDistance(src, dst string, c Costs) int {
	if src == dst {
		return 0
	}
	alphaSet := map[byte]bool{}
	for i := 0; i < len(src); i++ {
		alphaSet[src[i]] = true
	}
	for i := 0; i < len(dst); i++ {
		alphaSet[dst[i]] = true
	}
	var alphabet []byte
	for ch := range alphaSet {
		alphabet = append(alphabet, ch)
	}
	maxLen := len(src) + len(dst)

	dist := map[string]int{src: 0}
	pq := &distHeap{{src, 0}}
	heap.Init(pq)
	relax := func(v string, nd int) {
		if old, ok := dist[v]; !ok || nd < old {
			dist[v] = nd
			heap.Push(pq, distItem{v, nd})
		}
	}
	for pq.Len() > 0 {
		it := heap.Pop(pq).(distItem)
		if it.dist != dist[it.s] {
			continue
		}
		if it.s == dst {
			return it.dist
		}
		u := it.s
		// 删除
		for i := 0; i < len(u); i++ {
			relax(u[:i]+u[i+1:], it.dist+c.Delete)
		}
		// 替换
		for i := 0; i < len(u); i++ {
			for _, a := range alphabet {
				if a != u[i] {
					relax(u[:i]+string(a)+u[i+1:], it.dist+c.Replace)
				}
			}
		}
		// 插入
		if len(u) < maxLen {
			for i := 0; i <= len(u); i++ {
				for _, a := range alphabet {
					relax(u[:i]+string(a)+u[i:], it.dist+c.Insert)
				}
			}
		}
		// 相邻交换
		for i := 0; i+1 < len(u); i++ {
			if u[i] != u[i+1] {
				v := []byte(u)
				v[i], v[i+1] = v[i+1], v[i]
				relax(string(v), it.dist+c.Transpose)
			}
		}
	}
	return -1 // 不可达（理论上不会发生）
}

// bruteForceTopK 用编辑图距离复算前 K 名。
// 与库一致，距离定义为词 -> 查询串的变换代价。
func bruteForceTopK(pattern string, words []string, c Costs, k int) []Result {
	var res []Result
	for _, w := range words {
		res = append(res, Result{Word: w, Cost: editGraphDistance(w, pattern, c)})
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Cost != res[j].Cost {
			return res[i].Cost < res[j].Cost
		}
		return res[i].Word < res[j].Word
	})
	if len(res) > k {
		res = res[:k]
	}
	return res
}

func allStrings(alphabet string, maxLen int) []string {
	var out []string
	var rec func(cur string)
	rec = func(cur string) {
		if len(cur) > 0 {
			out = append(out, cur)
		}
		if len(cur) == maxLen {
			return
		}
		for i := 0; i < len(alphabet); i++ {
			rec(cur + string(alphabet[i]))
		}
	}
	rec("")
	return out
}

func checkResults(t *testing.T, got []Result, err error, want []Result, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: got %d results %v, want %d %v", ctx, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].Word != want[i].Word || got[i].Cost != want[i].Cost {
			t.Fatalf("%s: rank %d got %+v want %+v (got=%v want=%v)", ctx, i, got[i], want[i], got, want)
		}
		if !got[i].Proven {
			t.Fatalf("%s: rank %d (%+v) should be proven without budget limit", ctx, i, got[i])
		}
	}
}

// TestExhaustiveShortWords 穷举短词：所有词典词与查询串，
// 多组代价（含零代价环），K 含超出词数的情况。
//
// 所有代价集合都满足精确区间条件 2*Transpose >= Delete+Insert，
// 此时 Lowrance-Wagner 距离与字符串编辑图最短路一致（见 search.go）。
func TestExhaustiveShortWords(t *testing.T) {
	words := allStrings("ab", 3) // 14 个词
	patterns := append([]string{""}, allStrings("ab", 3)...)
	costSets := []Costs{
		{1, 1, 1, 1},
		{0, 0, 0, 0}, // 全零：零代价环，同词多条编辑路径
		{0, 0, 1, 0}, // 免费插入/删除/交换：大量零代价环
		{0, 1, 1, 1}, // 免费插入
		{1, 0, 1, 1}, // 免费删除
		{1, 1, 2, 1},
		{3, 1, 1, 2},
		{1, 3, 2, 2},
		{0, 0, 2, 3},
		{3, 2, 1, 3},
	}
	ks := []int{1, 3, 5, 14, 20, 100} // 14=词数，20/100 超出词数
	dict := New(words)
	for _, c := range costSets {
		for _, p := range patterns {
			for _, k := range ks {
				want := bruteForceTopK(p, words, c, k)
				got, err := dict.Search(Query{Pattern: p, K: k, Costs: c, MaxCost: -1, MaxExpansions: -1})
				ctx := fmt.Sprintf("costs=%+v pattern=%q k=%d", c, p, k)
				checkResults(t, got, err, want, ctx)
			}
		}
	}
}

// TestRandomAgainstGraph 随机词典、随机代价，与编辑图复算对比。
// 随机代价被钳制到精确区间 2*Transpose >= Delete+Insert 内。
func TestRandomAgainstGraph(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 150; iter++ {
		nWords := 1 + rng.Intn(25)
		var words []string
		for i := 0; i < nWords; i++ {
			l := 1 + rng.Intn(4)
			w := make([]byte, l)
			for j := range w {
				w[j] = "abc"[rng.Intn(3)]
			}
			words = append(words, string(w))
		}
		pl := rng.Intn(5)
		p := make([]byte, pl)
		for j := range p {
			p[j] = "abc"[rng.Intn(3)]
		}
		c := Costs{rng.Intn(4), rng.Intn(4), rng.Intn(4), rng.Intn(4)}
		if need := c.Delete + c.Insert; 2*c.Transpose < need {
			c.Transpose = (need + 1) / 2
		}
		k := 1 + rng.Intn(30)
		dict := New(words)
		uniq := map[string]bool{}
		var uw []string
		for _, w := range words {
			if !uniq[w] {
				uniq[w] = true
				uw = append(uw, w)
			}
		}
		want := bruteForceTopK(string(p), uw, c, k)
		got, err := dict.Search(Query{Pattern: string(p), K: k, Costs: c, MaxCost: -1, MaxExpansions: -1})
		ctx := fmt.Sprintf("iter=%d costs=%+v pattern=%q k=%d words=%v", iter, c, p, k, uw)
		checkResults(t, got, err, want, ctx)
	}
}

// TestUpperBoundOutsideExactRegime 在精确区间之外（2T < D+I），
// 库的距离必须是编辑图距离的上界，且结果内部自洽。
func TestUpperBoundOutsideExactRegime(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 100; iter++ {
		nWords := 1 + rng.Intn(15)
		var words []string
		for i := 0; i < nWords; i++ {
			l := 1 + rng.Intn(4)
			w := make([]byte, l)
			for j := range w {
				w[j] = "ab"[rng.Intn(2)]
			}
			words = append(words, string(w))
		}
		pl := rng.Intn(5)
		p := make([]byte, pl)
		for j := range p {
			p[j] = "ab"[rng.Intn(2)]
		}
		c := Costs{rng.Intn(4), rng.Intn(4), rng.Intn(4), rng.Intn(4)}
		dict := New(words)
		got, err := dict.Search(Query{Pattern: string(p), K: 100, Costs: c, MaxCost: -1, MaxExpansions: -1})
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range got {
			gd := editGraphDistance(r.Word, string(p), c)
			if r.Cost < gd {
				t.Fatalf("iter=%d costs=%+v pattern=%q: word %q cost %d < graph distance %d",
					iter, c, p, r.Word, r.Cost, gd)
			}
			if !r.Proven {
				t.Fatalf("iter=%d: rank %d not proven without budget", iter, i)
			}
			if i > 0 && (got[i-1].Cost > r.Cost || (got[i-1].Cost == r.Cost && got[i-1].Word > r.Word)) {
				t.Fatalf("iter=%d: results not sorted at rank %d", iter, i)
			}
		}
	}
}

// TestUnrestrictedDamerau 同一子串被多次编辑的情形：
// "ca" -> "ac"（交换）-> "abc"（插入），受限 OSA 会算成 3。
func TestUnrestrictedDamerau(t *testing.T) {
	dict := New([]string{"ca"})
	got, err := dict.Search(Query{Pattern: "abc", K: 1, Costs: Costs{1, 1, 1, 1}, MaxCost: -1, MaxExpansions: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Cost != 2 {
		t.Fatalf("want cost 2 for ca->abc, got %+v", got)
	}
}

// TestBudgetProvenRanks 预算逐步增加：已证明的名次必须与精确结果一致，
// 且 Proven 标记构成前缀。
func TestBudgetProvenRanks(t *testing.T) {
	words := []string{"abc", "abd", "ab", "babc", "ca", "cba", "aac", "bbb", "ac"}
	dict := New(words)
	c := Costs{1, 1, 1, 1}
	exact, err := dict.Search(Query{Pattern: "ab", K: 9, Costs: c, MaxCost: -1, MaxExpansions: -1})
	if err != nil {
		t.Fatal(err)
	}
	for budget := 0; budget < 40; budget++ {
		got, err := dict.Search(Query{Pattern: "ab", K: 9, Costs: c, MaxCost: -1, MaxExpansions: budget})
		if err != nil {
			t.Fatal(err)
		}
		seenUnproven := false
		for i, r := range got {
			if r.Proven {
				if seenUnproven {
					t.Fatalf("budget=%d: proven rank %d after unproven rank", budget, i)
				}
				if i >= len(exact) || exact[i].Word != r.Word || exact[i].Cost != r.Cost {
					t.Fatalf("budget=%d: proven rank %d = %+v, exact = %+v", budget, i, r, exact)
				}
			} else {
				seenUnproven = true
			}
		}
	}
	// 预算充足时应与精确结果完全一致且全部已证明。
	full, err := dict.Search(Query{Pattern: "ab", K: 9, Costs: c, MaxCost: -1, MaxExpansions: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != len(exact) {
		t.Fatalf("full budget: got %d want %d", len(full), len(exact))
	}
	for i := range exact {
		if full[i] != exact[i] {
			t.Fatalf("full budget rank %d: got %+v want %+v", i, full[i], exact[i])
		}
	}
}

// TestMaxCost 代价上限：超出上限的词不出现，且名次仍被正确证明。
func TestMaxCost(t *testing.T) {
	words := allStrings("ab", 3)
	dict := New(words)
	c := Costs{1, 1, 2, 1}
	for _, maxCost := range []int{0, 1, 2, 3} {
		got, err := dict.Search(Query{Pattern: "ab", K: 20, Costs: c, MaxCost: maxCost, MaxExpansions: -1})
		if err != nil {
			t.Fatal(err)
		}
		var want []Result
		for _, r := range bruteForceTopK("ab", words, c, len(words)) {
			if r.Cost <= maxCost {
				want = append(want, r)
			}
		}
		checkResults(t, got, err, want, fmt.Sprintf("maxCost=%d", maxCost))
	}
}

// TestEdgeCases 空词典、重复词、K=0、空查询串、负代价。
func TestEdgeCases(t *testing.T) {
	empty := New(nil)
	if got, err := empty.Search(Query{Pattern: "a", K: 5, Costs: Costs{1, 1, 1, 1}, MaxCost: -1, MaxExpansions: -1}); err != nil || len(got) != 0 {
		t.Fatalf("empty dict: got %v err %v", got, err)
	}
	dup := New([]string{"ab", "ab", "ab"})
	if dup.Len() != 1 {
		t.Fatalf("duplicate words not deduped: len=%d", dup.Len())
	}
	got, err := dup.Search(Query{Pattern: "ab", K: 5, Costs: Costs{1, 1, 1, 1}, MaxCost: -1, MaxExpansions: -1})
	if err != nil || len(got) != 1 || got[0].Word != "ab" || got[0].Cost != 0 {
		t.Fatalf("dup dict: got %v err %v", got, err)
	}
	if got, _ := dup.Search(Query{Pattern: "ab", K: 0, Costs: Costs{1, 1, 1, 1}, MaxCost: -1, MaxExpansions: -1}); len(got) != 0 {
		t.Fatalf("K=0: got %v", got)
	}
	if _, err := dup.Search(Query{Pattern: "ab", K: 1, Costs: Costs{-1, 1, 1, 1}, MaxCost: -1, MaxExpansions: -1}); err != ErrNegativeCosts {
		t.Fatalf("negative costs: want ErrNegativeCosts, got %v", err)
	}
	// 空查询串：代价 = 词长 * Delete。
	got, _ = New([]string{"a", "abc", "ab"}).Search(Query{Pattern: "", K: 3, Costs: Costs{1, 2, 1, 1}, MaxCost: -1, MaxExpansions: -1})
	want := []Result{{"a", 2, true}, {"ab", 4, true}, {"abc", 6, true}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("empty pattern: got %v want %v", got, want)
	}
}
