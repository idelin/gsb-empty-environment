package publisher

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// 乱序完成的任务必须按提交顺序交付。
func TestOutOfOrderCompletionDeliveredInOrder(t *testing.T) {
	const n = 20
	p := New(8) // 容量小于任务数，Submit 自身也会被背压阻塞

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			i := i
			_, err := p.Submit(context.Background(), func(context.Context) (any, error) {
				time.Sleep(time.Duration(n-i) * time.Millisecond) // 先提交的最慢完成
				return i, nil
			})
			if err != nil {
				t.Errorf("submit %d: %v", i, err)
				return
			}
		}
		p.Close()
	}()

	for want := 0; want < n; want++ {
		r, ok := <-p.Results()
		if !ok {
			t.Fatalf("channel closed early at position %d", want)
		}
		if r.Seq != want || r.Value != want || r.Err != nil {
			t.Fatalf("position %d: got %+v", want, r)
		}
	}
	if _, ok := <-p.Results(); ok {
		t.Fatal("expected channel to close after all results")
	}
	<-done
}

// 任务失败时交付错误占位，后续结果照常交付。
func TestFailurePlaceholder(t *testing.T) {
	// 容量需容纳全部 5 个任务：本测试先提交后消费，交付前结果都占槽位。
	p := New(5)
	boom := errors.New("boom")

	for i := 0; i < 5; i++ {
		i := i
		if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
			if i == 2 {
				return nil, boom
			}
			return i, nil
		}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	p.Close()

	var got []Result
	for r := range p.Results() {
		got = append(got, r)
	}
	if len(got) != 5 {
		t.Fatalf("got %d results, want 5", len(got))
	}
	for i, r := range got {
		if r.Seq != i {
			t.Fatalf("out of order at position %d: %+v", i, r)
		}
		if i == 2 {
			if !errors.Is(r.Err, boom) {
				t.Fatalf("slot 2: want boom placeholder, got %+v", r)
			}
		} else if r.Err != nil || r.Value != i {
			t.Fatalf("slot %d: unexpected %+v", i, r)
		}
	}
}

// 取消也占一个序号：交付 context.Canceled 占位，流继续。
func TestCancellationOccupiesSlot(t *testing.T) {
	p := New(4)
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	defer close(release) // 测试结束时放走被遗弃的任务体，避免 goroutine 泄漏

	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) { return "a", nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Submit(ctx, func(context.Context) (any, error) {
		<-release // 任务体永不主动完成，只能靠 ctx 取消脱身
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) { return "c", nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) { return "d", nil }); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond) // 确保 1 号任务已启动
	cancel()
	p.Close()

	type want struct {
		val    any
		cancel bool
	}
	wants := []want{{"a", false}, {nil, true}, {"c", false}, {"d", false}}
	for i, w := range wants {
		r, ok := <-p.Results()
		if !ok {
			t.Fatalf("channel closed early at slot %d", i)
		}
		if r.Seq != i {
			t.Fatalf("slot %d: got seq %d", i, r.Seq)
		}
		if w.cancel {
			if !errors.Is(r.Err, context.Canceled) {
				t.Fatalf("slot %d: want context.Canceled placeholder, got %+v", i, r)
			}
		} else if r.Err != nil || r.Value != w.val {
			t.Fatalf("slot %d: got %+v", i, r)
		}
	}
	if _, ok := <-p.Results(); ok {
		t.Fatal("expected channel to close after all results")
	}
}

// panic 也转换为错误占位，不中断后续交付。
func TestPanicPlaceholder(t *testing.T) {
	p := New(2)
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) { panic("kaboom") }); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) { return "ok", nil }); err != nil {
		t.Fatal(err)
	}
	p.Close()

	first := <-p.Results()
	if first.Seq != 0 || first.Err == nil || first.Value != nil {
		t.Fatalf("slot 0: want panic placeholder, got %+v", first)
	}
	second := <-p.Results()
	if second.Seq != 1 || second.Err != nil || second.Value != "ok" {
		t.Fatalf("slot 1: want ok, got %+v", second)
	}
}

// 缓冲区满时新的提交必须阻塞，直到有结果交付释放槽位。
func TestBufferFullBlocksSubmit(t *testing.T) {
	p := New(2)
	gate := make(chan struct{})

	// 占满两个槽位：0 号阻塞，1 号快速完成但因队头阻塞无法交付。
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
		<-gate
		return 0, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
		return 1, nil
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Pending() == 1 }, "task 1 to finish and pend")

	// 缓冲已满：第三个提交必须阻塞。
	third := make(chan int, 1)
	go func() {
		seq, err := p.Submit(context.Background(), func(context.Context) (any, error) { return 2, nil })
		if err != nil {
			t.Errorf("third submit: %v", err)
		}
		third <- seq
	}()
	select {
	case <-third:
		t.Fatal("submit should block while buffer is full")
	case <-time.After(150 * time.Millisecond):
	}

	// 放行队头 -> 交付 -> 释放槽位 -> 第三个提交得以启动。
	close(gate)
	for want := 0; want < 3; want++ {
		r, ok := <-p.Results()
		if !ok {
			t.Fatalf("channel closed early at position %d", want)
		}
		if r.Seq != want || r.Value != want {
			t.Fatalf("position %d: got %+v", want, r)
		}
	}
	if seq := <-third; seq != 2 {
		t.Fatalf("third seq = %d, want 2", seq)
	}
	p.Close()
	if _, ok := <-p.Results(); ok {
		t.Fatal("expected channel to close after all results")
	}
}

// 最早的任务长期阻塞时，内存占用必须有界：
// 待交付结果不超过容量，额外提交被背压拒绝，堆增长与容量成正比而非与提交尝试数成正比。
func TestMemoryBoundWhileHeadBlocked(t *testing.T) {
	const (
		capacity = 32
		blobSize = 1 << 20 // 1 MiB
	)
	p := New(capacity)

	gate := make(chan struct{})
	headStarted := make(chan struct{})
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
		close(headStarted)
		<-gate // 最早的任务长期阻塞
		return make([]byte, blobSize), nil
	}); err != nil {
		t.Fatal(err)
	}
	<-headStarted

	// 其余 capacity-1 个任务各自完成并携带 1 MiB 结果。
	finished := make(chan struct{}, capacity-1)
	for i := 1; i < capacity; i++ {
		i := i
		if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
			blob := make([]byte, blobSize)
			blob[0] = byte(i)
			finished <- struct{}{}
			return blob, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < capacity; i++ {
		<-finished
	}
	waitFor(t, func() bool { return p.Pending() == capacity-1 }, "all tail results to pend")

	// 队头阻塞期间，额外提交一律因缓冲满而失败：不启动、不占内存。
	for i := 0; i < 4*capacity; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, err := p.Submit(ctx, func(context.Context) (any, error) {
			return make([]byte, blobSize), nil
		})
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("extra submit %d: got %v, want DeadlineExceeded", i, err)
		}
	}

	// 内存上界：整个堆（含 31 个 1 MiB 待交付结果）远低于 3 倍容量上限；
	// 若缓冲无界，仅 128 次额外提交就会再涨 128 MiB。
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	if limit := uint64(3 * capacity * blobSize); m.HeapAlloc > limit {
		t.Fatalf("HeapAlloc = %d MiB, exceeds bound %d MiB", m.HeapAlloc>>20, limit>>20)
	}
	if got := p.Pending(); got != capacity-1 {
		t.Fatalf("pending = %d, want %d", got, capacity-1)
	}

	// 放行队头：所有结果按序交付，一个不少、一字节不丢。
	close(gate)
	p.Close()
	count := 0
	total := 0
	for r := range p.Results() {
		if r.Seq != count {
			t.Fatalf("out of order: got seq %d at position %d", r.Seq, count)
		}
		if r.Err != nil {
			t.Fatalf("seq %d: unexpected error %v", r.Seq, r.Err)
		}
		total += len(r.Value.([]byte))
		count++
	}
	if count != capacity {
		t.Fatalf("delivered %d results, want %d", count, capacity)
	}
	if total != capacity*blobSize {
		t.Fatalf("total payload = %d bytes, want %d", total, capacity*blobSize)
	}
}

// Close 之后尾部结果不被遗忘：已提交任务全部按序交付完毕通道才关闭。
func TestCloseDoesNotForgetTail(t *testing.T) {
	const n = 16
	p := New(n)
	for i := 0; i < n; i++ {
		i := i
		if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
			time.Sleep(time.Duration(i) * 10 * time.Millisecond) // 尾部结果最后完成
			return i, nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 立即关闭：不再接受新任务，但已提交的尾部结果必须全部交付。
	p.Close()
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) { return nil, nil }); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after close: got %v, want ErrClosed", err)
	}

	count := 0
	for r := range p.Results() {
		if r.Seq != count || r.Value != count || r.Err != nil {
			t.Fatalf("position %d: got %+v", count, r)
		}
		count++
	}
	if count != n {
		t.Fatalf("delivered %d results after close, want %d", count, n)
	}
}
