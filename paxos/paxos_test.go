package paxos

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

const testLease = 30 * time.Second

// newCluster 创建 n 个节点的全连通集群。
func newCluster(t *testing.T, n int) (*Network, []*Node) {
	t.Helper()
	nw := NewNetwork(n)
	nodes := make([]*Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = NewNode(i, nw, testLease)
		nw.Register(nodes[i])
	}
	t.Cleanup(func() {
		for _, nd := range nodes {
			nd.Close()
		}
	})
	return nw, nodes
}

// waitApplied 等待节点应用满 count 条并取出它们。
func waitApplied(t *testing.T, n *Node, count int) []AppliedEntry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n.appliedQ.Len() >= int64(count) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	var out []AppliedEntry
	for len(out) < count {
		e, ok := n.appliedQ.Dequeue()
		if !ok {
			t.Fatalf("node %d: applied %d entries, want %d", n.id, len(out), count)
		}
		out = append(out, e)
	}
	return out
}

// drainApplied 取出节点当前所有已应用条目。
func drainApplied(n *Node) []AppliedEntry {
	var out []AppliedEntry
	for {
		e, ok := n.appliedQ.Dequeue()
		if !ok {
			return out
		}
		out = append(out, e)
	}
}

func values(entries []AppliedEntry) [][]byte {
	out := make([][]byte, len(entries))
	for i, e := range entries {
		out[i] = e.Value
	}
	return out
}

func TestBasicCommit(t *testing.T) {
	_, nodes := newCluster(t, 3)
	for i, v := range []string{"a", "b", "c"} {
		if _, err := nodes[0].Propose([]byte(v)); err != nil {
			t.Fatalf("propose %q: %v", v, err)
		}
		_ = i
	}
	for _, nd := range nodes {
		got := values(waitApplied(t, nd, 3))
		for i, want := range []string{"a", "b", "c"} {
			if !bytes.Equal(got[i], []byte(want)) {
				t.Fatalf("node %d slot %d = %q, want %q", nd.id, i, got[i], want)
			}
		}
	}
}

func TestPrepareRejectsLowerBallot(t *testing.T) {
	_, nodes := newCluster(t, 3)
	n := nodes[0]
	b1 := NewBallot(5, 1)
	if r := n.HandlePrepare(PrepareRequest{Ballot: b1}); !r.OK {
		t.Fatal("first prepare must succeed")
	}
	if r := n.HandlePrepare(PrepareRequest{Ballot: NewBallot(4, 2)}); r.OK {
		t.Fatal("lower ballot must be rejected")
	}
	if r := n.HandlePrepare(PrepareRequest{Ballot: b1}); r.OK {
		t.Fatal("equal ballot must be rejected")
	}
	if r := n.HandlePrepare(PrepareRequest{Ballot: NewBallot(6, 0)}); !r.OK {
		t.Fatal("higher ballot must succeed")
	}
}

func TestAcceptRespectsPromise(t *testing.T) {
	_, nodes := newCluster(t, 3)
	n := nodes[0]
	n.HandlePrepare(PrepareRequest{Ballot: NewBallot(5, 1)})
	if r := n.HandleAccept(AcceptRequest{Ballot: NewBallot(4, 0), Slot: 0, Value: []byte("x")}); r.OK {
		t.Fatal("accept below promise must be rejected")
	}
	if r := n.HandleAccept(AcceptRequest{Ballot: NewBallot(5, 1), Slot: 0, Value: []byte("x")}); !r.OK {
		t.Fatal("accept at promised ballot must succeed")
	}
}

func TestCommittedSlotNeverOverwritten(t *testing.T) {
	_, nodes := newCluster(t, 3)
	n := nodes[0]
	n.HandleCommit(CommitMessage{Ballot: NewBallot(3, 0), Slot: 0, Value: []byte("final")})
	// 更高提案号的异值 Accept 也必须被拒绝
	if r := n.HandleAccept(AcceptRequest{Ballot: NewBallot(9, 1), Slot: 0, Value: []byte("evil")}); r.OK {
		t.Fatal("committed slot must reject conflicting accept")
	}
	// 同值幂等接受
	if r := n.HandleAccept(AcceptRequest{Ballot: NewBallot(9, 1), Slot: 0, Value: []byte("final")}); !r.OK {
		t.Fatal("idempotent accept on committed slot must succeed")
	}
	// 异值 Commit 触发安全 panic
	defer func() {
		if recover() == nil {
			t.Fatal("conflicting commit must panic (safety violation)")
		}
	}()
	n.HandleCommit(CommitMessage{Ballot: NewBallot(9, 1), Slot: 0, Value: []byte("evil")})
}

// TestLeaseBypassesPhase1 租约内连续提交不得再发 Prepare。
func TestLeaseBypassesPhase1(t *testing.T) {
	nw, nodes := newCluster(t, 5)
	leader := nodes[0]
	if _, err := leader.Propose([]byte("first")); err != nil {
		t.Fatalf("first propose: %v", err)
	}
	preparesAfterFirst := nw.Prepares.Load()
	if preparesAfterFirst == 0 {
		t.Fatal("first propose must run phase 1")
	}
	for i := 0; i < 5; i++ {
		if _, err := leader.Propose([]byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("propose %d: %v", i, err)
		}
	}
	if got := nw.Prepares.Load(); got != preparesAfterFirst {
		t.Fatalf("lease must bypass phase 1: prepares grew %d -> %d", preparesAfterFirst, got)
	}
	for _, nd := range nodes {
		waitApplied(t, nd, 6)
	}
}

// TestHoleRepair Phase 1 必须修补空洞：无值槽位填 noop，最高提案号的值被选中。
func TestHoleRepair(t *testing.T) {
	nw, nodes := newCluster(t, 5)
	b1 := NewBallot(1, 0)
	// 手工注入：多数派 {0,1,2} 在槽 0、2 有值，槽 1 是空洞。
	for _, id := range []int{0, 1, 2} {
		nodes[id].HandleAccept(AcceptRequest{Ballot: b1, Slot: 0, Value: []byte("v0")})
		nodes[id].HandleAccept(AcceptRequest{Ballot: b1, Slot: 2, Value: []byte("v2")})
	}
	// 节点 0 竞选，修补后提交。
	if _, err := nodes[0].Propose([]byte("v3")); err != nil {
		t.Fatalf("propose: %v", err)
	}
	want := [][]byte{[]byte("v0"), nil, []byte("v2"), []byte("v3")}
	for _, id := range []int{0, 1, 2} {
		got := values(waitApplied(t, nodes[id], 4))
		for i := range want {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("node %d slot %d = %q, want %q", id, i, got[i], want[i])
			}
		}
	}
	_ = nw
}

// TestPartitionAcceptance 验收场景：
// 5 节点集群分区 {0,1}|{2,3,4}，旧 Leader(0) 被多数派新 Leader(2) 取代；
// 新 Leader 通过 Phase 1 补全未提交槽位并安全覆写少数派脏日志。
func TestPartitionAcceptance(t *testing.T) {
	nw, nodes := newCluster(t, 5)
	oldLeader, newLeader := nodes[0], nodes[2]

	// 1) 旧 Leader 正常提交 3 条日志。
	for _, v := range []string{"a", "b", "c"} {
		if _, err := oldLeader.Propose([]byte(v)); err != nil {
			t.Fatalf("propose %q: %v", v, err)
		}
	}
	for _, nd := range nodes {
		waitApplied(t, nd, 3)
	}

	// 2) 网络分区：{0,1} | {2,3,4}。
	nw.Partition([]int{0, 1}, []int{2, 3, 4})

	// 3) 旧 Leader 在少数派一侧：提案无法达到多数派，留下脏日志。
	if _, err := oldLeader.Propose([]byte("dirty")); err != ErrNoQuorum {
		t.Fatalf("old leader in minority must fail with ErrNoQuorum, got %v", err)
	}
	if s := nodes[0].log.load(3); s == nil || !s.hasValue || s.committed {
		t.Fatal("slot 3 should hold an uncommitted dirty value on node 0")
	}

	// 4) 多数派一侧选出新 Leader：Phase 1 补全未提交槽位。
	if _, err := newLeader.Propose([]byte("x")); err != nil {
		t.Fatalf("new leader propose: %v", err)
	}
	// 5) 租约内连续提交（绕过 Phase 1）。
	prepares := nw.Prepares.Load()
	if _, err := newLeader.Propose([]byte("y")); err != nil {
		t.Fatalf("new leader propose under lease: %v", err)
	}
	if nw.Prepares.Load() != prepares {
		t.Fatal("new leader must bypass phase 1 under lease")
	}
	for _, id := range []int{2, 3, 4} {
		got := values(waitApplied(t, nodes[id], 2))
		for i, want := range []string{"x", "y"} {
			if !bytes.Equal(got[i], []byte(want)) {
				t.Fatalf("node %d slot %d = %q, want %q", id, i+3, got[i], want)
			}
		}
	}

	// 6) 旧 Leader 已被取代：多数派不再接受它的写入。
	if _, err := oldLeader.Propose([]byte("stale")); err != ErrNoQuorum {
		t.Fatalf("deposed old leader must fail, got %v", err)
	}

	// 7) 分区愈合，新 Leader 同步已提交日志，安全覆写少数派脏日志。
	nw.Heal()
	newLeader.SyncTo(0)
	newLeader.SyncTo(1)

	// 少数派节点补上前缀 {a,b,c} 后，应继续应用 {x,y}（脏日志被覆写）。
	want := []string{"x", "y"}
	for _, id := range []int{0, 1} {
		got := values(waitApplied(t, nodes[id], 2))
		for i := range want {
			if !bytes.Equal(got[i], []byte(want[i])) {
				t.Fatalf("node %d slot %d = %q, want %q (dirty log not overwritten)", id, i+3, got[i], want[i])
			}
		}
	}
	// 旧 Leader 让位：不再持有租约。
	if oldLeader.HasLease() {
		t.Fatal("old leader must step down after seeing higher-ballot commits")
	}
	// 已提交前缀 {a,b,c} 在分区前后从未改变（安全不变量）。
	for _, nd := range nodes {
		s := nd.log.load(0)
		if s == nil || !s.committed || !bytes.Equal(s.value, []byte("a")) {
			t.Fatalf("node %d: committed slot 0 was altered", nd.id)
		}
	}
}

// TestConcurrentPropose 单 Leader 上并发提案：槽位唯一、全部提交、顺序一致。
func TestConcurrentPropose(t *testing.T) {
	_, nodes := newCluster(t, 5)
	leader := nodes[0]
	if _, err := leader.Propose([]byte("warmup")); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	slots := make([][]uint64, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				slot, err := leader.Propose([]byte(fmt.Sprintf("w%02d-%02d", w, i)))
				if err != nil {
					t.Errorf("propose: %v", err)
					return
				}
				slots[w] = append(slots[w], slot)
			}
		}(w)
	}
	wg.Wait()
	seen := make(map[uint64]bool)
	for w := range slots {
		for _, s := range slots[w] {
			if seen[s] {
				t.Fatalf("slot %d allocated twice", s)
			}
			seen[s] = true
		}
	}
	total := 1 + workers*perWorker
	for _, nd := range nodes {
		waitApplied(t, nd, total)
	}
}

// TestNoGoroutineLeak 严格断言：集群关闭后 goroutine 数量回落到基线。
func TestNoGoroutineLeak(t *testing.T) {
	baseline := runtime.NumGoroutine()
	func() {
		nw := NewNetwork(5)
		nodes := make([]*Node, 5)
		for i := range nodes {
			nodes[i] = NewNode(i, nw, testLease)
			nw.Register(nodes[i])
		}
		for i := 0; i < 10; i++ {
			if _, err := nodes[0].Propose([]byte(fmt.Sprintf("m%d", i))); err != nil {
				t.Fatalf("propose: %v", err)
			}
		}
		for _, nd := range nodes {
			waitApplied(t, nd, 10)
		}
		for _, nd := range nodes {
			nd.Close()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline=%d now=%d", baseline, runtime.NumGoroutine())
}

// TestDrainAfterClose 关闭后已应用的条目仍可消费，且队列最终为空（无残留）。
func TestDrainAfterClose(t *testing.T) {
	nw := NewNetwork(3)
	nodes := make([]*Node, 3)
	for i := range nodes {
		nodes[i] = NewNode(i, nw, testLease)
		nw.Register(nodes[i])
	}
	if _, err := nodes[0].Propose([]byte("only")); err != nil {
		t.Fatalf("propose: %v", err)
	}
	for _, nd := range nodes {
		waitApplied(t, nd, 1)
		nd.Close()
		if rest := drainApplied(nd); len(rest) != 0 {
			t.Fatalf("node %d: %d leftover entries", nd.id, len(rest))
		}
		if nd.appliedQ.Len() != 0 || nd.commitQ.Len() != 0 {
			t.Fatalf("node %d: queues not drained", nd.id)
		}
	}
}
