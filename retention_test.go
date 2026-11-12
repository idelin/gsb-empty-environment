package netsec

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func at(day int) time.Time {
	return time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC)
}

func mustDeadline(t *testing.T, g *Graph, id string) time.Time {
	t.Helper()
	deadline, err := g.Deadline(id)
	if err != nil {
		t.Fatalf("Deadline(%q): %v", id, err)
	}
	return deadline
}

func assertDeadline(t *testing.T, g *Graph, id string, want time.Time) {
	t.Helper()
	got := mustDeadline(t, g, id)
	if !got.Equal(want) {
		t.Fatalf("node %q deadline = %v, want %v", id, got, want)
	}
}

// TestBasicPropagation 验证复制、合并、多级派生一律取最早来源期限，
// 且派生不会延长来源的保留期限。
func TestBasicPropagation(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(10)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddSource("s2", at(5)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("c1", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddMerge("m1", "c1", "s2"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddRebuild("r1", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("c2", "r1"); err != nil {
		t.Fatal(err)
	}

	assertDeadline(t, g, "c1", at(10))
	assertDeadline(t, g, "m1", at(5))
	assertDeadline(t, g, "r1", at(5))
	assertDeadline(t, g, "c2", at(5))

	// 收紧 s1 后，整条派生链都应同步收紧。
	if err := g.SetSourceDeadline("s1", at(3)); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "c1", at(3))
	assertDeadline(t, g, "m1", at(3))
	assertDeadline(t, g, "c2", at(3))
}

// TestUnconstrainedSources 验证零值期限表示无约束，min 语义正确。
func TestUnconstrainedSources(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("open", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("c1", "open"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "c1", time.Time{})

	if err := g.AddSource("s1", at(8)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddMerge("m1", "c1", "s1"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "m1", at(8))
}

// TestAnonymizeWithoutAttestation 验证没有有效证明时匿名化只是普通派生。
func TestAnonymizeWithoutAttestation(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(7)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAnonymize("a1", []string{"s1"}, "tag-x", at(30)); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "a1", at(7))

	info, err := g.Node("a1")
	if err != nil {
		t.Fatal(err)
	}
	if info.IsIndependent {
		t.Fatal("anonymized node must not be independent without an issued attestation")
	}

	// 没有标签的匿名化节点不能申请独立期限。
	if err := g.AddAnonymize("a2", []string{"s1"}, "", at(30)); !errors.Is(err, ErrAnonymizeRequired) {
		t.Fatalf("want ErrAnonymizeRequired, got %v", err)
	}
}

// TestAttestationIssueAndRevoke 验证证明签发后切换独立期限、撤销后恢复普通派生。
func TestAttestationIssueAndRevoke(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(7)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAnonymize("a1", []string{"s1"}, "tag-x", at(30)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("down", "a1"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "down", at(7))

	g.IssueAttestation("tag-x")
	assertDeadline(t, g, "a1", at(30))
	assertDeadline(t, g, "down", at(30))
	info, _ := g.Node("a1")
	if !info.IsIndependent {
		t.Fatal("a1 should be independent after attestation is issued")
	}

	// 证明有效期间，来源收紧不再影响屏障节点及其下游。
	if err := g.SetSourceDeadline("s1", at(2)); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "a1", at(30))
	assertDeadline(t, g, "down", at(30))

	g.RevokeAttestation("tag-x")
	assertDeadline(t, g, "a1", at(2))
	assertDeadline(t, g, "down", at(2))
	info, _ = g.Node("a1")
	if info.IsIndependent {
		t.Fatal("a1 must lose independent status after revocation")
	}

	// 撤销不存在的标签是空操作，重复撤销也安全。
	g.RevokeAttestation("does-not-exist")
	g.RevokeAttestation("tag-x")
	assertDeadline(t, g, "down", at(2))
}

// TestReissueAfterRevoke 验证撤销后重新签发可再次启用独立期限。
func TestReissueAfterRevoke(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(7)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAnonymize("a1", []string{"s1"}, "tag-x", at(30)); err != nil {
		t.Fatal(err)
	}
	g.IssueAttestation("tag-x")
	assertDeadline(t, g, "a1", at(30))
	g.RevokeAttestation("tag-x")
	assertDeadline(t, g, "a1", at(7))
	g.IssueAttestation("tag-x")
	assertDeadline(t, g, "a1", at(30))
}

// TestIndependentDeadlineEarlier 验证独立期限早于来源期限时下游采用独立期限。
func TestIndependentDeadlineEarlier(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(20)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAnonymize("a1", []string{"s1"}, "tag-x", at(9)); err != nil {
		t.Fatal(err)
	}
	g.IssueAttestation("tag-x")
	assertDeadline(t, g, "a1", at(9))
	if err := g.SetIndependentDeadline("a1", at(4)); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "a1", at(4))
	if err := g.SetIndependentDeadline("s1", at(4)); !errors.Is(err, ErrAnonymizeRequired) {
		t.Fatalf("want ErrAnonymizeRequired, got %v", err)
	}
}

// TestCyclePropagation 验证依赖循环中的节点收敛到最早来源期限，
// 循环本身不能延长任何来源的保留期限。
func TestCyclePropagation(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(10)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("a", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("b", "a"); err != nil {
		t.Fatal(err)
	}
	// 制造循环 a -> b -> a（a 额外依赖 b）。
	if err := g.AddEdge("b", "a"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "a", at(10))
	assertDeadline(t, g, "b", at(10))

	// 收紧来源后，循环内所有节点都必须同步收紧。
	if err := g.SetSourceDeadline("s1", at(6)); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "a", at(6))
	assertDeadline(t, g, "b", at(6))

	// 循环外的下游同样取最早期限。
	if err := g.AddCopy("tail", "b"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "tail", at(6))
}

// TestCycleWithAnonymizeBarrier 验证循环中的匿名化屏障在证明有效时锁定独立期限，
// 撤销后整个循环重新收敛到来源最早期限。
func TestCycleWithAnonymizeBarrier(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(10)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAnonymize("a", []string{"s1"}, "tag-x", at(20)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("b", "a"); err != nil {
		t.Fatal(err)
	}

	// 证明未签发：屏障不生效，循环收敛到来源期限。
	assertDeadline(t, g, "a", at(10))
	assertDeadline(t, g, "b", at(10))

	g.IssueAttestation("tag-x")
	assertDeadline(t, g, "a", at(20))
	assertDeadline(t, g, "b", at(20))

	g.RevokeAttestation("tag-x")
	assertDeadline(t, g, "a", at(10))
	assertDeadline(t, g, "b", at(10))
}

// TestSelfLoop 验证自环不会破坏推导与循环解释。
func TestSelfLoop(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(11)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("a", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("a", "a"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "a", at(11))
	explanation, err := g.Explain("a")
	if err != nil {
		t.Fatal(err)
	}
	if !explanation.InCycle || len(explanation.Cycles) != 1 {
		t.Fatalf("expected exactly one self-loop cycle, got %+v", explanation.Cycles)
	}
	cycle := explanation.Cycles[0]
	if len(cycle) != 2 || cycle[0] != "a" || cycle[1] != "a" {
		t.Fatalf("unexpected self-loop representation: %v", cycle)
	}
}

// TestLateSource 验证迟到来源：先建零期限来源与派生链，再补设期限，
// 以及通过 AddEdge 把新来源接入既有合并节点。
func TestLateSource(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("late", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := g.AddSource("early", at(12)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddMerge("m1", "late", "early"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("tail", "m1"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "m1", at(12))

	// 迟到来源补设一个更早的期限，立即收紧整条链。
	if err := g.SetSourceDeadline("late", at(4)); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "m1", at(4))
	assertDeadline(t, g, "tail", at(4))

	// 新登记的来源通过增量加边接入既有节点。
	if err := g.AddSource("newer", at(2)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("newer", "m1"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "m1", at(2))
	assertDeadline(t, g, "tail", at(2))

	// 加边引用不存在的节点必须报错。
	if err := g.AddEdge("ghost", "m1"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
	if err := g.AddMerge("broken", "ghost"); !errors.Is(err, ErrMissingSource) {
		t.Fatalf("want ErrMissingSource, got %v", err)
	}
}

// TestExplain 验证来源解释能区分决定来源与其他来源，并给出可验证的路径。
func TestExplain(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s-early", at(3)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddSource("s-late", at(9)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("c1", "s-early"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddMerge("m1", "c1", "s-late"); err != nil {
		t.Fatal(err)
	}

	explanation, err := g.Explain("m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(explanation.Determining) != 1 || explanation.Determining[0].SourceID != "s-early" {
		t.Fatalf("unexpected determining sources: %+v", explanation.Determining)
	}
	path := explanation.Determining[0].Path
	if len(path) != 3 || path[0] != "s-early" || path[1] != "c1" || path[2] != "m1" {
		t.Fatalf("unexpected determining path: %v", path)
	}
	if len(explanation.OtherSources) != 1 || explanation.OtherSources[0].SourceID != "s-late" {
		t.Fatalf("unexpected other sources: %+v", explanation.OtherSources)
	}

	// 独立期限节点的解释不再包含上游来源。
	if err := g.AddAnonymize("a1", []string{"m1"}, "tag-x", at(30)); err != nil {
		t.Fatal(err)
	}
	g.IssueAttestation("tag-x")
	independentExplanation, err := g.Explain("a1")
	if err != nil {
		t.Fatal(err)
	}
	if !independentExplanation.Independent || len(independentExplanation.Determining) != 0 {
		t.Fatalf("unexpected independent explanation: %+v", independentExplanation)
	}

	if _, err := g.Explain("missing"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
}

// TestExplainCycle 验证循环节点的解释会报告循环且来源路径仍然完整。
func TestExplainCycle(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(8)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("a", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddEdge("b", "a"); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"a", "b"} {
		explanation, err := g.Explain(id)
		if err != nil {
			t.Fatal(err)
		}
		if !explanation.InCycle {
			t.Fatalf("node %q should be reported as part of a cycle", id)
		}
		if len(explanation.Determining) != 1 || explanation.Determining[0].SourceID != "s1" {
			t.Fatalf("node %q missing determining source: %+v", id, explanation.Determining)
		}
		last := explanation.Determining[0].Path
		if last[0] != "s1" || last[len(last)-1] != id {
			t.Fatalf("node %q has invalid source path: %v", id, last)
		}
	}
}

// TestRebuildMatchesIncremental 验证全量 Rebuild 与增量传播得到完全一致的期限。
func TestRebuildMatchesIncremental(t *testing.T) {
	g := NewGraph()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.AddSource("n"+"aa", at(10)))
	must(g.AddSource("n"+"ab", at(20)))
	must(g.AddCopy("n"+"ac", "naa"))
	must(g.AddMerge("n"+"ad", "nac", "nab"))
	must(g.AddAnonymize("n"+"ae", []string{"nad"}, "tag-1", at(15)))
	must(g.AddCopy("n"+"af", "nae"))
	g.IssueAttestation("tag-1")
	must(g.AddEdge("naf", "nac")) // 引入循环
	must(g.SetSourceDeadline("naa", at(5)))

	before := make(map[string]time.Time)
	for _, id := range []string{"naa", "nab", "nac", "nad", "nae", "naf"} {
		before[id] = mustDeadline(t, g, id)
	}

	if g.Rebuild() != 6 {
		t.Fatal("Rebuild should report all nodes")
	}
	for id, want := range before {
		assertDeadline(t, g, id, want)
	}
}

// TestIncrementalVsFullRecomputeProperty 用随机操作序列验证：
// 任意增量状态与从零全量重算的结果始终一致（含循环与证明撤销）。
func TestIncrementalVsFullRecomputeProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	g := NewGraph()

	// 预先登记一批来源，保证后续派生总能找到已存在的上游。
	const sourceCount = 6
	for i := 0; i < sourceCount; i++ {
		id := fmtNodeID(i)
		deadline := time.Time{}
		if rng.Intn(2) == 0 {
			deadline = at(1 + rng.Intn(30))
		}
		if err := g.AddSource(id, deadline); err != nil {
			t.Fatal(err)
		}
	}

	const derivedCount = 14
	for i := sourceCount; i < sourceCount+derivedCount; i++ {
		id := fmtNodeID(i)
		srcA := fmtNodeID(rng.Intn(i))
		srcB := fmtNodeID(rng.Intn(i))
		switch rng.Intn(4) {
		case 0:
			if err := g.AddCopy(id, srcA); err != nil {
				t.Fatal(err)
			}
		case 1:
			if err := g.AddMerge(id, srcA, srcB); err != nil {
				t.Fatal(err)
			}
		case 2:
			label := "tag"
			independent := time.Time{}
			if rng.Intn(2) == 0 {
				independent = at(1 + rng.Intn(40))
			}
			if err := g.AddAnonymize(id, []string{srcA}, label, independent); err != nil {
				t.Fatal(err)
			}
		default:
			if err := g.AddRebuild(id, srcA); err != nil {
				t.Fatal(err)
			}
		}
		// 随机制造循环：让新节点反向成为某个旧节点的上游。
		if rng.Intn(3) == 0 {
			target := fmtNodeID(rng.Intn(i))
			if err := g.AddEdge(id, target); err != nil {
				t.Fatal(err)
			}
		}
	}

	// 随机扰动：改来源期限、签发/撤销证明、增量加边，并在每轮后比对全量重算。
	for round := 0; round < 40; round++ {
		switch rng.Intn(3) {
		case 0:
			src := fmtNodeID(rng.Intn(sourceCount))
			if err := g.SetSourceDeadline(src, at(1+rng.Intn(30))); err != nil {
				t.Fatal(err)
			}
		case 1:
			if rng.Intn(2) == 0 {
				g.IssueAttestation("tag")
			} else {
				g.RevokeAttestation("tag")
			}
		case 2:
			src := fmtNodeID(rng.Intn(sourceCount + derivedCount))
			dst := fmtNodeID(sourceCount + rng.Intn(derivedCount))
			if err := g.AddEdge(src, dst); err != nil {
				t.Fatal(err)
			}
		}

		incremental := make(map[string]time.Time)
		for i := 0; i < sourceCount+derivedCount; i++ {
			id := fmtNodeID(i)
			incremental[id] = mustDeadline(t, g, id)
		}
		g.Rebuild()
		for id, want := range incremental {
			assertDeadline(t, g, id, want)
		}
	}
}

func fmtNodeID(i int) string {
	return "n" + string(rune('a'+i/26)) + string(rune('a'+i%26))
}

// TestConcurrentAccess 用并发读写验证内部锁的正确性（配合 -race 使用）。
func TestConcurrentAccess(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(10)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddAnonymize("a1", []string{"s1"}, "tag-x", at(30)); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 100; step++ {
				switch (worker + step) % 5 {
				case 0:
					_ = g.SetSourceDeadline("s1", at(1+step%30))
				case 1:
					g.IssueAttestation("tag-x")
				case 2:
					g.RevokeAttestation("tag-x")
				case 3:
					if _, err := g.Deadline("a1"); err != nil {
						t.Error(err)
						return
					}
				case 4:
					if _, err := g.Explain("a1"); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}(worker)
	}
	wg.Wait()

	// 并发结束后图仍保持内部一致：全量重算不应改变任何期限。
	snapshot := make(map[string]time.Time)
	for _, id := range []string{"s1", "a1"} {
		snapshot[id] = mustDeadline(t, g, id)
	}
	g.Rebuild()
	for id, want := range snapshot {
		assertDeadline(t, g, id, want)
	}
}

// TestErrorAndIdempotency 验证重复 ID、重复加边与不存在节点的行为。
func TestErrorAndIdempotency(t *testing.T) {
	g := NewGraph()
	if err := g.AddSource("s1", at(10)); err != nil {
		t.Fatal(err)
	}
	if err := g.AddSource("s1", at(11)); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("want ErrNodeExists, got %v", err)
	}
	if err := g.AddCopy("c1", "s1"); err != nil {
		t.Fatal(err)
	}
	if err := g.AddCopy("c1", "s1"); !errors.Is(err, ErrNodeExists) {
		t.Fatalf("want ErrNodeExists, got %v", err)
	}

	// 重复加边幂等，不改变期限。
	if err := g.AddEdge("s1", "c1"); err != nil {
		t.Fatal(err)
	}
	assertDeadline(t, g, "c1", at(10))

	if _, err := g.Deadline("ghost"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}
	if _, err := g.Node("ghost"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("want ErrNodeNotFound, got %v", err)
	}

	// AddMerge 对重复来源去重。
	if err := g.AddMerge("m1", "s1", "s1", "c1"); err != nil {
		t.Fatal(err)
	}
	info, err := g.Node("m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Sources) != 2 {
		t.Fatalf("expected 2 deduplicated sources, got %v", info.Sources)
	}
}
