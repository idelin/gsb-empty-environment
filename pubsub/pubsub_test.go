package pubsub

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func drain[T any](sub *Subscription[T]) []T {
	var got []T
	for v := range sub.C() {
		got = append(got, v)
	}
	return got
}

// 基本扇出：每个当前订阅者都收到消息。
func TestFanout(t *testing.T) {
	b := New[int]()
	s1, _ := b.Subscribe()
	s2, _ := b.Subscribe()

	res, err := b.Publish(context.Background(), 42)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if res.Delivered != 2 || res.Dropped != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := drain(s1); len(got) != 1 || got[0] != 42 {
		t.Fatalf("s1 got %v", got)
	}
	if got := drain(s2); len(got) != 1 || got[0] != 42 {
		t.Fatalf("s2 got %v", got)
	}
}

// 单订阅者顺序与队列容量正确。
func TestPerSubscriberOrdering(t *testing.T) {
	b := New[int]()
	sub, _ := b.Subscribe(WithCapacity[int](4))
	for i := 1; i <= 4; i++ {
		if _, err := b.Publish(context.Background(), i); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}
	_ = b.Close(context.Background())
	got := drain(sub)
	if len(got) != 4 {
		t.Fatalf("got %v", got)
	}
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("order broken at %d: %v", i, got)
		}
	}
}

// PolicyBlock：慢订阅者只延迟 Publish 的返回；其他订阅者在此期间
// 已并行拿到消息，绝不无声丢消息。
func TestBlockSlowDoesNotDropOthers(t *testing.T) {
	b := New[int]()
	slow, _ := b.Subscribe(WithCapacity[int](1)) // 无消费者
	fast, _ := b.Subscribe(WithCapacity[int](8))

	// 填满慢者唯一槽位。
	if _, err := b.Publish(context.Background(), 7); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_, _ = b.Publish(context.Background(), 8) // 慢者满，阻塞
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Publish should block on the slow subscriber")
	case <-time.After(50 * time.Millisecond):
	}

	// 慢者卡住期间，fast 独立 broker 的消息立即可达；
	// 且本 broker 中 fast 已收到 7、8（8 的入队在慢者阻塞前已并行完成）。
	other := New[int]()
	only, _ := other.Subscribe(WithCapacity[int](1))
	if r, err := other.Publish(context.Background(), 100); err != nil || r.Delivered != 1 {
		t.Fatalf("independent subscriber starved: %+v %v", r, err)
	}
	if v := <-only.C(); v != 100 {
		t.Fatalf("unexpected %d", v)
	}
	_ = other.Close(context.Background())

	// 慢者开始消费，解除背压，消息 7、8 均交付，无丢失。
	slowN := make(chan int, 1)
	go func() {
		n := 0
		for range slow.C() {
			n++
		}
		slowN <- n
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked Publish never returned")
	}

	_ = b.Close(context.Background())
	if n := <-slowN; n != 2 {
		t.Fatalf("slow expected 2 messages, got %d", n)
	}
	if got := drain(fast); len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Fatalf("fast expected [7 8], got %v", got)
	}
}

// 阻塞发布可用 ctx 取消，不影响其他订阅者已入队的消息。
func TestBlockPublishContextCancel(t *testing.T) {
	b := New[int]()
	slow, _ := b.Subscribe(WithCapacity[int](1))
	fast, _ := b.Subscribe(WithCapacity[int](4))

	// 填满慢者唯一槽位。
	if _, err := b.Publish(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pubDone := make(chan error, 1)
	go func() {
		_, err := b.Publish(ctx, 2)
		pubDone <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-pubDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled publish never returned")
	}

	go func() {
		for range slow.C() {
		}
	}()
	_ = b.Close(context.Background())
	got := drain(fast)
	if len(got) < 1 || got[0] != 1 {
		t.Fatalf("fast should keep promised message 1, got %v", got)
	}
	// 消息 2 若已并行入队则必须交付，不能丢失；未入队则不在此列。
	for _, v := range got {
		if v != 1 && v != 2 {
			t.Fatalf("unexpected message %d", v)
		}
	}
}

// PolicyDropNewest：队列满时显式丢弃，可统计、可回调，且永不阻塞。
func TestDropNewestExplicit(t *testing.T) {
	b := New[int]()
	var dropped []int
	var dmu sync.Mutex
	sub, _ := b.Subscribe(
		WithCapacity[int](2),
		WithPolicy[int](PolicyDropNewest),
		WithOnDrop[int](func(msg int) {
			dmu.Lock()
			dropped = append(dropped, msg)
			dmu.Unlock()
		}),
	)

	for i := 1; i <= 5; i++ {
		res, err := b.Publish(context.Background(), i)
		if err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
		if i <= 2 && res.Dropped != 0 {
			t.Fatalf("msg %d should be delivered", i)
		}
		if i > 2 && res.Dropped != 1 {
			t.Fatalf("msg %d should be dropped, got %+v", i, res)
		}
	}
	if sub.Dropped() != 3 {
		t.Fatalf("Dropped counter = %d", sub.Dropped())
	}
	_ = b.Close(context.Background())
	got := drain(sub)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("kept messages = %v", got)
	}
	dmu.Lock()
	if len(dropped) != 3 || dropped[0] != 3 || dropped[2] != 5 {
		t.Fatalf("dropped callback = %v", dropped)
	}
	dmu.Unlock()
}

// 重复取消订阅不 panic；channel 只关闭一次。
func TestUnsubscribeIdempotent(t *testing.T) {
	b := New[int]()
	sub, _ := b.Subscribe()
	for i := 0; i < 100; i++ {
		sub.Unsubscribe()
	}
	_ = b.Close(context.Background())
	// channel 关闭后排空安全。
	for range sub.C() {
	}
}

// 取消订阅与发布并发安全，且已入队消息被排空交付。
func TestConcurrentUnsubscribeAndPublish(t *testing.T) {
	b := New[int]()
	var wg sync.WaitGroup
	subs := make([]*Subscription[int], 0, 8)
	for i := 0; i < 8; i++ {
		sub, err := b.Subscribe(WithCapacity[int](128))
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, sub)
	}

	stop := make(chan struct{})
	var published atomic.Int64
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = b.Publish(context.Background(), int(published.Add(1)))
				}
			}
		}()
	}
	for _, sub := range subs {
		wg.Add(1)
		go func(sub *Subscription[int]) {
			defer wg.Done()
			timer := time.NewTimer(20 * time.Millisecond)
			defer timer.Stop()
			<-timer.C
			sub.Unsubscribe()
		}(sub)
	}
	time.Sleep(40 * time.Millisecond)
	close(stop)

	// 排空已承诺消息；Close 保证在途发布完成入队后再关闭 channel。
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, sub := range subs {
		for range sub.C() {
			total++
		}
	}
	wg.Wait()
	t.Logf("published=%d, delivered after unsubscribe=%d", published.Load(), total)
}

// Close 后 Publish/Subscribe 返回 ErrClosed；重复 Close 不 panic。
func TestCloseRejectsNewWork(t *testing.T) {
	b := New[int]()
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := b.Close(context.Background()); err != nil {
			t.Fatalf("repeated Close: %v", err)
		}
	}
	if _, err := b.Publish(context.Background(), 1); !errors.Is(err, ErrClosed) {
		t.Fatalf("Publish after close err = %v", err)
	}
	if _, err := b.Subscribe(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Subscribe after close err = %v", err)
	}
}

// Close 保证关闭瞬间在途的消息被排空交付。
func TestCloseDrainsPromised(t *testing.T) {
	b := New[int]()
	sub, _ := b.Subscribe(WithCapacity[int](16))

	const n = 16
	committed := make(chan struct{}, n)
	b.committed = func() { committed <- struct{}{} }
	var pwg sync.WaitGroup
	for i := 0; i < n; i++ {
		pwg.Add(1)
		go func(i int) {
			defer pwg.Done()
			_, _ = b.Publish(context.Background(), i)
		}(i)
	}
	// 确保所有发布都已登记为在途（已“承诺”），但不等待其入队完成。
	for i := 0; i < n; i++ {
		<-committed
	}
	// Close 必须等待这些在途发布完成入队，并最终排空。
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	pwg.Wait()

	got := drain(sub)
	if len(got) != n {
		t.Fatalf("promised messages lost: got %d, want %d", len(got), n)
	}
	seen := make(map[int]bool, n)
	for _, v := range got {
		if seen[v] {
			t.Fatalf("duplicate delivery: %d", v)
		}
		seen[v] = true
	}
}

// 任务函数与失败注入：重试 + 假时钟退避，用尽后调用失败回调。
func TestHandlerFailureRetryWithInjectedClock(t *testing.T) {
	clk := newFakeClock()
	b := New[int](WithClock(clk))

	var attempts atomic.Int32
	var failedMsg atomic.Int64
	var failedErr atomic.Value

	sub, err := b.Subscribe(WithHandler(
		func(_ context.Context, msg int) error {
			n := attempts.Add(1)
			if n < 3 {
				return errors.New("boom")
			}
			return nil
		},
		WithMaxAttempts[int](3),
		WithBackoff[int](func(attempt int) time.Duration {
			return time.Duration(attempt) * 10 * time.Millisecond
		}),
		WithOnFailed[int](func(msg int, err error) {
			failedMsg.Store(int64(msg))
			failedErr.Store(err)
		}),
	))
	if err != nil {
		t.Fatal(err)
	}

	_, _ = b.Publish(context.Background(), 99)

	// 轮询推进假时钟，避免“退避定时器尚未创建就推进”的时序竞争。
	advanceUntil := func(want int32, what string) {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if attempts.Load() >= want {
				return
			}
			clk.Advance(time.Millisecond)
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("want %s, got %d attempts", what, attempts.Load())
	}
	advanceUntil(1, "1 attempt")
	advanceUntil(2, "2 attempts")
	advanceUntil(3, "success on 3rd attempt")
	_ = b.Close(context.Background())
	drain(sub)
	if failedMsg.Load() != 0 {
		t.Fatalf("message should succeed, but failed: %d", failedMsg.Load())
	}

	// 用尽重试 -> onFailed 被调用。
	clk2 := newFakeClock()
	b2 := New[int](WithClock(clk2))
	var attempts2 atomic.Int32
	var failed2 atomic.Bool
	sub2, _ := b2.Subscribe(WithHandler(
		func(context.Context, int) error {
			attempts2.Add(1)
			return errors.New("permanent")
		},
		WithMaxAttempts[int](2),
		WithBackoff[int](func(int) time.Duration { return time.Millisecond }),
		WithOnFailed[int](func(int, error) { failed2.Store(true) }),
	))
	_, _ = b2.Publish(context.Background(), 1)
	for attempts2.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	clk2.Advance(time.Millisecond)
	done := make(chan struct{})
	go func() { _ = b2.Close(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung")
	}
	drain(sub2)
	if attempts2.Load() != 2 || !failed2.Load() {
		t.Fatalf("attempts=%d failed=%v", attempts2.Load(), failed2.Load())
	}
}

// 高并发压力：与竞态检测配合，验证无数据竞争、无向已关闭 channel 发送。
func TestStressNoSendOnClosedChannel(t *testing.T) {
	b := New[int]()
	const subN, pubN = 6, 200
	var wg sync.WaitGroup
	for i := 0; i < subN; i++ {
		sub, _ := b.Subscribe(
			WithCapacity[int](8),
			WithPolicy[int](PolicyDropNewest),
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range sub.C() {
			}
		}()
	}
	for i := 0; i < pubN; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = b.Publish(context.Background(), i)
		}(i)
	}
	if err := b.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
}
