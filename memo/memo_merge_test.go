package memo

import "testing"

// TestUnionFindRedirect 验证 Union-Find 路径压缩与重定向语义。
func TestUnionFindRedirect(t *testing.T) {
	m := NewMemo(testSchema())
	a := m.newGroup(1)
	b := m.newGroup(2)
	c := m.newGroup(4)

	m.mergeGroups(a, b)
	m.mergeGroups(a, c)

	for _, id := range []int{a, b, c} {
		if got := m.Find(id); got != a {
			t.Fatalf("Find(%d)=%d, want survivor %d", id, got, a)
		}
	}
	if _, alive := m.groups[b]; alive {
		t.Fatalf("merged group %d should be removed from live groups", b)
	}
	if _, alive := m.groups[c]; alive {
		t.Fatalf("merged group %d should be removed from live groups", c)
	}
	if m.MergeCount() != 2 {
		t.Fatalf("MergeCount=%d, want 2", m.MergeCount())
	}
}

// TestAssociativityExploresAllShapes 验证交换律/结合律探索后，每个连通
// 关系集合恰好一个 Group，且根 Group 同时持有左深与右深结合形态。
func TestAssociativityExploresAllShapes(t *testing.T) {
	m := exploredMemo(t, chainTree(4))

	if m.DedupeCount() == 0 {
		t.Fatalf("expected logical expression deduplication during exploration")
	}

	seenRelset := map[uint32]bool{}
	for gid, g := range m.groups {
		if m.Find(gid) != gid {
			t.Fatalf("live group %d is not a union-find root", gid)
		}
		if seenRelset[g.relset] {
			t.Fatalf("duplicate live group for relset %d", g.relset)
		}
		seenRelset[g.relset] = true
		if len(g.exprs) == 0 {
			t.Fatalf("group %d has no expressions", gid)
		}
	}

	sigs := map[string]bool{}
	for _, e := range m.exprs {
		if sigs[e.key] {
			t.Fatalf("duplicate expression key after exploration: %s", e.key)
		}
		sigs[e.key] = true
		for _, child := range e.children {
			if m.Find(child) != child {
				t.Fatalf("expr %s references non-canonical child %d", e.key, child)
			}
		}
	}

	// 4 表链式连接的连通关系集合共 10 个。
	if len(m.groups) != 10 {
		t.Fatalf("live groups=%d, want 10 connected subsets", len(m.groups))
	}

	root := m.groups[m.Root()]
	shapes := map[string]bool{}
	for _, eid := range root.exprs {
		e := m.exprs[eid]
		if e.op != opJoin {
			continue
		}
		l := popcount(m.groups[e.children[0]].relset)
		r := popcount(m.groups[e.children[1]].relset)
		shapes[shapeName(l, r)] = true
	}
	if !shapes["1-3"] || !shapes["3-1"] {
		t.Fatalf("root missing associativity shapes, got %v", shapes)
	}
}

// TestInjectTriggersGroupMergeAndCascade 验证显式注入等价 Group 时：
// Union-Find 合并、表达式去重、父表达式哈希重算与二次级联去重全部发生，
// 且 Memo 收敛回“每个关系集合一个 Group”的不变量。
func TestInjectTriggersGroupMergeAndCascade(t *testing.T) {
	m := exploredMemo(t, chainTree(4))

	// 选一个被父表达式引用的两表 Group（t1⋈t2），注入它的等价 Group。
	target := m.byRelset[3]
	if target == 0 {
		t.Fatalf("missing t1⋈t2 group")
	}

	beforeM := m.MergeCount()
	beforeD := m.DedupeCount()
	beforeH := m.HashRecomputeCount()

	root, err := m.injectEquivalentGroupReroot(target)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}

	if m.MergeCount() <= beforeM {
		t.Fatalf("expected a union-find group merge")
	}
	if m.DedupeCount() <= beforeD {
		t.Fatalf("expected expression deduplication during merge")
	}
	if m.HashRecomputeCount() <= beforeH {
		t.Fatalf("expected parent-expression hash recomputation during cascade")
	}

	// 收敛后不变量：每个关系集合仅一个存活 Group，所有表达式键唯一。
	relsetOwners := map[uint32]int{}
	for gid, g := range m.groups {
		if m.Find(gid) != gid {
			t.Fatalf("live group %d not a union-find root", gid)
		}
		if owner, dup := relsetOwners[g.relset]; dup {
			t.Fatalf("relset %d owned by both %d and %d after cascade",
				g.relset, owner, gid)
		}
		relsetOwners[g.relset] = gid
	}
	keys := map[string]bool{}
	for _, e := range m.exprs {
		if keys[e.key] {
			t.Fatalf("duplicate expression key remains: %s", e.key)
		}
		keys[e.key] = true
	}

	// 根 Group 仍然可解析到一个存活的规范 Group。
	rootCanonical := m.Find(m.Root())
	if _, alive := m.groups[rootCanonical]; !alive {
		t.Fatalf("root group not preserved after cascade")
	}
	// 注入目标是两表子组，根 Group 保持独立存活。
	if _, alive := m.groups[root]; !alive {
		t.Fatalf("injected survivor group %d is not live", root)
	}
}

func shapeName(a, b int) string { return itoa(a) + "-" + itoa(b) }

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}

func popcount(v uint32) int {
	n := 0
	for v != 0 {
		n += int(v & 1)
		v >>= 1
	}
	return n
}

// TestPublicInjectAPI 验证导出的 InjectEquivalentGroup 入口可用且收敛。
func TestPublicInjectAPI(t *testing.T) {
	m := exploredMemo(t, chainTree(3))
	target := m.byRelset[3]
	before := m.MergeCount()

	canonical, err := m.InjectEquivalentGroup(target)
	if err != nil {
		t.Fatalf("inject: %v", err)
	}
	if m.MergeCount() <= before {
		t.Fatalf("public inject did not trigger a merge")
	}
	if _, alive := m.groups[canonical]; !alive {
		t.Fatalf("canonical group %d not live", canonical)
	}

	// 物理搜索在合并后的 Memo 上仍能正常求解。
	best, err := NewOptimizer(m, DefaultCostModel{}).BestPlan(PhysProps{})
	if err != nil {
		t.Fatalf("optimize after inject: %v", err)
	}
	if best == nil {
		t.Fatalf("nil plan after inject")
	}
}
