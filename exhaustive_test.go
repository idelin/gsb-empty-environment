package netsec

import (
	"math/rand/v2"
	"sort"
	"testing"
)

// naiveEval 是刻意不做记忆化的独立求值器，用于交叉验证 DAG 求值，
// 同时天然在共享子式上重复展开。
func naiveEval(r *Rule, facts map[string]Value) Value {
	switch r.Op() {
	case opFact:
		if v, ok := facts[r.FactID()]; ok {
			return v
		}
		return Unknown
	case opNot:
		return Not(naiveEval(r.Children()[0], facts))
	case opAnd:
		vs := make([]Value, len(r.Children()))
		for i, child := range r.Children() {
			vs[i] = naiveEval(child, facts)
		}
		return And(vs...)
	case opOr:
		vs := make([]Value, len(r.Children()))
		for i, child := range r.Children() {
			vs[i] = naiveEval(child, facts)
		}
		return Or(vs...)
	default:
		panic("bad op")
	}
}

func reachableFactIDs(r *Rule) []string {
	seen := map[string]bool{}
	var walk func(*Rule)
	walk = func(node *Rule) {
		if node.Op() == opFact {
			seen[node.FactID()] = true
			return
		}
		for _, child := range node.Children() {
			walk(child)
		}
	}
	walk(r)
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// bruteForceMinExplain 独立暴力枚举全部 2^n 子集，返回与 Explain 相同口径
// （规模最小、同规模字典序最小）的解释。
func bruteForceMinExplain(r *Rule, facts map[string]Value, ids []string) []string {
	target := Decide(naiveEval(r, facts))
	known := make([]string, 0, len(ids))
	for _, id := range ids {
		if v, ok := facts[id]; ok && v != Unknown {
			known = append(known, id)
		}
	}
	sort.Strings(known)

	var best []string
	for mask := 0; mask < 1<<len(known); mask++ {
		subset := make([]string, 0)
		reduced := map[string]Value{}
		for i, id := range known {
			if mask&(1<<i) != 0 {
				subset = append(subset, id)
				reduced[id] = facts[id]
			}
		}
		if Decide(naiveEval(r, reduced)) != target {
			continue
		}
		if best == nil || len(subset) < len(best) ||
			(len(subset) == len(best) && lessStrings(subset, best)) {
			best = subset
		}
	}
	return best
}

func lessStrings(a, b []string) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// allAssignments 对 ids 生成全部 4^n 赋值：缺省、unknown、pass、fail。
func allAssignments(ids []string) []map[string]Value {
	out := make([]map[string]Value, 0, pow4(len(ids)))
	var gen func(int, map[string]Value)
	gen = func(i int, cur map[string]Value) {
		if i == len(ids) {
			cp := make(map[string]Value, len(cur))
			for k, v := range cur {
				cp[k] = v
			}
			out = append(out, cp)
			return
		}
		id := ids[i]
		gen(i+1, cur) // 缺省
		cur[id] = Unknown
		gen(i+1, cur)
		cur[id] = Pass
		gen(i+1, cur)
		cur[id] = Fail
		gen(i+1, cur)
		delete(cur, id)
	}
	gen(0, map[string]Value{})
	return out
}

func pow4(n int) int {
	v := 1
	for i := 0; i < n; i++ {
		v *= 4
	}
	return v
}

// randomSharedDAG 随机构造带共享子式的 DAG。每一步都把上一步结果与锚点
// 重新组合，因此整条链都从根可达，锚点在可达图中稳定拥有多个父节点；
// 等价构造的重复调用由 Compiler 结构归并。
func randomSharedDAG(rng *rand.Rand, n int) (*Compiler, *Rule, int) {
	c := NewCompiler()
	pool := make([]*Rule, 0)
	for i := 0; i < n; i++ {
		pool = append(pool, c.Fact("f"+itoa(i)))
	}
	const constructions = 8

	// 锚点：被反复引用的共享子式。
	var anchor *Rule
	if n == 1 {
		anchor = c.Or(pool[0], c.Not(pool[0]))
	} else {
		i, j := rng.IntN(n), rng.IntN(n-1)
		if j >= i {
			j++
		}
		anchor = c.And(pool[i], pool[j])
	}
	pool = append(pool, anchor)

	// 逐步增长的可达链：root_k = op(root_{k-1}, anchor)，
	// 交替 AND/OR 使每一步结构不同，锚点被每个新节点引用。
	root := anchor
	for step := 0; step < constructions; step++ {
		// 用 NOT 包裹锚点，避免与锚点同构而被去重压扁。
		if step%2 == 0 {
			root = c.And(root, c.Not(anchor))
		} else {
			root = c.Or(root, c.Not(anchor))
		}
		pool = append(pool, root)
	}

	// 显式重复一次等价构造，验证结构归并返回同一节点。
	again := c.And(anchor, pool[0])
	if again != c.And(anchor, pool[0]) {
		panic("equivalent construction must be interned to the same node")
	}

	return c, root, n + 1 + constructions
}

func TestExhaustiveSmallDAGs(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 7))
	for iter := 0; iter < 100; iter++ {
		n := 1 + rng.IntN(4) // 1..4 个事实
		_, root, _ := randomSharedDAG(rng, n)
		ids := reachableFactIDs(root)

		for _, facts := range allAssignments(ids) {
			// 1. 记忆化求值与朴素求值一致。
			if got, want := root.Eval(facts), naiveEval(root, facts); got != want {
				t.Fatalf("iter %d facts %v: eval %v != naive %v", iter, facts, got, want)
			}

			// 2. Explain 与独立暴力枚举一致（含同规模字典序决胜）。
			ex := root.Explain(facts)
			wantFacts := bruteForceMinExplain(root, facts, ids)
			if !equalSlices(ex.Facts, wantFacts) {
				t.Fatalf("iter %d facts %v: explain %v != brute force %v",
					iter, facts, ex.Facts, wantFacts)
			}

			// 3. 最小解释确实保持当前决策。
			reduced := map[string]Value{}
			for i, id := range ex.Facts {
				reduced[id] = ex.Values[i]
			}
			if root.Decide(reduced) != ex.Decision {
				t.Fatalf("iter %d: reduced facts change decision", iter)
			}

			// 4. 任何真子集都不足以维持决策（最小性）。
			for i := range ex.Facts {
				smaller := map[string]Value{}
				for j, id := range ex.Facts {
					if i != j {
						smaller[id] = ex.Values[j]
					}
				}
				if root.Decide(smaller) == ex.Decision {
					t.Fatalf("iter %d: explanation %v is not minimal", iter, ex.Facts)
				}
			}
		}
	}
}

func TestSharingActuallyHappens(t *testing.T) {
	rng := rand.New(rand.NewPCG(99, 1))
	sharedCount := 0
	for iter := 0; iter < 100; iter++ {
		_, root, nominalCount := randomSharedDAG(rng, 4)
		reachable := map[*Rule]bool{}
		var walk func(*Rule)
		walk = func(node *Rule) {
			if reachable[node] {
				return
			}
			reachable[node] = true
			for _, child := range node.Children() {
				walk(child)
			}
		}
		walk(root)

		// 实际可达节点数少于名义构造数，说明结构归并确实发生。
		if len(reachable) < nominalCount {
			sharedCount++
		}

		// 统计入度（DAG 需按节点去重遍历）：存在多父节点即证明子式被共享。
		indegree := map[*Rule]int{}
		visited := map[*Rule]bool{}
		var count func(*Rule)
		count = func(node *Rule) {
			if visited[node] {
				return
			}
			visited[node] = true
			for _, child := range node.Children() {
				indegree[child]++
				count(child)
			}
		}
		count(root)
		hasShared := false
		for _, deg := range indegree {
			if deg > 1 {
				hasShared = true
				break
			}
		}
		if !hasShared {
			t.Fatalf("iter %d: expected at least one shared subexpression", iter)
		}
	}
	if sharedCount == 0 {
		t.Fatal("structural interning never reduced node count")
	}
}

func TestTwentyFacts(t *testing.T) {
	c := NewCompiler()
	facts := make([]*Rule, MaxFacts)
	for i := range facts {
		facts[i] = c.Fact("f" + itoa(i))
	}
	root := c.And(facts...)

	// 单个 fail 即可短路为 reject，只需枚举规模 0、1。
	assignment := map[string]Value{}
	for _, r := range facts {
		assignment[r.FactID()] = Pass
	}
	assignment["f0"] = Fail
	ex := root.Explain(assignment)
	if ex.Decision != Reject || ex.Size != 1 || ex.Facts[0] != "f0" {
		t.Fatalf("single-fail AND explanation = %+v", ex)
	}

	if got, want := root.Eval(assignment), naiveEval(root, assignment); got != want {
		t.Fatalf("20-fact eval mismatch %v != %v", got, want)
	}
}

func TestWorstCaseExplainTenFacts(t *testing.T) {
	c := NewCompiler()
	facts := make([]*Rule, 10)
	for i := range facts {
		facts[i] = c.Fact("f" + itoa(i))
	}
	root := c.And(facts...)
	assignment := map[string]Value{}
	for _, r := range facts {
		assignment[r.FactID()] = Pass
	}
	ex := root.Explain(assignment)
	if ex.Decision != Allow || ex.Size != 10 {
		t.Fatalf("all-pass AND needs all 10 facts, got %+v", ex)
	}

	ids := reachableFactIDs(root)
	if want := bruteForceMinExplain(root, assignment, ids); !equalSlices(ex.Facts, want) {
		t.Fatalf("worst-case explain %v != brute force %v", ex.Facts, want)
	}
}

func TestCombinationEnumerationLexicographic(t *testing.T) {
	items := []string{"a", "b", "c"}
	var got [][]string
	forEachCombination(items, 2, func(combo []string) bool {
		got = append(got, append([]string(nil), combo...))
		return true
	})
	want := [][]string{{"a", "b"}, {"a", "c"}, {"b", "c"}}
	if len(got) != len(want) {
		t.Fatalf("combinations = %v, want %v", got, want)
	}
	for i := range want {
		if !equalSlices(got[i], want[i]) {
			t.Fatalf("combinations = %v, want %v", got, want)
		}
	}
}

func BenchmarkExplainTwentyFactsWorstCase(b *testing.B) {
	c := NewCompiler()
	facts := make([]*Rule, MaxFacts)
	for i := range facts {
		facts[i] = c.Fact("f" + itoa(i))
	}
	root := c.And(facts...)
	assignment := map[string]Value{}
	for _, r := range facts {
		assignment[r.FactID()] = Pass
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		root.Explain(assignment)
	}
}

func equalSlices(a, b []string) bool {
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
