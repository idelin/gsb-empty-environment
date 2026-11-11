package netsec

import (
	"errors"
	"reflect"
	"testing"
)

const (
	nsHost = "host-ns"
	nsA    = "sandbox-a"
	nsB    = "sandbox-b"

	grpHost = "g-host"
	grpA    = "g-a"
	grpB    = "g-b"
)

// buildTestGraph 构造固定拓扑：
//
//	host-ns:   root(shared, g-host)
//	sandbox-a: rootA(slave,  g-a)   g-a 是 g-host 的从属组
//	           privA(private, parent=rootA)
//	sandbox-b: rootB(slave,  g-b)   g-b 是 g-a 的从属组（二级从属链）
func buildTestGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph(nsHost)
	must(t, g.AddGroup(grpHost, Shared, ""))
	must(t, g.AddGroup(grpA, Slave, grpHost))
	must(t, g.AddGroup(grpB, Slave, grpA))

	must(t, g.AddNode(Node{ID: "root", Namespace: nsHost, Type: Shared, Group: grpHost}))
	must(t, g.AddNode(Node{ID: "rootA", Namespace: nsA, Type: Slave, Group: grpA}))
	must(t, g.AddNode(Node{ID: "privA", Namespace: nsA, ParentID: "rootA", Type: Private}))
	must(t, g.AddNode(Node{ID: "rootB", Namespace: nsB, Type: Slave, Group: grpB}))
	return g
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v", target, err)
	}
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertInts(t *testing.T, got, want []int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func reachedIDs(report *Report) []string {
	ids := make([]string, 0, len(report.Reached))
	for _, reached := range report.Reached {
		ids = append(ids, reached.NodeID)
	}
	return ids
}

func TestPropagateSharedAndDownstreamSlaves(t *testing.T) {
	g := buildTestGraph(t)

	report := g.Propagate("root")
	assertStrings(t, reachedIDs(report), []string{"root", "rootA", "rootB"})
	assertStrings(t, report.Namespaces, []string{nsHost, nsA, nsB})
	if !report.HostVisible {
		t.Fatalf("event sourced in host must be host-visible")
	}

	rootA := report.Reached[1]
	if len(rootA.Path) != 1 || rootA.Path[0].Mechanism != HopDownstreamSlave {
		t.Fatalf("host -> rootA should be one downstream-slave hop, got %+v", rootA.Path)
	}
	rootB := report.Reached[2]
	if len(rootB.Path) != 2 {
		t.Fatalf("host -> rootB should be a two-hop path, got %+v", rootB.Path)
	}
}

func TestPropagateSlaveNeverFlowsUpstream(t *testing.T) {
	g := buildTestGraph(t)

	// 二级从属组发起的事件只能停在自身：既不能回到 g-a/g-host，也没有更下游的组。
	reportB := g.Propagate("rootB")
	assertStrings(t, reachedIDs(reportB), []string{"rootB"})
	assertStrings(t, reportB.Namespaces, []string{nsB})
	if reportB.HostVisible {
		t.Fatalf("slave event must never propagate back to the host")
	}

	// g-a 是从属组：事件可以继续向下游 g-b 流动，但不能回灌主组 g-host。
	reportA := g.Propagate("rootA")
	assertStrings(t, reachedIDs(reportA), []string{"rootA", "rootB"})
	assertStrings(t, reportA.Namespaces, []string{nsA, nsB})
	if reportA.HostVisible {
		t.Fatalf("slave-group event must not reach the host master group")
	}
}

func TestPropagatePrivateIsTerminal(t *testing.T) {
	g := buildTestGraph(t)

	report := g.Propagate("privA")
	assertStrings(t, reachedIDs(report), []string{"privA"})
	assertStrings(t, report.Namespaces, []string{nsA})
	if report.HostVisible {
		t.Fatalf("private mount must never leak")
	}
}

func TestPropagateSharedPeer(t *testing.T) {
	g := NewGraph(nsHost)
	must(t, g.AddGroup("sg", Shared, ""))
	must(t, g.AddNode(Node{ID: "h1", Namespace: nsHost, Type: Shared, Group: "sg"}))
	must(t, g.AddNode(Node{ID: "a1", Namespace: nsA, Type: Shared, Group: "sg"}))
	must(t, g.AddNode(Node{ID: "a2", Namespace: nsA, Type: Shared, Group: "sg"}))

	report := g.Propagate("h1")
	assertStrings(t, reachedIDs(report), []string{"a1", "a2", "h1"})
	assertStrings(t, report.Namespaces, []string{nsHost, nsA})

	var peerHop *Hop
	for i := range report.Reached {
		if report.Reached[i].NodeID == "a1" {
			peerHop = &report.Reached[i].Path[0]
		}
	}
	if peerHop == nil || peerHop.Mechanism != HopSharedPeer || peerHop.Group != "sg" {
		t.Fatalf("expected a shared-peer hop inside sg, got %+v", peerHop)
	}
}

func TestSlaveGroupCycleIsRejected(t *testing.T) {
	g := NewGraph(nsHost)
	must(t, g.AddGroup("s1", Shared, ""))
	must(t, g.AddGroup("d1", Slave, "s1"))
	must(t, g.AddGroup("d2", Slave, "d1"))

	// 让 d1 改主到自己的下游 d2：d1 -> d2 -> d1 构成循环，必须拒绝。
	assertErrorIs(t, g.SetMaster("d1", "d2"), ErrCycle)
	// 自环同样拒绝。
	assertErrorIs(t, g.SetMaster("d1", "d1"), ErrCycle)

	master, ok := g.GroupMaster("d1")
	if !ok || master != "s1" {
		t.Fatalf("rejected rebind must leave master unchanged, got %q (%v)", master, ok)
	}
}

func TestSetMasterDisconnectsPropagation(t *testing.T) {
	g := buildTestGraph(t)

	// 断开前：宿主事件沿 g-host -> g-a -> g-b 到达两个沙箱。
	before := g.Propagate("root")
	if len(before.Reached) != 3 {
		t.Fatalf("pre-disconnect topology should reach all three nodes, got %+v", before)
	}

	// 新建独立共享组并把 g-a 切过去：原 g-host 的下游链路被切断。
	must(t, g.AddGroup("g-island", Shared, ""))
	must(t, g.SetMaster(grpA, "g-island"))

	after := g.Propagate("root")
	assertStrings(t, reachedIDs(after), []string{"root"})
	assertStrings(t, after.Namespaces, []string{nsHost})

	// 从属侧依旧不能反向触达宿主，但仍可向自己的下游 g-b 转发。
	fromA := g.Propagate("rootA")
	if fromA.HostVisible {
		t.Fatalf("rebound slave group must still be unable to reach the host")
	}
	assertStrings(t, reachedIDs(fromA), []string{"rootA", "rootB"})
}

func TestRemoveNodeDisconnectsPropagation(t *testing.T) {
	g := buildTestGraph(t)

	// g-b 目前只有 rootB 一个成员，删除后 g-host 的事件在 g-a 处终止（无下游成员可达）。
	must(t, g.RemoveNode("rootB"))
	report := g.Propagate("root")
	assertStrings(t, reachedIDs(report), []string{"root", "rootA"})

	// 有子挂载的节点不允许直接删除，避免悬空父子关系。
	assertErrorIs(t, g.RemoveNode("rootA"), ErrHasChildren)
}

func TestMoveKeepsParentConsistency(t *testing.T) {
	g := buildTestGraph(t)
	must(t, g.AddNode(Node{ID: "mntA", Namespace: nsA, ParentID: "rootA", Type: Private}))
	must(t, g.AddNode(Node{ID: "mntB", Namespace: nsA, ParentID: "mntA", Type: Private}))

	report, err := g.Move("mntA", "rootA")
	if err != nil {
		t.Fatalf("move within same namespace should succeed: %v", err)
	}
	node, _ := g.Node("mntA")
	if node.ParentID != "rootA" {
		t.Fatalf("parent should be rootA after move, got %q", node.ParentID)
	}
	subtree, ok := g.SubtreeIDs("mntA")
	if !ok {
		t.Fatalf("moved node must still be present")
	}
	assertStrings(t, subtree, []string{"mntA", "mntB"})
	if report.Origin != "mntA" {
		t.Fatalf("move should return the propagation report of the moved node, got %q", report.Origin)
	}

	// 不能把节点移到自己的后代下面。
	_, err = g.Move("mntA", "mntB")
	assertErrorIs(t, err, ErrMoveToDescendant)

	// 不能跨命名空间移动。
	_, err = g.Move("mntA", "root")
	assertErrorIs(t, err, ErrParentNamespace)

	// 根挂载没有父边，不可移动。
	_, err = g.Move("root", "rootA")
	assertErrorIs(t, err, ErrCannotMoveRoot)
}

func TestAddNodeRejectsCrossNamespaceParent(t *testing.T) {
	g := buildTestGraph(t)
	err := g.AddNode(Node{ID: "bad", Namespace: nsB, ParentID: "rootA", Type: Private})
	assertErrorIs(t, err, ErrParentNamespace)
}

func TestReplayHostForbiddenRollsBackMove(t *testing.T) {
	events := []Event{
		{Type: EvAddGroup, GroupID: grpHost, Kind: Shared},
		{Type: EvAddNode, Node: Node{ID: "root", Namespace: nsHost, Type: Shared, Group: grpHost}},
		{Type: EvAddNode, Node: Node{ID: "peer", Namespace: nsA, Type: Shared, Group: grpHost}},
		// 共享同伴发起的事件会沿同组回传到宿主：在宿主禁用策略下必须被拒绝。
		{Type: EvMount, NodeID: "peer"},
		// 宿主源头事件必然抵达宿主：禁止，且不改变图。
		{Type: EvMount, NodeID: "root"},
	}

	result := Replay(events, ReplayOptions{HostNamespace: nsHost, ForbidHostVisible: true})
	if len(result.Steps) != len(events) {
		t.Fatalf("expected %d step results, got %d", len(events), len(result.Steps))
	}
	assertErrorIs(t, result.Steps[3].Err, ErrHostForbidden)
	assertErrorIs(t, result.Steps[4].Err, ErrHostForbidden)
	assertInts(t, result.Failures, []int{3, 4})
	assertInts(t, result.HostBlocked, []int{3, 4})

	peer, ok := result.Graph.Node("peer")
	if !ok || peer.Namespace != nsA {
		t.Fatalf("structural steps must survive blocked host events, got %+v", peer)
	}
}

func TestReplayMoveRollbackOnHostPolicy(t *testing.T) {
	// 共享组里有宿主节点与沙箱节点；沙箱里再放一条父子链用于移动。
	events := []Event{
		{Type: EvAddGroup, GroupID: "sg", Kind: Shared},
		{Type: EvAddNode, Node: Node{ID: "root", Namespace: nsHost, Type: Shared, Group: "sg"}},
		{Type: EvAddNode, Node: Node{ID: "base", Namespace: nsA, Type: Shared, Group: "sg"}},
		{Type: EvAddNode, Node: Node{ID: "leaf", Namespace: nsA, ParentID: "base", Type: Shared, Group: "sg"}},
		// leaf 与宿主 root 是共享同伴：移动后从 leaf 发起的报告会抵达宿主，必须回滚。
		{Type: EvMove, NodeID: "leaf", NewParent: "base"},
	}
	result := Replay(events, ReplayOptions{HostNamespace: nsHost, ForbidHostVisible: true})

	step := result.Steps[4]
	if step.Applied || !errors.Is(step.Err, ErrHostForbidden) {
		t.Fatalf("host-visible move must be blocked: %+v", step)
	}
	leaf, _ := result.Graph.Node("leaf")
	if leaf.ParentID != "base" {
		t.Fatalf("rolled-back move must leave the parent edge unchanged, got %q", leaf.ParentID)
	}
	assertInts(t, result.HostBlocked, []int{4})
}

func TestReplayMoveAllowedWhenHostNotReachable(t *testing.T) {
	events := []Event{
		{Type: EvAddGroup, GroupID: grpHost, Kind: Shared},
		{Type: EvAddGroup, GroupID: grpA, Kind: Slave, Master: grpHost},
		{Type: EvAddNode, Node: Node{ID: "root", Namespace: nsHost, Type: Shared, Group: grpHost}},
		{Type: EvAddNode, Node: Node{ID: "rootA", Namespace: nsA, Type: Slave, Group: grpA}},
		// 私有挂载与宿主无任何传播边，移动它不可能让宿主可见。
		{Type: EvAddNode, Node: Node{ID: "mntA", Namespace: nsA, ParentID: "rootA", Type: Private}},
		{Type: EvMove, NodeID: "mntA", NewParent: "rootA"},
	}
	result := Replay([]Event{
		events[0], events[1], events[2], events[3], events[4], events[5],
	}, ReplayOptions{HostNamespace: nsHost, ForbidHostVisible: true})

	step := result.Steps[5]
	if !step.Applied || step.Report == nil {
		t.Fatalf("private move should apply with a report: %+v", step)
	}
	mnt, _ := result.Graph.Node("mntA")
	if mnt.ParentID != "rootA" {
		t.Fatalf("move should update parent edge, got %q", mnt.ParentID)
	}
}

func TestReplayRecordsCycleAndContinues(t *testing.T) {
	events := []Event{
		{Type: EvAddGroup, GroupID: "sg", Kind: Shared},
		{Type: EvAddGroup, GroupID: "d1", Kind: Slave, Master: "sg"},
		{Type: EvAddGroup, GroupID: "d2", Kind: Slave, Master: "d1"},
		// 制造 d1 -> d2 -> d1 循环，必须失败且不改变 d1 的主组。
		{Type: EvSetMaster, GroupID: "d1", Master: "d2"},
		// 失败后重放仍可继续，合法节点应成功落地。
		{Type: EvAddNode, Node: Node{ID: "n1", Namespace: nsHost, Type: Shared, Group: "sg"}},
		{Type: EvMount, NodeID: "n1"},
		{Type: EvRemoveNode, NodeID: "n1"},
		{Type: EvMount, NodeID: "n1"}, // 节点已删除：报未找到
	}

	result := Replay(events, ReplayOptions{HostNamespace: nsHost})
	assertInts(t, result.Failures, []int{3, 7})
	assertErrorIs(t, result.Steps[3].Err, ErrCycle)
	assertErrorIs(t, result.Steps[7].Err, ErrNodeNotFound)

	master, ok := result.Graph.GroupMaster("d1")
	if !ok || master != "sg" {
		t.Fatalf("d1 master must remain sg after failed rebind, got %q", master)
	}
	if _, ok := result.Graph.Node("n1"); ok {
		t.Fatalf("n1 should have been removed by step 6")
	}
	if result.Steps[5].Report == nil || !result.Steps[5].Report.HostVisible {
		t.Fatalf("mount step on host node should produce a host-visible report")
	}
}

func TestReplayRejectsUnknownEvent(t *testing.T) {
	result := Replay([]Event{{Type: EventType(99)}}, ReplayOptions{})
	if len(result.Failures) != 1 {
		t.Fatalf("unknown event should be a failure, got %+v", result.Steps)
	}
	if !errors.Is(result.Steps[0].Err, ErrInvalidEvent) {
		t.Fatalf("unknown event should be reported as ErrInvalidEvent, got %+v", result.Steps)
	}
}
