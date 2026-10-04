package publisher

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func collect(t *testing.T, p *Publisher, want int) []Result {
	t.Helper()
	var got []Result
	timeout := time.After(5 * time.Second)
	for len(got) < want {
		select {
		case r, ok := <-p.Results():
			if !ok {
				t.Fatalf("results channel closed after %d results, want %d", len(got), want)
			}
			got = append(got, r)
		case <-timeout:
			t.Fatalf("timeout after %d results, want %d", len(got), want)
		}
	}
	return got
}

// 乱序完成，必须按提交顺序交付。
func TestOutOfOrderCompletionDeliveredInOrder(t *testing.T) {
	p := New(8)
	const n = 6
	gates := make([]chan struct{}, n)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	for i := 0; i < n; i++ {
		i := i
		seq, err := p.Submit(context.Background(), func(context.Context) (any, error) {
			<-gates[i]
			return i, nil
		})
		if err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
		if seq != uint64(i) {
			t.Fatalf("seq = %d, want %d", seq, i)
		}
	}
	// 逆序放行：完成顺序与提交顺序相反。
	for i := n - 1; i >= 0; i-- {
		close(gates[i])
		time.Sleep(5 * time.Millisecond)
	}
	got := collect(t, p, n)
	for i, r := range got {
		if r.Seq != uint64(i) || r.Value != i || r.Err != nil {
			t.Fatalf("result %d = %+v, want seq=%d value=%d", i, r, i, i)
		}
	}
	p.Close()
}

// 任务失败时交付错误占位，后续结果继续交付。
func TestFailureDeliversPlaceholderAndContinues(t *testing.T) {
	p := New(4)
	boom := errors.New("boom")
	for i := 0; i < 3; i++ {
		i := i
		if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
			if i == 1 {
				return nil, boom
			}
			return i, nil
		}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	got := collect(t, p, 3)
	if got[0].Err != nil || got[0].Value != 0 {
		t.Fatalf("result 0 = %+v", got[0])
	}
	if !errors.Is(got[1].Err, boom) {
		t.Fatalf("result 1 err = %v, want boom placeholder", got[1].Err)
	}
	if got[2].Err != nil || got[2].Value != 2 {
		t.Fatalf("result 2 = %+v, want delivery to continue after failure", got[2])
	}
	p.Close()
}

// 取消也占一个序号：交付取消占位，顺序不受影响。
func TestCancellationOccupiesSequence(t *testing.T) {
	p := New(4)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
		return "first", nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Submit(ctx, func(ctx context.Context) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
		return "third", nil
	}); err != nil {
		t.Fatal(err)
	}
	cancel()
	got := collect(t, p, 3)
	if got[0].Value != "first" {
		t.Fatalf("result 0 = %+v", got[0])
	}
	if !errors.Is(got[1].Err, context.Canceled) {
		t.Fatalf("result 1 err = %v, want context.Canceled placeholder", got[1].Err)
	}
	if got[2].Value != "third" {
		t.Fatalf("result 2 = %+v", got[2])
	}
	p.Close()
}

// 缓冲区满时阻塞新任务启动。
func TestFullBufferBlocksSubmit(t *testing.T) {
	p := New(2)
	release := make(chan struct{})
	blocking := func(context.Context) (any, error) {
		<-release
		return nil, nil
	}
	for i := 0; i < 2; i++ {
		if _, err := p.Submit(context.Background(), blocking); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Submit(ctx, blocking); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("submit on full buffer = %v, want DeadlineExceeded (blocked)", err)
	}
	// 释放一个任务后，新提交应能成功。
	close(release)
	waitFor(t, "slot to free", func() bool { return p.Buffered() == 0 })
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
		return "ok", nil
	}); err != nil {
		t.Fatalf("submit after drain: %v", err)
	}
	got := collect(t, p, 3)
	if got[2].Value != "ok" {
		t.Fatalf("result 2 = %+v", got[2])
	}
	p.Close()
}

// 最早任务长期阻塞时，乱序缓存的结果数有上界（< capacity），
// 且缓冲区满后新任务无法启动，内存不随提交尝试增长。
func TestMemoryBoundWithBlockedHead(t *testing.T) {
	const capacity = 8
	p := New(capacity)
	head := make(chan struct{})
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
		<-head // 最早任务长期阻塞
		return "head", nil
	}); err != nil {
		t.Fatal(err)
	}
	// 填满剩余槽位：这些任务立即完成，但无法交付，只能缓存。
	for i := 1; i < capacity; i++ {
		i := i
		if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
			return i, nil
		}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	waitFor(t, "buffered results", func() bool { return p.Buffered() == capacity-1 })

	// 缓冲区已满：后续提交被阻塞，缓存不会继续增长。
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := p.Submit(ctx, func(context.Context) (any, error) { return nil, nil })
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("submit beyond capacity = %v, want blocked", err)
		}
	}
	if got := p.Buffered(); got != capacity-1 {
		t.Fatalf("Buffered = %d, want %d (bounded)", got, capacity-1)
	}
	if got := p.MaxBuffered(); got > capacity {
		t.Fatalf("MaxBuffered = %d exceeds bound %d", got, capacity)
	}

	// 放行头部任务，全部结果按序交付。
	close(head)
	got := collect(t, p, capacity)
	for i, r := range got {
		if r.Seq != uint64(i) {
			t.Fatalf("result %d seq = %d, out of order", i, r.Seq)
		}
	}
	if got[0].Value != "head" {
		t.Fatalf("head result = %+v", got[0])
	}
	p.Close()
	if got := p.MaxBuffered(); got > capacity {
		t.Fatalf("MaxBuffered = %d exceeds bound %d", got, capacity)
	}
}

// 关闭后尾部结果不被遗忘：已提交任务全部按序交付后通道才关闭。
func TestCloseDeliversTailResults(t *testing.T) {
	p := New(16)
	const n = 10
	release := make(chan struct{})
	for i := 0; i < n; i++ {
		i := i
		if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
			<-release
			return fmt.Sprintf("task-%d", i), nil
		}); err != nil {
			t.Fatalf("submit %d: %v", i, err)
		}
	}
	p.Close() // 立即关闭：仍有任务在途、结果未交付
	if _, err := p.Submit(context.Background(), func(context.Context) (any, error) {
		return nil, nil
	}); !errors.Is(err, ErrClosed) {
		t.Fatalf("submit after close = %v, want ErrClosed", err)
	}
	close(release) // 放行所有在途任务
	var got []Result
	for r := range p.Results() { // 通道关闭即全部交付完毕
		got = append(got, r)
	}
	if len(got) != n {
		t.Fatalf("delivered %d results after close, want %d (tail forgotten)", len(got), n)
	}
	for i, r := range got {
		if r.Seq != uint64(i) || r.Value != fmt.Sprintf("task-%d", i) || r.Err != nil {
			t.Fatalf("result %d = %+v", i, r)
		}
	}
}

// 空发布器关闭后通道应立即关闭。
func TestCloseEmpty(t *testing.T) {
	p := New(2)
	p.Close()
	select {
	case _, ok := <-p.Results():
		if ok {
			t.Fatal("unexpected result on empty publisher")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("results channel not closed after Close")
	}
}
