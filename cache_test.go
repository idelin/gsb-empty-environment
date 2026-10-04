package shardcache

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestCache(maxCost int64, loader Loader) *Cache {
	return New(Options{
		Shards:  4,
		MaxCost: maxCost,
		Cost:    func(_ string, v []byte) int64 { return int64(len(v)) },
		Reserve: func(string) int64 { return 8 },
		Loader:  loader,
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// 超大值：Set 直接拒绝；加载结算时超额则释放预留且不存储。
func TestOversizedValue(t *testing.T) {
	c := newTestCache(64, func(context.Context, string) ([]byte, error) {
		return make([]byte, 128), nil
	})
	if err := c.Set("big", make([]byte, 128)); !errors.Is(err, ErrCostTooLarge) {
		t.Fatalf("Set oversized: got %v", err)
	}
	if _, err := c.GetOrLoad(context.Background(), "k"); !errors.Is(err, ErrCostTooLarge) {
		t.Fatalf("GetOrLoad oversized: got %v", err)
	}
	st := c.Stats()
	if st.Reserved != 0 || st.Resident != 0 {
		t.Fatalf("reservation not released: %+v", st)
	}
	if st.Loads != 1 || st.Misses != 1 {
		t.Fatalf("stats: %+v", st)
	}
}

// 屏障控制加载完成次序：加载被屏障挡住时 Set 覆盖同键，
// 旧加载完成后不得覆盖新值。
func TestSameKeyOverwriteVsLoad(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	c := newTestCache(64, func(context.Context, string) ([]byte, error) {
		once.Do(func() { close(started) })
		<-release
		return []byte("old-value"), nil
	})
	type result struct {
		val []byte
		err error
	}
	rc := make(chan result, 1)
	go func() {
		v, err := c.GetOrLoad(context.Background(), "k")
		rc <- result{v, err}
	}()
	<-started // 加载已在进行中（预留已占用）
	if err := c.Set("k", []byte("new-value")); err != nil {
		t.Fatal(err)
	}
	close(release) // 屏障放行，旧加载结算
	r := <-rc
	if r.err != nil {
		t.Fatalf("waiter: %v", r.err)
	}
	got, ok := c.Get("k")
	if !ok || string(got) != "new-value" {
		t.Fatalf("stale load overwrote new value: ok=%v val=%q", ok, got)
	}
	st := c.Stats()
	if st.Reserved != 0 || st.Resident != int64(len("new-value")) {
		t.Fatalf("accounting: %+v", st)
	}
}

// 屏障控制两个不同键的加载按相反次序完成，二者都应正确结算。
func TestBarrierOrderedCompletion(t *testing.T) {
	gateA, gateB := make(chan struct{}), make(chan struct{})
	c := newTestCache(64, func(_ context.Context, key string) ([]byte, error) {
		switch key {
		case "a":
			<-gateA
			return []byte("va"), nil
		default:
			<-gateB
			return []byte("vb"), nil
		}
	})
	done := make(chan error, 2)
	go func() { _, err := c.GetOrLoad(context.Background(), "a"); done <- err }()
	go func() { _, err := c.GetOrLoad(context.Background(), "b"); done <- err }()
	waitFor(t, "both reserved", func() bool { return c.Stats().Reserved == 16 })
	close(gateB) // b 先完成
	waitFor(t, "b settled", func() bool { return c.Stats().Reserved == 8 })
	close(gateA) // a 后完成
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st := c.Stats()
	if st.Reserved != 0 || st.Resident != 4 || st.Loads != 2 {
		t.Fatalf("stats: %+v", st)
	}
	if v, ok := c.Get("a"); !ok || string(v) != "va" {
		t.Fatalf("a: %q ok=%v", v, ok)
	}
	if v, ok := c.Get("b"); !ok || string(v) != "vb" {
		t.Fatalf("b: %q ok=%v", v, ok)
	}
}

// 所有等待者取消：底层加载被取消，预留释放。
func TestAllWaitersCancel(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	var sawCancel atomic.Bool
	c := newTestCache(64, func(ctx context.Context, _ string) ([]byte, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		sawCancel.Store(true)
		return nil, ctx.Err()
	})
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	errs := make(chan error, 2)
	go func() { _, err := c.GetOrLoad(ctx1, "k"); errs <- err }()
	go func() { _, err := c.GetOrLoad(ctx2, "k"); errs <- err }()
	<-started
	time.Sleep(20 * time.Millisecond) // 让第二个等待者完成合并登记
	cancel1()
	cancel2()
	for i := 0; i < 2; i++ {
		if err := <-errs; !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter %d: got %v", i, err)
		}
	}
	waitFor(t, "reservation released", func() bool { return c.Stats().Reserved == 0 })
	if !sawCancel.Load() {
		t.Fatal("underlying load was not canceled")
	}
	st := c.Stats()
	if st.Cancels != 2 || st.Loads != 1 || st.Resident != 0 {
		t.Fatalf("stats: %+v", st)
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("canceled load must not publish")
	}
}

// 某等待者取消不影响其他等待者。
func TestOneWaiterCancelOthersSurvive(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	c := newTestCache(64, func(_ context.Context, _ string) ([]byte, error) {
		once.Do(func() { close(started) })
		<-release
		return []byte("v"), nil
	})
	ctx1, cancel1 := context.WithCancel(context.Background())
	errs := make(chan error, 2)
	vals := make(chan []byte, 1)
	go func() { _, err := c.GetOrLoad(ctx1, "k"); errs <- err }()
	go func() {
		v, err := c.GetOrLoad(context.Background(), "k")
		if err != nil {
			errs <- err
			return
		}
		vals <- v
	}()
	<-started
	time.Sleep(20 * time.Millisecond)
	cancel1()
	if err := <-errs; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter: %v", err)
	}
	close(release)
	if v := <-vals; string(v) != "v" {
		t.Fatalf("surviving waiter got %q", v)
	}
	st := c.Stats()
	if st.Cancels != 1 || st.Reserved != 0 || st.Resident != 1 {
		t.Fatalf("stats: %+v", st)
	}
}

// 同键同版本并发 miss 合并为一次加载。
func TestMissCoalescing(t *testing.T) {
	var calls atomic.Int64
	c := newTestCache(64, func(_ context.Context, _ string) ([]byte, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return []byte("x"), nil
	})
	const n = 32
	var wg sync.WaitGroup
	results := make([][]byte, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := c.GetOrLoad(context.Background(), "k")
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = v
		}(i)
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loader called %d times", calls.Load())
	}
	for i, v := range results {
		if string(v) != "x" {
			t.Fatalf("waiter %d got %q", i, v)
		}
	}
	st := c.Stats()
	if st.Loads != 1 || st.Misses != n || st.Resident != 1 || st.Reserved != 0 {
		t.Fatalf("stats: %+v", st)
	}
}

// 加载失败释放预留。
func TestLoadErrorReleasesReservation(t *testing.T) {
	want := errors.New("boom")
	c := newTestCache(64, func(context.Context, string) ([]byte, error) {
		return nil, want
	})
	if _, err := c.GetOrLoad(context.Background(), "k"); !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
	st := c.Stats()
	if st.Reserved != 0 || st.Resident != 0 || st.Loads != 1 {
		t.Fatalf("stats: %+v", st)
	}
}

// 固定值与加载预留共同受全局额度约束。
func TestReservationSharesGlobalBudget(t *testing.T) {
	c := newTestCache(100, func(context.Context, string) ([]byte, error) {
		return []byte("v"), nil
	})
	if err := c.Set("fixed", make([]byte, 95)); err != nil {
		t.Fatal(err)
	}
	// 预留 8：95+8 > 100，加载无法开始。
	if _, err := c.GetOrLoad(context.Background(), "k"); !errors.Is(err, ErrInsufficientBudget) {
		t.Fatalf("got %v", err)
	}
	st := c.Stats()
	if st.Loads != 0 || st.Reserved != 0 || st.Resident != 95 {
		t.Fatalf("stats: %+v", st)
	}
}

// 统计读取与淘汰并发：快照必须始终满足不变量。
func TestStatsConcurrentWithEviction(t *testing.T) {
	c := newTestCache(256, nil)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("g%d-k%d", g, rng.Intn(64))
				if rng.Intn(4) == 0 {
					c.Delete(key)
					continue
				}
				if err := c.Set(key, make([]byte, 1+rng.Intn(40))); err != nil {
					t.Errorf("Set: %v", err)
					return
				}
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				st := c.Stats()
				if st.Resident < 0 || st.Reserved < 0 || st.Resident+st.Reserved > 256 {
					t.Errorf("invariant violated: %+v", st)
					return
				}
			}
		}()
	}
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
	st := c.Stats()
	if st.Evicts == 0 {
		t.Fatal("expected evictions under pressure")
	}
	if st.Resident+st.Reserved > 256 {
		t.Fatalf("final invariant: %+v", st)
	}
}

// 独立账本复算占用：模型中仍存在的键的成本之和必须等于 Resident。
func TestIndependentLedger(t *testing.T) {
	c := newTestCache(512, nil)
	model := map[string]int64{}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 3000; i++ {
		key := fmt.Sprintf("key-%d", rng.Intn(50))
		switch rng.Intn(3) {
		case 0, 1:
			size := int64(1 + rng.Intn(30))
			if err := c.Set(key, make([]byte, size)); err == nil {
				model[key] = size
			}
		case 2:
			c.Delete(key)
			delete(model, key)
		}
	}
	var expect int64
	for k, sz := range model {
		if _, ok := c.Get(k); ok {
			expect += sz
		}
	}
	if st := c.Stats(); st.Resident != expect {
		t.Fatalf("ledger=%d resident=%d", expect, st.Resident)
	}
}

// 返回值与存储值均为不可变副本。
func TestImmutableCopies(t *testing.T) {
	c := newTestCache(64, func(context.Context, string) ([]byte, error) {
		return []byte("loaded"), nil
	})
	in := []byte("hello")
	if err := c.Set("k", in); err != nil {
		t.Fatal(err)
	}
	in[0] = 'X' // 修改入参不影响已存储值
	got, ok := c.Get("k")
	if !ok || string(got) != "hello" {
		t.Fatalf("stored value changed with input: %q", got)
	}
	got[0] = 'Y' // 修改返回值不影响缓存
	again, _ := c.Get("k")
	if string(again) != "hello" {
		t.Fatalf("returned copy aliases stored value: %q", again)
	}
	v, err := c.GetOrLoad(context.Background(), "l")
	if err != nil {
		t.Fatal(err)
	}
	v[0] = 'Z'
	stored, _ := c.Get("l")
	if string(stored) != "loaded" {
		t.Fatalf("load result aliases stored value: %q", stored)
	}
}
