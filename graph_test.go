package netsec

import "testing"

func nodeIDs(sub Subgraph) map[string]bool {
	ids := make(map[string]bool, len(sub.Nodes))
	for _, n := range sub.Nodes {
		ids[n.ID()] = true
	}
	return ids
}

func sourceIDs(sub Subgraph) map[string]bool {
	ids := make(map[string]bool, len(sub.Sources))
	for _, n := range sub.Sources {
		ids[n.ID()] = true
	}
	return ids
}

func edgeKinds(sub Subgraph) map[EdgeKind]int {
	counts := make(map[EdgeKind]int)
	for _, e := range sub.Edges {
		counts[e.Kind]++
	}
	return counts
}

func requireNode(t *testing.T, sub Subgraph, id string) {
	t.Helper()
	if !nodeIDs(sub)[id] {
		t.Errorf("期望子图包含节点 %s，实际节点: %v", id, sub.Nodes)
	}
}

func requireNoNode(t *testing.T, sub Subgraph, id string) {
	t.Helper()
	if nodeIDs(sub)[id] {
		t.Errorf("期望子图不包含节点 %s，实际节点: %v", id, sub.Nodes)
	}
}

// 核心验证：覆盖写入后，旧内容版本不再成为新连接的来源。
// 事件流故意乱序给出，由 Replay 按 (Time, Seq) 重放。
func TestBacktraceOverwriteSeversOldContent(t *testing.T) {
	g := NewGraph()
	mal := ProcessRef{PID: 100, StartID: 1}
	reader := ProcessRef{PID: 101, StartID: 1}
	cleaner := ProcessRef{PID: 102, StartID: 1}
	victim := ProcessRef{PID: 103, StartID: 1}

	events := []Event{
		{Kind: EventConnect, Time: 6, Proc: victim, Addr: "203.0.113.9:8443"}, // [0] 新连接
		{Kind: EventWrite, Time: 1, Proc: mal, Path: "/tmp/payload"},          // [1] 恶意内容 v1
		{Kind: EventRead, Time: 5, Proc: victim, Path: "/tmp/payload"},        // [2] 读到 v2
		{Kind: EventConnect, Time: 3, Proc: reader, Addr: "198.51.100.7:443"}, // [3] 旧连接
		{Kind: EventWrite, Time: 4, Proc: cleaner, Path: "/tmp/payload"},      // [4] 覆盖写入 v2
		{Kind: EventRead, Time: 2, Proc: reader, Path: "/tmp/payload"},        // [5] 读到 v1
	}
	h := g.Replay(events)

	// 覆盖写入后的新连接：只能追溯到 v2 与清理进程，不能追溯到 v1/恶意进程。
	after := g.Backtrace(h[0])
	requireNode(t, after, "proc:103:1")
	requireNode(t, after, "proc:102:1")
	requireNode(t, after, "file:/tmp/payload#v2")
	requireNode(t, after, "sock:203.0.113.9:8443")
	requireNoNode(t, after, "file:/tmp/payload#v1")
	requireNoNode(t, after, "proc:100:1")
	requireNoNode(t, after, "proc:101:1")
	requireNoNode(t, after, "sock:198.51.100.7:443")
	if got := sourceIDs(after); len(got) != 1 || !got["proc:102:1"] {
		t.Errorf("新连接的源点应为清理进程，实际: %v", got)
	}
	if kinds := edgeKinds(after); kinds[EdgeWrite] != 1 || kinds[EdgeRead] != 1 || kinds[EdgeConnect] != 1 {
		t.Errorf("新连接子图应只有 写/读/连 各一条边，实际: %v", after.Edges)
	}

	// 覆盖写入前的旧连接：仍归因于 v1 与恶意进程，且不受未来事件污染。
	before := g.Backtrace(h[3])
	requireNode(t, before, "proc:100:1")
	requireNode(t, before, "proc:101:1")
	requireNode(t, before, "file:/tmp/payload#v1")
	requireNoNode(t, before, "file:/tmp/payload#v2")
	requireNoNode(t, before, "proc:102:1")
	requireNoNode(t, before, "proc:103:1")
	if got := sourceIDs(before); len(got) != 1 || !got["proc:100:1"] {
		t.Errorf("旧连接的源点应为恶意进程，实际: %v", got)
	}
}

// 进程号重用：同一 PID 的不同生命期是不同的节点，互不污染。
func TestBacktracePIDReuse(t *testing.T) {
	g := NewGraph()
	oldProc := ProcessRef{PID: 200, StartID: 1}
	child := ProcessRef{PID: 201, StartID: 1}
	newProc := ProcessRef{PID: 200, StartID: 2} // 进程号被重用

	h := g.Replay([]Event{
		{Kind: EventFork, Time: 1, Proc: oldProc, Child: child},
		{Kind: EventWrite, Time: 2, Proc: oldProc, Path: "/tmp/old"},
		{Kind: EventRead, Time: 3, Proc: newProc, Path: "/tmp/current"},
		{Kind: EventConnect, Time: 4, Proc: newProc, Addr: "192.0.2.1:443"},
	})

	sub := g.Backtrace(h[3])
	requireNode(t, sub, "proc:200:2")
	requireNode(t, sub, "file:/tmp/current#v0") // 从未写入，视为既有内容
	requireNoNode(t, sub, "proc:200:1")
	requireNoNode(t, sub, "proc:201:1")
	requireNoNode(t, sub, "file:/tmp/old#v1")
	if kinds := edgeKinds(sub); kinds[EdgeFork] != 0 || kinds[EdgeWrite] != 0 {
		t.Errorf("新生命期的溯源不应包含旧生命期的派生/写入边，实际: %v", sub.Edges)
	}
}

// 同刻事件按顺序号判定先后：连接之后的读取不能成为连接的来源，
// 而连接之前的读取可以。
func TestBacktraceSameTimestampSeq(t *testing.T) {
	g := NewGraph()
	writer := ProcessRef{PID: 300, StartID: 1}
	proc := ProcessRef{PID: 301, StartID: 1}

	h := g.Replay([]Event{
		{Kind: EventWrite, Time: 5, Seq: 1, Proc: writer, Path: "/tmp/c"},
		{Kind: EventConnect, Time: 5, Seq: 2, Proc: proc, Addr: "192.0.2.2:443"}, // 早连接
		{Kind: EventRead, Time: 5, Seq: 3, Proc: proc, Path: "/tmp/c"},
		{Kind: EventConnect, Time: 5, Seq: 4, Proc: proc, Addr: "192.0.2.2:443"}, // 晚连接
	})

	early := g.Backtrace(h[1])
	requireNoNode(t, early, "file:/tmp/c#v1") // 读发生在连接之后（Seq 3 > 2）
	requireNoNode(t, early, "proc:300:1")
	if kinds := edgeKinds(early); kinds[EdgeRead] != 0 {
		t.Errorf("同刻顺序号在后的读边不应进入子图，实际: %v", early.Edges)
	}

	late := g.Backtrace(h[3])
	requireNode(t, late, "file:/tmp/c#v1")
	requireNode(t, late, "proc:300:1")
	if got := sourceIDs(late); len(got) != 1 || !got["proc:300:1"] {
		t.Errorf("晚连接的源点应为写入进程，实际: %v", got)
	}
}

// 派生链：写入 -> 读取 -> 派生 -> 写新文件 -> 读取 -> 连接，
// 全链路可追溯；无关事件不进入最小子图。
func TestBacktraceForkChain(t *testing.T) {
	g := NewGraph()
	mal := ProcessRef{PID: 400, StartID: 1}
	parent := ProcessRef{PID: 401, StartID: 1}
	child := ProcessRef{PID: 402, StartID: 1}
	victim := ProcessRef{PID: 403, StartID: 1}
	noise := ProcessRef{PID: 499, StartID: 1}

	h := g.Replay([]Event{
		{Kind: EventWrite, Time: 1, Proc: mal, Path: "/tmp/x"},
		{Kind: EventRead, Time: 2, Proc: parent, Path: "/tmp/x"},
		{Kind: EventWrite, Time: 2, Proc: noise, Path: "/tmp/noise"}, // 无关分支
		{Kind: EventFork, Time: 3, Proc: parent, Child: child},
		{Kind: EventWrite, Time: 4, Proc: child, Path: "/tmp/y"},
		{Kind: EventRead, Time: 5, Proc: victim, Path: "/tmp/y"},
		{Kind: EventConnect, Time: 6, Proc: victim, Addr: "192.0.2.3:443"},
	})

	sub := g.Backtrace(h[6])
	for _, id := range []string{
		"proc:400:1", "file:/tmp/x#v1", "proc:401:1", "proc:402:1",
		"file:/tmp/y#v1", "proc:403:1", "sock:192.0.2.3:443",
	} {
		requireNode(t, sub, id)
	}
	requireNoNode(t, sub, "proc:499:1")
	requireNoNode(t, sub, "file:/tmp/noise#v1")
	if got := sourceIDs(sub); len(got) != 1 || !got["proc:400:1"] {
		t.Errorf("源点应只有恶意写入进程，实际: %v", got)
	}
	if len(sub.Edges) != 6 {
		t.Errorf("最小子图应恰好包含 6 条因果边，实际 %d 条: %v", len(sub.Edges), sub.Edges)
	}
}
