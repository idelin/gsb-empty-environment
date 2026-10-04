package statewait

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"
)

func atLeast(n int) Predicate {
	return func(s interface{}) bool { return s.(int) >= n }
}

// 谓词在登记时已满足：立即返回，不阻塞。
func TestWaitAlreadySatisfied(t *testing.T) {
	h := New(5)
	ev, err := h.Wait(context.Background(), atLeast(3))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ev.State.(int) != 5 || ev.Version != 0 {
		t.Fatalf("got %+v, want state=5 version=0", ev)
	}
}

// 基本路径：登记后由 Update 唤醒。
func TestWaitWokenByUpdate(t *testing.T) {
	h := New(0)
	done := make(chan Event, 1)
	go func() {
		ev, err := h.Wait(context.Background(), atLeast(10))
		if err != nil {
			t.Errorf("unexpected err: %v", err)
		}
		done <- ev
	}()
	h.Update(3)
	h.Update(10)
	select {
	case ev := <-done:
		if ev.State.(int) != 10 || ev.Version != 2 {
			t.Fatalf("got %+v, want state=10 version=2", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not woken")
	}
}

// 检查后登记竞态：Wait 与 Update 紧密交错，任何一次错过唤醒都会
// 表现为超时错误。
func TestNoMissedWakeup(t *testing.T) {
	for i := 0; i < 2000; i++ {
		h := New(0)
		done := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := h.Wait(ctx, atLeast(1))
			done <- err
		}()
		h.Update(1)
		if err := <-done; err != nil {
			t.Fatalf("iter %d: missed wakeup: %v", i, err)
		}
	}
}

// 取消与状态满足竞争：Wait 恰好返回一次，结果非此即彼；
// 竞争结束后继续 Update 不会阻塞或 panic（无泄漏的唤醒发送）。
func TestCancelSatisfactionRace(t *testing.T) {
	for i := 0; i < 2000; i++ {
		h := New(0)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := h.Wait(ctx, atLeast(1))
			done <- err
		}()
		go h.Update(1)
		go cancel()
		err := <-done // 恰好返回一次
		if err != nil && err != context.Canceled {
			t.Fatalf("iter %d: unexpected err: %v", i, err)
		}
		h.Update(2) // 已注销的 waiter 不应再收到发送
	}
}

// 事件历史校验：并发更新与等待，每个成功等待返回的事件必须
// 对应历史中真实可见的状态（版本存在、状态一致、谓词成立）。
func TestEventHistoryConsistency(t *testing.T) {
	const updaters = 4
	const increments = 250
	const waiters = 64
	const total = updaters * increments

	h := New(0)
	results := make([]Event, waiters)
	thresholds := make([]int, waiters)

	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		thresholds[i] = 1 + rand.Intn(total)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ev, err := h.Wait(context.Background(), atLeast(thresholds[i]))
			if err != nil {
				t.Errorf("waiter %d: %v", i, err)
				return
			}
			results[i] = ev
		}(i)
	}
	for u := 0; u < updaters; u++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < increments; k++ {
				h.UpdateFunc(func(old interface{}) interface{} {
					return old.(int) + 1
				})
			}
		}()
	}
	wg.Wait()

	// 历史自身一致性：版本从 0 连续递增，版本 v 的状态即 v。
	history := h.History()
	if len(history) != total+1 {
		t.Fatalf("history len = %d, want %d", len(history), total+1)
	}
	byVersion := make(map[uint64]interface{}, len(history))
	for i, ev := range history {
		if ev.Version != uint64(i) {
			t.Fatalf("history[%d].Version = %d", i, ev.Version)
		}
		if ev.State.(int) != i {
			t.Fatalf("history[%d].State = %v", i, ev.State)
		}
		byVersion[ev.Version] = ev.State
	}

	// 每个成功等待对应真实可见状态。
	final := h.Snapshot()
	for i, ev := range results {
		state, ok := byVersion[ev.Version]
		if !ok {
			t.Fatalf("waiter %d: version %d not in history", i, ev.Version)
		}
		if state != ev.State {
			t.Fatalf("waiter %d: returned state %v != history state %v (version %d)",
				i, ev.State, state, ev.Version)
		}
		if ev.State.(int) < thresholds[i] {
			t.Fatalf("waiter %d: predicate false on returned state %v (threshold %d)",
				i, ev.State, thresholds[i])
		}
		if ev.Version > final.Version {
			t.Fatalf("waiter %d: version %d not yet visible (final %d)",
				i, ev.Version, final.Version)
		}
	}
}

// 并发取消：所有等待者最终都返回，且成功结果仍在历史中。
func TestConcurrentCancel(t *testing.T) {
	h := New(0)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(),
				time.Duration(rand.Intn(5))*time.Millisecond)
			defer cancel()
			h.Wait(ctx, atLeast(1<<30)) // 永不满足
		}(i)
	}
	wg.Wait()
	if got := h.Snapshot().Version; got != 0 {
		t.Fatalf("version = %d, want 0", got)
	}
}
