package singleflight

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// waitCond 轮询直到条件满足或超时。
func waitCond(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for: %s", msg)
		}
		time.Sleep(time.Millisecond)
	}
}

// waiterCount 返回 key 对应航班的等待者数量（白盒）。
func (g *Group) waiterCount(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if f, ok := g.flights[key]; ok {
		return len(f.waiters)
	}
	return -1
}

// flightGen 返回 key 对应航班的代次（白盒）。
func (g *Group) flightGen(key string) (uint64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if f, ok := g.flights[key]; ok {
		return f.gen, true
	}
	return 0, false
}

func (g *Group) flightCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.flights)
}

// blockedFn 返回一个 fn：进入后关闭 entered，阻塞在 release 上，随后返回 val。
func blockedFn(entered chan<- struct{}, release <-chan struct{}, val string) func(context.Context, *Handle) (interface{}, error) {
	return func(context.Context, *Handle) (interface{}, error) {
		close(entered)
		<-release
		return val, nil
	}
}

func TestDoDeduplicates(t *testing.T) {
	g := NewGroup()
	entered := make(chan struct{})
	release := make(chan struct{})
	var execs int32

	go func() {
		g.Do(context.Background(), "k", func(ctx context.Context, h *Handle) (interface{}, error) {
			atomic.AddInt32(&execs, 1)
			return blockedFn(entered, release, "v")(ctx, h)
		})
	}()
	<-entered

	const n = 5
	results := make(chan Result, n)
	for i := 0; i < n; i++ {
		go func() { results <- g.Do(context.Background(), "k", nil) }()
	}
	waitCond(t, "5 waiters", func() bool { return g.waiterCount("k") == n })
	close(release)

	var gen uint64
	for i := 0; i < n; i++ {
		r := <-results
		if r.Val != "v" || !r.Shared {
			t.Fatalf("waiter got %+v, want shared v", r)
		}
		if i == 0 {
			gen = r.Gen
		} else if r.Gen != gen {
			t.Fatalf("waiters disagree on generation: %d vs %d", r.Gen, gen)
		}
	}
	if got := atomic.LoadInt32(&execs); got != 1 {
		t.Fatalf("fn executed %d times, want 1", got)
	}
	waitCond(t, "map drained", func() bool { return g.flightCount() == 0 })
}

func TestPromoteToFreeKey(t *testing.T) {
	g := NewGroup()
	entered := make(chan struct{})
	canPromote := make(chan struct{})
	promoted := make(chan struct{})
	release := make(chan struct{})

	execRes := make(chan Result, 1)
	go func() {
		execRes <- g.Do(context.Background(), "tmp", func(ctx context.Context, h *Handle) (interface{}, error) {
			close(entered)
			<-canPromote
			if !h.Promote("formal") {
				t.Error("promote to free key failed")
			}
			close(promoted)
			<-release
			return "T", nil
		})
	}()
	<-entered
	close(canPromote)
	<-promoted

	// 旧键已释放：新请求必须开启明确的新代次，而不是挂到已迁走的航班上。
	newRes := make(chan Result, 1)
	go func() {
		newRes <- g.Do(context.Background(), "tmp", func(context.Context, *Handle) (interface{}, error) {
			return "N", nil
		})
	}()

	// 正式键上的新请求应加入迁移过来的航班。
	joined := make(chan Result, 1)
	var otherExecs int32
	go func() {
		joined <- g.Do(context.Background(), "formal", func(context.Context, *Handle) (interface{}, error) {
			atomic.AddInt32(&otherExecs, 1)
			return "other", nil
		})
	}()
	waitCond(t, "waiter on formal", func() bool { return g.waiterCount("formal") == 1 })

	nr := <-newRes
	close(release)
	tr, jr := <-execRes, <-joined

	if tr.Val != "T" || tr.Shared {
		t.Fatalf("executor got %+v, want own T", tr)
	}
	if jr.Val != "T" || !jr.Shared || jr.Gen != tr.Gen {
		t.Fatalf("formal waiter got %+v, want shared T gen %d", jr, tr.Gen)
	}
	if nr.Val != "N" || nr.Shared {
		t.Fatalf("old-key request got %+v, want fresh execution", nr)
	}
	if nr.Gen == tr.Gen {
		t.Fatalf("old-key request reused generation %d, want a new explicit one", nr.Gen)
	}
	if got := atomic.LoadInt32(&otherExecs); got != 0 {
		t.Fatalf("formal fn executed %d times, want 0 (joined promoted flight)", got)
	}
	waitCond(t, "map drained", func() bool { return g.flightCount() == 0 })
}

func TestPromoteMergesWaiters(t *testing.T) {
	g := NewGroup()
	enteredA := make(chan struct{})
	releaseA := make(chan struct{})
	genA := make(chan uint64, 1)

	// 正式键上的既有任务。
	go func() {
		g.Do(context.Background(), "formal", func(ctx context.Context, h *Handle) (interface{}, error) {
			genA <- h.Gen()
			return blockedFn(enteredA, releaseA, "A")(ctx, h)
		})
	}()
	<-enteredA

	// 临时键任务，运行中升级到已有任务的正式键。
	enteredB := make(chan struct{})
	canPromote := make(chan struct{})
	promoted := make(chan struct{})
	releaseB := make(chan struct{})
	execB := make(chan Result, 1)
	go func() {
		execB <- g.Do(context.Background(), "tmp", func(context.Context, *Handle) (interface{}, error) {
			close(enteredB)
			<-canPromote
			if !g.Promote("tmp", "formal") {
				t.Error("promote onto existing flight failed")
			}
			close(promoted)
			<-releaseB
			return "B", nil
		})
	}()
	<-enteredB

	// 两个等待者挂在临时键上。
	ctx1, cancel1 := context.WithCancel(context.Background())
	w1 := make(chan Result, 1)
	w2 := make(chan Result, 1)
	go func() { w1 <- g.Do(ctx1, "tmp", nil) }()
	go func() { w2 <- g.Do(context.Background(), "tmp", nil) }()
	waitCond(t, "2 waiters on tmp", func() bool { return g.waiterCount("tmp") == 2 })

	close(canPromote)
	<-promoted

	// 合并后：tmp 键释放，等待者并入 formal 航班。
	if _, ok := g.flightGen("tmp"); ok {
		t.Fatal("tmp still mapped after promote")
	}
	waitCond(t, "waiters merged into formal", func() bool { return g.waiterCount("formal") == 2 })

	// 取消其中一个被合并的等待者，不影响另一个。
	cancel1()
	if r := <-w1; r.Err != context.Canceled {
		t.Fatalf("cancelled waiter got %+v, want context.Canceled", r)
	}
	waitCond(t, "one waiter left", func() bool { return g.waiterCount("formal") == 1 })

	close(releaseA)
	r2 := <-w2
	wantGen := <-genA
	if r2.Val != "A" || !r2.Shared || r2.Gen != wantGen {
		t.Fatalf("merged waiter got %+v, want shared A gen %d", r2, wantGen)
	}

	close(releaseB)
	if rb := <-execB; rb.Val != "B" || rb.Shared {
		t.Fatalf("merged executor got %+v, want its own B", rb)
	}
	waitCond(t, "map drained", func() bool { return g.flightCount() == 0 })
}

// 核心验证：两个临时键上的任务同时迁往同一正式键。
func TestTwoTempsPromoteToSameFormal(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		g := NewGroup()
		entered1 := make(chan struct{})
		entered2 := make(chan struct{})
		startPromote := make(chan struct{})
		promoted := make(chan struct{}, 2)
		release := make(chan struct{})
		exec := make(chan Result, 2)

		task := func(tmpKey, val string) func(context.Context, *Handle) (interface{}, error) {
			return func(ctx context.Context, h *Handle) (interface{}, error) {
				if tmpKey == "tmp1" {
					close(entered1)
				} else {
					close(entered2)
				}
				<-startPromote
				h.Promote("formal")
				promoted <- struct{}{}
				<-release
				return val, nil
			}
		}
		go func() { exec <- g.Do(context.Background(), "tmp1", task("tmp1", "T1")) }()
		go func() { exec <- g.Do(context.Background(), "tmp2", task("tmp2", "T2")) }()
		<-entered1
		<-entered2

		// 每个临时键上各挂一个等待者。
		w1 := make(chan Result, 1)
		w2 := make(chan Result, 1)
		go func() { w1 <- g.Do(context.Background(), "tmp1", nil) }()
		go func() { w2 <- g.Do(context.Background(), "tmp2", nil) }()
		waitCond(t, "waiter on tmp1", func() bool { return g.waiterCount("tmp1") == 1 })
		waitCond(t, "waiter on tmp2", func() bool { return g.waiterCount("tmp2") == 1 })

		// 同时迁移。
		close(startPromote)
		<-promoted
		<-promoted

		// 迁移后：formal 上恰有一个航班，两个等待者都并入其中；旧键均释放。
		winnerGen, ok := g.flightGen("formal")
		if !ok {
			t.Fatal("iter", iter, ": no flight under formal after promotes")
		}
		if _, ok := g.flightGen("tmp1"); ok {
			t.Fatal("iter", iter, ": tmp1 still mapped")
		}
		if _, ok := g.flightGen("tmp2"); ok {
			t.Fatal("iter", iter, ": tmp2 still mapped")
		}
		waitCond(t, "both waiters merged", func() bool { return g.waiterCount("formal") == 2 })

		// 正式键上的新请求加入胜出的航班。
		w3 := make(chan Result, 1)
		go func() { w3 <- g.Do(context.Background(), "formal", nil) }()
		waitCond(t, "third waiter joined", func() bool { return g.waiterCount("formal") == 3 })

		close(release)
		r1, r2, r3 := <-w1, <-w2, <-w3
		for _, r := range []Result{r1, r2, r3} {
			if !r.Shared || r.Gen != winnerGen {
				t.Fatalf("iter %d: waiter got %+v, want shared result of gen %d", iter, r, winnerGen)
			}
		}
		if r1.Val != r2.Val || r2.Val != r3.Val {
			t.Fatalf("iter %d: waiters got different values: %v %v %v", iter, r1.Val, r2.Val, r3.Val)
		}

		// 两个任务都正常执行完毕，各自拿到自己的结果。
		e1, e2 := <-exec, <-exec
		vals := map[string]bool{e1.Val.(string): true, e2.Val.(string): true}
		if !vals["T1"] || !vals["T2"] {
			t.Fatalf("iter %d: executors got %v and %v", iter, e1.Val, e2.Val)
		}
		waitCond(t, "map drained", func() bool { return g.flightCount() == 0 })
	}
}

func TestCancelWaiterDoesNotAffectOthers(t *testing.T) {
	g := NewGroup()
	entered := make(chan struct{})
	release := make(chan struct{})

	execRes := make(chan Result, 1)
	go func() {
		execRes <- g.Do(context.Background(), "k", blockedFn(entered, release, "v"))
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	w1 := make(chan Result, 1)
	w2 := make(chan Result, 1)
	go func() { w1 <- g.Do(ctx, "k", nil) }()
	go func() { w2 <- g.Do(context.Background(), "k", nil) }()
	waitCond(t, "2 waiters", func() bool { return g.waiterCount("k") == 2 })

	cancel()
	if r := <-w1; r.Err != context.Canceled {
		t.Fatalf("cancelled waiter got %+v, want context.Canceled", r)
	}
	waitCond(t, "one waiter left", func() bool { return g.waiterCount("k") == 1 })

	close(release)
	if r := <-w2; r.Val != "v" || !r.Shared {
		t.Fatalf("surviving waiter got %+v, want shared v", r)
	}
	if r := <-execRes; r.Val != "v" || r.Shared {
		t.Fatalf("executor got %+v, want own v", r)
	}
}

// 交叉迁移（t1→t2 与 t2→t1 并发）必须可串行化、无死锁。
func TestCrossPromoteNoDeadlock(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		g := NewGroup()
		entered := make(chan struct{}, 2)
		start := make(chan struct{})
		done := make(chan Result, 2)

		task := func(enteredFlag *struct{}, key, target, val string) func(context.Context, *Handle) (interface{}, error) {
			return func(ctx context.Context, h *Handle) (interface{}, error) {
				entered <- struct{}{}
				<-start
				h.Promote(target)
				return val, nil
			}
		}
		var s1, s2 struct{}
		go func() { done <- g.Do(context.Background(), "t1", task(&s1, "t1", "t2", "V1")) }()
		go func() { done <- g.Do(context.Background(), "t2", task(&s2, "t2", "t1", "V2")) }()
		<-entered
		<-entered
		close(start)

		for i := 0; i < 2; i++ {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("iter %d: cross promote deadlocked", iter)
			}
		}
		waitCond(t, "map drained", func() bool { return g.flightCount() == 0 })
	}
}
