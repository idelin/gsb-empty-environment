package leaseread

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// newCluster 构建 n 节点集群，返回主节点优化器与传输层。
func newCluster(n int, latency time.Duration, safety time.Duration) (*ReadOptimizer, *SimTransport, *LeaseManager) {
	nodes := make(map[int]*Node, n)
	for i := 0; i < n; i++ {
		nodes[i] = &Node{ID: i, Store: NewKVStore()}
	}
	transport := NewSimTransport(nodes, latency)
	peers := make([]int, 0, n-1)
	for i := 1; i < n; i++ {
		peers = append(peers, i)
	}
	lm := NewLeaseManager(safety)
	ro := NewReadOptimizer(0, nodes[0], peers, transport, lm)
	return ro, transport, lm
}

// 验收场景 1：租约绝对安全窗口内，1 万次并发读全部走本地，零网络开销。
func TestLeaseReadZeroNetwork(t *testing.T) {
	ro, transport, lm := newCluster(3, 2*time.Millisecond, 10*time.Millisecond)
	ctx := context.Background()

	// 通过多数派心跳获得租约（租约路径的网络开销，按租约周期摊销）。
	if err := lm.Renew(ctx, transport, 0, []int{1, 2}, 10*time.Second); err != nil {
		t.Fatalf("renew lease: %v", err)
	}
	if err := ro.Write(ctx, "hot-key", "v1", 1); err != nil {
		t.Fatalf("write: %v", err)
	}

	// 记录基线：租约续约心跳 + 写复制不计入读路径开销。
	baseCalls := transport.TotalCalls()

	const reads = 10000
	var wg sync.WaitGroup
	errs := make(chan error, reads)
	start := time.Now()
	for i := 0; i < reads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := ro.Read(ctx, "hot-key")
			if err != nil {
				errs <- err
				return
			}
			if v.Value != "v1" || v.Version != 1 {
				errs <- fmt.Errorf("stale read: got %+v", v)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if got := ro.LocalReads.Load(); got != reads {
		t.Fatalf("local reads = %d, want %d", got, reads)
	}
	if got := ro.QuorumReads.Load(); got != 0 {
		t.Fatalf("quorum reads = %d, want 0", got)
	}
	if got := transport.TotalCalls() - baseCalls; got != 0 {
		t.Fatalf("network calls during reads = %d, want 0 (zero network cost)", got)
	}
	t.Logf("10000 concurrent lease reads finished in %v (%.0f reads/s), network calls on read path: 0",
		elapsed, float64(reads)/elapsed.Seconds())
}

// 验收场景 2：租约剩余不足 100ms 时，自动降级为全量网络心跳确认读。
func TestDegradeWhenLeaseBelowThreshold(t *testing.T) {
	ro, transport, lm := newCluster(3, time.Millisecond, 0)
	ctx := context.Background()

	if err := ro.Write(ctx, "k", "v9", 9); err != nil {
		t.Fatalf("write: %v", err)
	}
	// 授予一个仅剩 50ms 的租约：低于 100ms 降级阈值。
	lm.Grant(50 * time.Millisecond)
	if lm.InSafeWindow(DefaultDegradeThreshold) {
		t.Fatal("expected lease to be outside safe window")
	}

	baseFetch := transport.FetchCallsCount()
	const reads = 100
	for i := 0; i < reads; i++ {
		v, err := ro.Read(ctx, "k")
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if v.Version != 9 {
			t.Fatalf("non-linearizable read: got version %d, want 9", v.Version)
		}
	}
	if got := ro.QuorumReads.Load(); got != reads {
		t.Fatalf("quorum reads = %d, want %d", got, reads)
	}
	if got := ro.LocalReads.Load(); got != 0 {
		t.Fatalf("local reads = %d, want 0 (must degrade)", got)
	}
	if got := transport.FetchCallsCount() - baseFetch; got <= 0 {
		t.Fatalf("expected network confirmation calls, got %d", got)
	}
	t.Logf("degraded path: %d reads, %d fetch RPCs (quorum confirmation)",
		reads, transport.FetchCallsCount()-baseFetch)
}

// 租约续约后恢复快路径；续约本身产生心跳网络开销。
func TestLeaseRenewalRestoresFastPath(t *testing.T) {
	ro, transport, lm := newCluster(5, time.Millisecond, 0)
	ctx := context.Background()

	if err := ro.Write(ctx, "k", "v1", 1); err != nil {
		t.Fatalf("write: %v", err)
	}
	lm.Grant(10 * time.Millisecond) // 即将过期 -> 降级
	if _, err := ro.Read(ctx, "k"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if ro.QuorumReads.Load() != 1 {
		t.Fatal("expected degraded read")
	}

	baseHB := transport.HeartbeatCallsCount()
	if err := lm.Renew(ctx, transport, 0, []int{1, 2, 3, 4}, 10*time.Second); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if got := transport.HeartbeatCallsCount() - baseHB; got != 4 {
		t.Fatalf("heartbeat calls = %d, want 4", got)
	}

	if _, err := ro.Read(ctx, "k"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if ro.LocalReads.Load() != 1 {
		t.Fatal("expected fast-path read after renewal")
	}
}

// 线性一致性：并发写完成后，所有读必须观察到最高版本。
func TestLinearizableUnderConcurrentWrites(t *testing.T) {
	ro, _, lm := newCluster(3, time.Millisecond, 5*time.Millisecond)
	ctx := context.Background()
	lm.Grant(10 * time.Second)

	const writers = 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 1; i <= 20; i++ {
				_ = ro.Write(ctx, "k", fmt.Sprintf("w%d-i%d", w, i), uint64(w*1000+i))
			}
		}(w)
	}
	wg.Wait()

	v, err := ro.Read(ctx, "k")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if v.Version != uint64((writers-1)*1000+20) {
		t.Fatalf("read version = %d, want max committed %d", v.Version, (writers-1)*1000+20)
	}
}

// 无租约时绝不走本地读（防止脑裂脏读）。
func TestNoLeaseNoLocalRead(t *testing.T) {
	ro, _, _ := newCluster(3, time.Millisecond, 0)
	ctx := context.Background()
	if err := ro.Write(ctx, "k", "v1", 1); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ro.Read(ctx, "k"); err != nil {
		t.Fatalf("read: %v", err)
	}
	if ro.LocalReads.Load() != 0 {
		t.Fatal("local read without lease: linearizability violation")
	}
	if ro.QuorumReads.Load() != 1 {
		t.Fatal("expected quorum read without lease")
	}
}
