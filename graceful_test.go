package graceful

import (
	"sync"
	"testing"
	"time"
)

const testQuiescent = 100 * time.Millisecond

func newTestCluster(ids ...string) *Cluster {
	c := NewCluster(testQuiescent)
	for _, id := range ids {
		c.Join(id)
	}
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// 节点发起 Leave 后，其他节点应平滑地将其移出就绪路由池，
// 条目进入 Leaving 状态但尚未物理销毁。
func TestLeaveMovesNodeOutOfReadyPool(t *testing.T) {
	c := newTestCluster("A", "B", "C")
	b, _ := c.Node("B")

	b.Leave()

	for _, id := range []string{"A", "C"} {
		peer, _ := c.Node(id)
		if peer.Router.IsReady("B") {
			t.Fatalf("%s: B still in ready pool after leave", id)
		}
		st, ok := peer.Router.State("B")
		if !ok || st != StateLeaving {
			t.Fatalf("%s: B route state = %v, ok=%v; want Leaving", id, st, ok)
		}
	}
	if b.State() != StateLeaving {
		t.Fatalf("leaver state = %v; want Leaving", b.State())
	}
}

// 平静期内新流量被阻断：调用方本地快速失败（ErrNotReady），无 RPC 超时。
func TestNewTrafficBlockedDuringQuiescent(t *testing.T) {
	c := newTestCluster("A", "B")
	b, _ := c.Node("B")
	b.Leave()

	start := time.Now()
	err := c.Call("A", "B", func() { t.Error("work must not run") })
	if err != ErrNotReady {
		t.Fatalf("err = %v; want ErrNotReady", err)
	}
	if d := time.Since(start); d > 50*time.Millisecond {
		t.Fatalf("rejection took %v; want fast local failure, no RPC timeout", d)
	}
}

// 已在途的 RPC 在平静期内正常完成，不发生突发超时；
// 在途排空后节点转为 Dead。
func TestInflightCompletesDuringQuiescent(t *testing.T) {
	c := newTestCluster("A", "B")
	b, _ := c.Node("B")

	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- c.Call("A", "B", func() { <-release }) // 模拟慢请求
	}()

	// 等请求进入在途状态后再发起 Leave。
	waitFor(t, "inflight registered", func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.inflight == 1
	})
	b.Leave()

	// 平静期内新流量被拒绝，但在途请求不受影响。
	if err := c.Call("A", "B", func() {}); err != ErrNotReady {
		t.Fatalf("new call during quiescent: err = %v; want ErrNotReady", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("inflight call failed: %v", err)
	}
	waitFor(t, "leaver to become Dead after drain", func() bool {
		return b.State() == StateDead
	})
}

// 平静期定时器超时后，路由条目被彻底物理销毁。
func TestPhysicalRemovalAfterQuiescent(t *testing.T) {
	c := newTestCluster("A", "B", "C")
	b, _ := c.Node("B")
	b.Leave()

	a, _ := c.Node("A")
	if _, ok := a.Router.State("B"); !ok {
		t.Fatal("B route removed before quiescent expired")
	}

	waitFor(t, "physical removal of B route", func() bool {
		_, ok := a.Router.State("B")
		return !ok
	})
	// 条目销毁后，调用直接失败（目标已不存在）。
	if err := c.Call("A", "B", func() {}); err != ErrNotReady && err != ErrGone {
		t.Fatalf("call after removal: err = %v; want ErrNotReady or ErrGone", err)
	}
}

// 使用手动定时器确定性验证：平静期未到期不销毁，到期立即销毁。
func TestRemovalDrivenByQuiescentTimer(t *testing.T) {
	c := NewCluster(time.Hour) // 平静期足够长，由手动定时器控制
	a := c.Join("A")
	c.Join("B")

	// 替换 A 路由表的定时器为手动触发。
	var fire func()
	a.Router.afterFunc = func(_ time.Duration, f func()) *time.Timer {
		fire = f
		return time.NewTimer(time.Hour) // 永不触发的占位定时器
	}

	b, _ := c.Node("B")
	b.Leave()

	if _, ok := a.Router.State("B"); !ok {
		t.Fatal("route removed before quiescent timer fired")
	}
	if fire == nil {
		t.Fatal("quiescent timer was not scheduled")
	}
	fire() // 模拟定时器超时
	if _, ok := a.Router.State("B"); ok {
		t.Fatal("route still present after quiescent timer fired")
	}
}

// 并发场景：Leave 广播与并发 RPC 竞争时，不产生恐慌或意外错误，
// 所有调用要么成功、要么快速失败。
func TestConcurrentLeaveAndCalls(t *testing.T) {
	c := newTestCluster("A", "B", "C")
	b, _ := c.Node("B")

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				err := c.Call("A", "B", func() {})
				if err != nil && err != ErrNotReady && err != ErrDraining && err != ErrGone {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}()
	}
	b.Leave()
	wg.Wait()
}
