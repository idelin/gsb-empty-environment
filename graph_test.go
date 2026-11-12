package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// wipeTracker 记录宿主擦除回调的触发情况，并校验回调一定发生在字节清零之后。
type wipeTracker struct {
	mu    sync.Mutex
	wiped map[string]bool
	order []string
	fired chan string
}

func newWipeTracker() *wipeTracker {
	return &wipeTracker{wiped: make(map[string]bool), fired: make(chan string, 4096)}
}

func (t *wipeTracker) wipe(n *Node) {
	// 回调触发时图必须已经完成清零，否则即为提前擦除。
	for _, b := range n.Secret {
		if b != 0 {
			panic("wipe callback fired before secret was zeroed: " + n.ID)
		}
	}
	t.mu.Lock()
	if !t.wiped[n.ID] {
		t.wiped[n.ID] = true
		t.order = append(t.order, n.ID)
	}
	t.mu.Unlock()
	select {
	case t.fired <- n.ID:
	default:
	}
}

func (t *wipeTracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.wiped)
}

func (t *wipeTracker) has(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wiped[id]
}

func secretByte(b byte, n int) []byte {
	s := make([]byte, n)
	for i := range s {
		s[i] = b
	}
	return s
}

func isZeroed(s []byte) bool {
	for _, b := range s {
		if b != 0 {
			return false
		}
	}
	return true
}

// buildReadIndexGraph 构建 README 描述的典型拓扑：
// 一份明文来源同时派生读缓存副本、索引缓存副本和两级派生片段。
func buildReadIndexGraph(t *testing.T, tracker *wipeTracker) *Graph {
	t.Helper()
	g := NewGraph()
	must := func(n *Node, err error) *Node {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected register error: %v", err)
		}
		return n
	}
	must(g.RegisterSource("plain", 1, secretByte('A', 16), tracker.wipe))
	must(g.RegisterReplica("read-cache", "plain", secretByte('A', 16), tracker.wipe))
	must(g.RegisterReplica("index-cache", "plain", secretByte('A', 8), tracker.wipe))
	must(g.RegisterDerived("frag-a", []string{"plain"}, secretByte('B', 8), tracker.wipe))
	must(g.RegisterDerived("frag-b", []string{"frag-a"}, secretByte('C', 8), tracker.wipe))
	return g
}

// TestRevokeClosureWipesAllCopies 撤销来源必须清掉读缓存、索引缓存和全部
// 派生片段，一份副本都不能漏。
func TestRevokeClosureWipesAllCopies(t *testing.T) {
	tracker := newWipeTracker()
	g := buildReadIndexGraph(t, tracker)

	// 保留各节点底层字节引用，撤销后直接检查底层内存已清零。
	backing := map[string][]byte{}
	for _, id := range []string{"plain", "read-cache", "index-cache", "frag-a", "frag-b"} {
		n, err := g.Lookup(id)
		if err != nil {
			t.Fatalf("lookup %s: %v", id, err)
		}
		backing[id] = n.Secret
	}

	if err := g.Revoke("plain"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	want := []string{"plain", "read-cache", "index-cache", "frag-a", "frag-b"}
	if tracker.count() != len(want) {
		t.Fatalf("wipe callback count = %d, want %d (order=%v)", tracker.count(), len(want), tracker.order)
	}
	for _, id := range want {
		if !tracker.has(id) {
			t.Errorf("node %s was not wiped", id)
		}
		if !isZeroed(backing[id]) {
			t.Errorf("node %s secret still present in memory", id)
		}
		n, _ := g.Lookup(id)
		if n.Available() || !n.Wiped() {
			t.Errorf("node %s state = available:%v wiped:%v", id, n.Available(), n.Wiped())
		}
		if _, err := g.Acquire(id); !errors.Is(err, ErrRevoked) {
			t.Errorf("acquire after revoke on %s: err=%v, want ErrRevoked", id, err)
		}
	}

	// 已撤销来源下不允许再注册新副本。
	if _, err := g.RegisterReplica("late-replica", "plain", nil, tracker.wipe); !errors.Is(err, ErrRevoked) {
		t.Fatalf("register under revoked source: err=%v, want ErrRevoked", err)
	}
	// 重复撤销幂等，且不会重复触发擦除回调。
	if err := g.Revoke("plain"); err != nil {
		t.Fatalf("idempotent re-revoke should not error: %v", err)
	}
	if tracker.count() != len(want) {
		t.Fatalf("re-revoke produced extra wipes: %d", tracker.count())
	}
}

// TestSharedDerivedInvalidatedByAnySource 共享派生物含多个来源时，任一来源
// 撤销即失效；未被撤销的另一来源及其私有副本保持可用。
func TestSharedDerivedInvalidatedByAnySource(t *testing.T) {
	tracker := newWipeTracker()
	g := NewGraph()
	mustReg := func(n *Node, err error) *Node {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		return n
	}
	mustReg(g.RegisterSource("src-1", 1, secretByte('1', 8), tracker.wipe))
	mustReg(g.RegisterSource("src-2", 1, secretByte('2', 8), tracker.wipe))
	mustReg(g.RegisterReplica("src-2-replica", "src-2", secretByte('2', 8), tracker.wipe))
	shared := mustReg(g.RegisterDerived("shared", []string{"src-1", "src-2"}, secretByte('X', 8), tracker.wipe))
	sharedBacking := shared.Secret

	if err := g.Revoke("src-1"); err != nil {
		t.Fatalf("revoke src-1: %v", err)
	}

	for _, id := range []string{"src-1", "shared"} {
		if !tracker.has(id) {
			t.Errorf("%s not wiped after revoking one of its sources", id)
		}
	}
	if !isZeroed(sharedBacking) {
		t.Error("shared derived secret not zeroed")
	}
	if _, err := g.Acquire("shared"); !errors.Is(err, ErrRevoked) {
		t.Errorf("acquire shared: %v, want ErrRevoked", err)
	}

	// src-2 及其私有副本不在 src-1 的闭包内，必须保持可用且字节完好。
	for _, id := range []string{"src-2", "src-2-replica"} {
		if tracker.has(id) {
			t.Errorf("%s wrongly wiped", id)
		}
		h, err := g.Acquire(id)
		if err != nil {
			t.Errorf("acquire %s after partial revoke: %v", id, err)
			continue
		}
		if isZeroed(h.Secret()) {
			t.Errorf("%s secret prematurely zeroed", id)
		}
		h.Close()
	}

	// 撤销 src-2 时，shared 已擦除不应重复回调，src-2 闭包照常清理。
	before := tracker.count()
	if err := g.Revoke("src-2"); err != nil {
		t.Fatalf("revoke src-2: %v", err)
	}
	for _, id := range []string{"src-2", "src-2-replica"} {
		if !tracker.has(id) {
			t.Errorf("%s not wiped on second revoke", id)
		}
	}
	if tracker.count() != before+2 {
		t.Errorf("already-wiped shared node re-wiped: count delta=%d, want 2", tracker.count()-before)
	}
}

// TestRevokeWaitsForUsersNoEarlyWipe 撤销必须等待存量使用者退出：
// 句柄持有期间字节绝不允许提前清零，关闭后才擦除。
func TestRevokeWaitsForUsersNoEarlyWipe(t *testing.T) {
	tracker := newWipeTracker()
	g := buildReadIndexGraph(t, tracker)

	h, err := g.Acquire("frag-b")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	want := append([]byte(nil), h.Secret()...)

	done := make(chan struct{})
	go func() {
		if err := g.Revoke("plain"); err != nil {
			t.Errorf("revoke: %v", err)
		}
		close(done)
	}()

	// 等待撤销完成“标记不可用”但停在“等待使用者退出”阶段。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, _ := g.Lookup("plain")
		if !n.Available() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	n, _ := g.Lookup("plain")
	if n.Available() {
		t.Fatal("revoke did not mark nodes unavailable while waiting")
	}

	// 标记不可用后新 Acquire 立即失败，且存量字节完好。
	if _, err := g.Acquire("plain"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("acquire during draining: %v, want ErrRevoked", err)
	}
	if !bytes.Equal(h.Secret(), want) {
		t.Fatal("secret changed/zeroed while handle still held (early wipe)")
	}
	select {
	case <-done:
		t.Fatal("revoke returned before active user exited")
	default:
	}

	h.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("revoke did not complete after user exited")
	}

	for _, id := range []string{"plain", "read-cache", "index-cache", "frag-a", "frag-b"} {
		node, _ := g.Lookup(id)
		if !node.Wiped() || !isZeroed(node.Secret) {
			t.Errorf("node %s not fully wiped after drain", id)
		}
	}
	if h.Secret() != nil {
		t.Error("closed/revoked handle must not expose secret")
	}
}

// TestRevokeGeneration 密钥代撤销应清理该代全部对象，其他代不受影响。
func TestRevokeGeneration(t *testing.T) {
	tracker := newWipeTracker()
	g := NewGraph()
	mustReg := func(n *Node, err error) *Node {
		t.Helper()
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		return n
	}
	old := mustReg(g.RegisterSource("old-plain", 7, secretByte('O', 8), tracker.wipe))
	mustReg(g.RegisterReplica("old-read", "old-plain", secretByte('O', 8), tracker.wipe))
	fresh := mustReg(g.RegisterSource("new-plain", 8, secretByte('N', 8), tracker.wipe))
	mustReg(g.RegisterReplica("new-read", "new-plain", secretByte('N', 8), tracker.wipe))

	if err := g.RevokeGeneration(7); err != nil {
		t.Fatalf("revoke generation: %v", err)
	}
	if !old.Wiped() || !isZeroed(old.Secret) {
		t.Error("generation 7 source not wiped")
	}
	if !tracker.has("old-read") {
		t.Error("generation 7 replica not wiped")
	}
	if fresh.Wiped() || !fresh.Available() {
		t.Error("generation 8 object wrongly affected")
	}
	h, err := g.Acquire("new-read")
	if err != nil {
		t.Errorf("new generation replica should remain usable: %v", err)
	} else {
		h.Close()
	}
	// 重复撤销空集合应安全返回。
	if err := g.RevokeGeneration(7); err != nil {
		t.Errorf("re-revoke generation: %v", err)
	}
}

// TestRegistrationGuards 覆盖注册阶段的基本保护。
func TestRegistrationGuards(t *testing.T) {
	tracker := newWipeTracker()
	g := NewGraph()
	if _, err := g.RegisterSource("s", 1, secretByte('a', 4), tracker.wipe); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RegisterSource("s", 1, nil, tracker.wipe); err == nil {
		t.Error("duplicate source id should fail")
	}
	if _, err := g.RegisterReplica("r", "missing", nil, tracker.wipe); !errors.Is(err, ErrNotFound) {
		t.Errorf("replica of missing source: %v", err)
	}
	if _, err := g.RegisterDerived("d", nil, nil, tracker.wipe); err == nil {
		t.Error("derived without sources should fail")
	}
	if _, err := g.RegisterDerived("d", []string{"missing"}, nil, tracker.wipe); !errors.Is(err, ErrNotFound) {
		t.Errorf("derived from missing source: %v", err)
	}
	g2 := NewGraph()
	if _, err := g2.RegisterSource("a", 1, nil, tracker.wipe); err != nil {
		t.Fatal(err)
	}
	if _, err := g2.RegisterSource("b", 2, nil, tracker.wipe); err != nil {
		t.Fatal(err)
	}
	if _, err := g2.RegisterDerived("d", []string{"a", "b"}, nil, tracker.wipe); err == nil {
		t.Error("mixed-generation derived object should fail")
	}
	if err := g.Revoke("ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoke missing: %v", err)
	}
}

// TestConcurrentDeriveAndRevoke 并发派生与撤销压力测试。
//
// 固定拓扑上跑多轮：多个读取工作者持续 Acquire/读/Close，多个注册工作者
// 并发追加派生副本，多个撤销者并发撤销不同来源。全程校验两条核心不变量：
//  1. 无提前清零——成功获取的句柄在持有期内读到的字节必须非零且内容稳定；
//  2. 无漏清——Revoke 返回后，来源闭包内每个节点都已 wiped 且底层字节全零。
func TestConcurrentDeriveAndRevoke(t *testing.T) {
	const (
		sources    = 6
		readers    = 8
		derivers   = 4
		revokers   = 3
		iterations = 300
	)

	for round := 0; round < 4; round++ {
		tracker := newWipeTracker()
		g := NewGraph()

		sourceIDs := make([]string, 0, sources)
		for i := 0; i < sources; i++ {
			id := fmt.Sprintf("src-%d", i)
			fill := byte('A' + i)
			if _, err := g.RegisterSource(id, 1, secretByte(fill, 32), tracker.wipe); err != nil {
				t.Fatalf("register source: %v", err)
			}
			sourceIDs = append(sourceIDs, id)
		}

		var derivedSeq atomic.Int64
		var earlyWipe, badContent, leaked atomic.Int64
		var wg sync.WaitGroup
		stop := make(chan struct{})

		// 读取工作者：随机在来源与已注册派生物上获取句柄，持有期间反复读取，
		// 验证字节既不会提前清零，也不会在句柄存活期内被改写。
		for r := 0; r < readers; r++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					id := sourceIDs[int(derivedSeq.Add(0))%len(sourceIDs)]
					h, err := g.Acquire(id)
					if errors.Is(err, ErrRevoked) {
						continue
					}
					if err != nil {
						t.Errorf("acquire: %v", err)
						return
					}
					first := append([]byte(nil), h.Secret()...)
					if isZeroed(first) {
						earlyWipe.Add(1)
						h.Close()
						continue
					}
					// 持有句柄时多采几次样，任何一次变化都意味着提前清零/改写。
					for k := 0; k < 5; k++ {
						cur := h.Secret()
						if cur == nil || !bytes.Equal(cur, first) {
							badContent.Add(1)
							break
						}
					}
					h.Close()
				}
			}()
		}

		// 派生工作者：并发注册新的派生副本（含多来源共享派生物），
		// 注册后立刻持有一次句柄读取，成功读到的内容必须非零。
		for d := 0; d < derivers; d++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				for i := 0; i < iterations; i++ {
					seq := derivedSeq.Add(1)
					id := fmt.Sprintf("derived-%d-%d", worker, i)
					srcA := sourceIDs[int(seq)%sources]
					var err error
					if seq%3 == 0 {
						srcB := sourceIDs[(int(seq)+1)%sources]
						_, err = g.RegisterDerived(id, []string{srcA, srcB}, secretByte('Z', 16), tracker.wipe)
					} else {
						_, err = g.RegisterDerived(id, []string{srcA}, secretByte('Z', 16), tracker.wipe)
					}
					if errors.Is(err, ErrRevoked) {
						continue
					}
					if err != nil {
						t.Errorf("register derived: %v", err)
						return
					}
					h, err := g.Acquire(id)
					if errors.Is(err, ErrRevoked) {
						continue
					}
					if err != nil {
						t.Errorf("acquire fresh derived: %v", err)
						return
					}
					if isZeroed(h.Secret()) {
						earlyWipe.Add(1)
					}
					h.Close()
				}
			}(d)
		}

		// 撤销工作者：并发撤销不同来源，Revoke 返回即代表闭包已彻底擦除。
		revokeDone := make(chan struct{})
		for v := 0; v < revokers; v++ {
			wg.Add(1)
			go func(worker int) {
				defer wg.Done()
				id := sourceIDs[worker]
				if err := g.Revoke(id); err != nil {
					t.Errorf("revoke %s: %v", id, err)
					return
				}
				// 撤销返回后做闭包复核：该来源及其全部派生物必须已清零。
				g.mu.Lock()
				root := g.nodes[id]
				closure := g.closureLocked([]*Node{root})
				for n := range closure {
					if !n.wiped || !isZeroed(n.Secret) {
						leaked.Add(1)
					}
				}
				g.mu.Unlock()
				if worker == 0 {
					close(revokeDone)
				}
			}(v)
		}

		// 等首个撤销完成后再让读取者收敛，保证读取与撤销充分交错。
		select {
		case <-revokeDone:
		case <-time.After(10 * time.Second):
			t.Fatal("revokers stalled")
		}
		close(stop)
		wg.Wait()

		// 撤销剩余来源，使整图进入终态，然后做全局无漏清复核。
		for v := revokers; v < sources; v++ {
			if err := g.Revoke(sourceIDs[v]); err != nil {
				t.Fatalf("final revoke %s: %v", sourceIDs[v], err)
			}
		}

		if earlyWipe.Load() != 0 || badContent.Load() != 0 {
			t.Fatalf("round %d: early wipe=%d content mutation=%d (no-early-zero violated)",
				round, earlyWipe.Load(), badContent.Load())
		}
		if leaked.Load() != 0 {
			t.Fatalf("round %d: %d closure nodes not fully wiped on revoke return", round, leaked.Load())
		}

		g.mu.Lock()
		nodeCount := len(g.nodes)
		// 部分来源提前撤销后，随后的派生注册会合法地收到 ErrRevoked，
		// 因此实际节点数不固定；但每个实际注册成功的节点都必须终态干净。
		for id, n := range g.nodes {
			if n.available || !n.wiped || !isZeroed(n.Secret) || n.users != 0 {
				t.Errorf("round %d: node %s left dirty: available=%v wiped=%v users=%d",
					round, id, n.available, n.wiped, n.users)
			}
		}
		g.mu.Unlock()

		if tracker.count() != nodeCount {
			t.Errorf("round %d: wipe callbacks=%d, want %d (one per registered node)",
				round, tracker.count(), nodeCount)
		}
	}
}

// TestConcurrentRevokeGenerationDrain 验证密钥代撤销在并发使用者存在时
// 仍满足“等待退出再擦除”，且不会漏清任何副本。
func TestConcurrentRevokeGenerationDrain(t *testing.T) {
	const holders = 16
	tracker := newWipeTracker()
	g := NewGraph()

	if _, err := g.RegisterSource("plain", 3, secretByte('K', 64), tracker.wipe); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("replica-%d", i)
		if _, err := g.RegisterReplica(id, "plain", secretByte('K', 32), tracker.wipe); err != nil {
			t.Fatal(err)
		}
	}

	handles := make([]*Handle, 0, holders)
	release := make(chan struct{})
	var holderWG sync.WaitGroup
	for i := 0; i < holders; i++ {
		h, err := g.Acquire("plain")
		if err != nil {
			t.Fatalf("acquire holder %d: %v", i, err)
		}
		handles = append(handles, h)
		holderWG.Add(1)
		go func(handle *Handle) {
			defer holderWG.Done()
			<-release
			handle.Close()
		}(h)
	}

	done := make(chan struct{})
	go func() {
		if err := g.RevokeGeneration(3); err != nil {
			t.Errorf("revoke generation: %v", err)
		}
		close(done)
	}()

	// 撤销进行中：所有节点应已不可用，但字节在使用者退出前必须保持原样。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n, _ := g.Lookup("plain")
		if !n.Available() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	n, _ := g.Lookup("plain")
	if n.Available() {
		t.Fatal("generation revoke did not mark nodes unavailable")
	}
	for _, h := range handles {
		if isZeroed(h.Secret()) {
			t.Fatal("secret zeroed while generation users still active")
		}
	}
	select {
	case <-done:
		t.Fatal("generation revoke completed before users exited")
	default:
	}

	close(release)
	holderWG.Wait()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("generation revoke did not finish after drain")
	}

	if tracker.count() != 9 {
		t.Errorf("wipe callbacks=%d, want 9 (source + 8 replicas)", tracker.count())
	}
	g.mu.Lock()
	for id, node := range g.nodes {
		if !node.wiped || !isZeroed(node.Secret) || node.users != 0 {
			t.Errorf("node %s left dirty: wiped=%v users=%d", id, node.wiped, node.users)
		}
	}
	g.mu.Unlock()
}
