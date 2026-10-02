package suspense

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// 模拟异步数据源：按参数决定延迟，返回 "result:<param>"。
// 参数 "A" 慢（120ms），参数 "B" 快（10ms），用于制造竞争。
func fakeFetch(ctx context.Context, p string) (string, error) {
	delay := 10 * time.Millisecond
	if p == "A" {
		delay = 120 * time.Millisecond
	}
	select {
	case <-time.After(delay):
		return "result:" + p, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// TestPendingThenSuccess 验证状态机 Pending -> Success 的基本转换。
func TestPendingThenSuccess(t *testing.T) {
	params := NewSignal("B")
	res := NewResource(fakeFetch)
	res.Track(params)

	if got := res.Snapshot().State; got != StatePending {
		t.Fatalf("发起请求后应立即处于 Pending，实际 %v", got)
	}

	snap, err := res.Await(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != StateSuccess || snap.Value != "result:B" {
		t.Fatalf("期望 Success(result:B)，实际 %v(%q)", snap.State, snap.Value)
	}
}

// TestErrorState 验证错误被追踪并传播为 Error 状态。
func TestErrorState(t *testing.T) {
	boom := errors.New("boom")
	res := NewResource(func(ctx context.Context, p string) (string, error) {
		return "", boom
	})
	res.Track(NewSignal("x"))

	snap, err := res.Await(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != StateError || !errors.Is(snap.Err, boom) {
		t.Fatalf("期望 Error(boom)，实际 %v(%v)", snap.State, snap.Err)
	}
}

// TestPendingBeaconPropagates 验证下游在数据就绪前收到 Pending 信标，
// 且参数变化时再次收到 Pending。
func TestPendingBeaconPropagates(t *testing.T) {
	params := NewSignal("B")
	res := NewResource(fakeFetch)

	var mu sync.Mutex
	var states []State
	res.Subscribe(func(s Snapshot[string]) {
		mu.Lock()
		states = append(states, s.State)
		mu.Unlock()
	})

	res.Track(params)
	if _, err := res.Await(context.Background()); err != nil {
		t.Fatal(err)
	}
	params.Set("B") // 相同值也触发重新加载
	if _, err := res.Await(context.Background()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []State{StatePending, StateSuccess, StatePending, StateSuccess}
	if len(states) != len(want) {
		t.Fatalf("期望状态序列 %v，实际 %v", want, states)
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("期望状态序列 %v，实际 %v", want, states)
		}
	}
}

// TestRaceDiscardsStalePromise 验收边界：
// 快速连续修改查询参数 A 和 B，发起两次异步获取；
// 第二次请求发出后，第一次的旧 Promise 即使迟到返回也必须被作废；
// 下游最终稳定呈现第二次请求的结果。
func TestRaceDiscardsStalePromise(t *testing.T) {
	params := NewSignal("A")
	res := NewResource(fakeFetch)

	var mu sync.Mutex
	var log []Snapshot[string]
	res.Subscribe(func(s Snapshot[string]) {
		mu.Lock()
		log = append(log, s)
		mu.Unlock()
	})

	res.Track(params)                 // 请求 1：A（慢，120ms）
	time.Sleep(20 * time.Millisecond) // 确保请求 1 已在飞
	params.Set("B")                   // 请求 2：B（快，10ms），应立即作废请求 1

	// 下游应快速稳定到 B 的结果，而不是等待慢请求 A。
	snap, err := res.Await(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != StateSuccess || snap.Value != "result:B" {
		t.Fatalf("最终应稳定为 result:B，实际 %v(%q)", snap.State, snap.Value)
	}

	// 等待旧 Promise（A）到期返回，验证其结果被作废、不污染下游。
	time.Sleep(200 * time.Millisecond)

	final := res.Snapshot()
	if final.State != StateSuccess || final.Value != "result:B" {
		t.Fatalf("旧 Promise 返回后下游被污染：%v(%q)", final.State, final.Value)
	}
	if d := res.Discarded(); d != 1 {
		t.Fatalf("期望作废 1 个迟到结果，实际 %d", d)
	}

	// 下游传播序列中不得出现 A 的 Success。
	mu.Lock()
	defer mu.Unlock()
	for _, s := range log {
		if s.State == StateSuccess && s.Value == "result:A" {
			t.Fatalf("下游收到了已作废的旧结果 result:A，传播日志：%v", log)
		}
	}
	// 传播序列应以 Pending 信标开始，且每次请求都有 Pending。
	if len(log) < 3 || log[0].State != StatePending {
		t.Fatalf("传播日志缺少 Pending 信标：%v", log)
	}
}

// TestOldPromiseResolvingLastIsDiscarded 强化竞争场景：
// 让旧请求在新请求成功之后才返回，验证序号检查兜底作废。
func TestOldPromiseResolvingLastIsDiscarded(t *testing.T) {
	release := make(map[string]chan struct{})
	for _, k := range []string{"A", "B"} {
		release[k] = make(chan struct{})
	}
	res := NewResource(func(ctx context.Context, p string) (string, error) {
		select {
		case <-release[p]:
			return "result:" + p, nil
		case <-ctx.Done():
			// 即使 ctx 被取消，也模拟一个"忽略取消、迟到返回"的旧 Promise：
			<-release[p]
			return "result:" + p, nil
		}
	})
	params := NewSignal("A")
	res.Track(params) // 请求 1：A
	params.Set("B")   // 请求 2：B（请求 1 的 ctx 被取消，但它仍会在 release 后迟到返回）

	close(release["B"]) // B 先完成
	snap, err := res.Await(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Value != "result:B" {
		t.Fatalf("期望 result:B，实际 %q", snap.Value)
	}

	close(release["A"]) // 旧 Promise A 最后才返回
	time.Sleep(50 * time.Millisecond)

	final := res.Snapshot()
	if final.Value != "result:B" {
		t.Fatalf("迟到的旧 Promise 污染了下游：%q", final.Value)
	}
	if d := res.Discarded(); d != 1 {
		t.Fatalf("期望作废 1 个迟到结果，实际 %d", d)
	}
}

// TestAwaitContextCancel 验证 Await 尊重调用方 ctx。
func TestAwaitContextCancel(t *testing.T) {
	res := NewResource(func(ctx context.Context, p string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Hour):
			return "never", nil
		}
	})
	res.Track(NewSignal("x"))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := res.Await(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("期望 DeadlineExceeded，实际 %v", err)
	}
}
