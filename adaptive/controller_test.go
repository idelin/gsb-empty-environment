package adaptive

import (
	"math"
	"sync"
	"testing"
	"time"
)

// testClock 可手动推进的时钟，保证测试确定性。
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestController(t *testing.T, cfg Config) (*Controller, *testClock) {
	t.Helper()
	c, err := NewController(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clk := newTestClock()
	c.now = clk.now
	return c, clk
}

func healthy(inFlight int) Metrics {
	return Metrics{InFlight: inFlight, CPU: 0.3, LatencyP99: 0.05, ErrorRate: 0}
}

func overloaded(inFlight int) Metrics {
	return Metrics{InFlight: inFlight, CPU: 0.99, LatencyP99: 2.0, ErrorRate: 0.1}
}

func TestConfigValidation(t *testing.T) {
	good := DefaultConfig()
	if _, err := NewController(good); err != nil {
		t.Fatalf("default config rejected: %v", err)
	}
	bad := []Config{
		{Min: -1, Max: 10, TargetCPU: .5, LatencyGoal: 1, DecreaseFactor: .5, IncreaseRatio: .1, Smoothing: .5},
		{Min: 10, Max: 10, TargetCPU: .5, LatencyGoal: 1, DecreaseFactor: .5, IncreaseRatio: .1, Smoothing: .5},
		{Min: 0, Max: 10, TargetCPU: 0, LatencyGoal: 1, DecreaseFactor: .5, IncreaseRatio: .1, Smoothing: .5},
		{Min: 0, Max: 10, TargetCPU: .5, LatencyGoal: 0, DecreaseFactor: .5, IncreaseRatio: .1, Smoothing: .5},
		{Min: 0, Max: 10, TargetCPU: .5, LatencyGoal: 1, DecreaseFactor: 1, IncreaseRatio: .1, Smoothing: .5},
		{Min: 0, Max: 10, TargetCPU: .5, LatencyGoal: 1, DecreaseFactor: .5, IncreaseRatio: 0, Smoothing: .5},
		{Min: 0, Max: 10, TargetCPU: .5, LatencyGoal: 1, DecreaseFactor: .5, IncreaseRatio: .1, Smoothing: 0},
		{Min: 0, Max: 10, TargetCPU: .5, LatencyGoal: 1, DecreaseFactor: .5, IncreaseRatio: .1, Cooldown: -1, Smoothing: .5},
	}
	for i, cfg := range bad {
		if _, err := NewController(cfg); err == nil {
			t.Fatalf("bad config %d accepted", i)
		}
	}
}

// 边界：持续过载，阈值单调降到 Min 后钳制，绝不越界。
func TestDecreaseClampsAtMin(t *testing.T) {
	c, _ := newTestController(t, DefaultConfig())
	prev := c.Threshold()
	for i := 0; i < 200; i++ {
		got := c.Update(overloaded(1000))
		if got > prev {
			t.Fatalf("threshold increased under overload: %v -> %v", prev, got)
		}
		if got < c.cfg.Min {
			t.Fatalf("threshold below Min: %v", got)
		}
		prev = got
	}
	if prev != c.cfg.Min {
		t.Fatalf("threshold = %v, want clamped at Min %v", prev, c.cfg.Min)
	}
	// 继续过载也不应低于 Min。
	for i := 0; i < 50; i++ {
		if got := c.Update(overloaded(1000)); got != c.cfg.Min {
			t.Fatalf("threshold escaped Min: %v", got)
		}
	}
}

// 边界：持续健康且需求饱和，阈值升到 Max 后钳制，绝不越界。
func TestIncreaseClampsAtMax(t *testing.T) {
	c, clk := newTestController(t, DefaultConfig())
	prev := c.Threshold()
	for i := 0; i < 2000; i++ {
		clk.advance(3 * time.Second) // 越过冷却期
		got := c.Update(healthy(math.MaxInt32))
		if got < prev {
			t.Fatalf("threshold decreased while healthy: %v -> %v", prev, got)
		}
		if got > c.cfg.Max {
			t.Fatalf("threshold above Max: %v", got)
		}
		prev = got
	}
	if prev != c.cfg.Max {
		t.Fatalf("threshold = %v, want Max %v", prev, c.cfg.Max)
	}
}

// 负载感知：健康但无需求（InFlight 低）时不得上调，避免空耗资源。
func TestNoIncreaseWithoutDemand(t *testing.T) {
	c, clk := newTestController(t, DefaultConfig())
	for i := 0; i < 100; i++ {
		clk.advance(3 * time.Second)
		if got := c.Update(healthy(0)); got != c.cfg.Min {
			t.Fatalf("threshold rose without demand: %v", got)
		}
	}
}

// 雪崩规避：错误率越线立即回退（不等 EMA 爬升），且冷却期内禁止上调。
func TestErrorRateTripsImmediateBackoffAndCooldown(t *testing.T) {
	c, clk := newTestController(t, DefaultConfig())
	// 先爬坡抬高阈值。
	for i := 0; i < 50; i++ {
		clk.advance(3 * time.Second)
		c.Update(healthy(math.MaxInt32))
	}
	high := c.Threshold()
	if high <= c.cfg.Min {
		t.Fatal("precondition: threshold should have risen")
	}
	// 错误率尖峰：单次采样即触发回退。
	after := c.Update(Metrics{InFlight: 10, CPU: 0.1, LatencyP99: 0.01, ErrorRate: 0.5})
	if after >= high {
		t.Fatalf("no immediate backoff on error spike: %v -> %v", high, after)
	}
	// 冷却期内：即使健康且饱和也不得上调（累计推进仍小于冷却期）。
	for i := 0; i < 10; i++ {
		clk.advance(c.cfg.Cooldown / 20)
		if got := c.Update(healthy(math.MaxInt32)); got > after {
			t.Fatalf("threshold rose during cooldown: %v -> %v", after, got)
		}
	}
	// 冷却期结束后允许恢复。
	clk.advance(c.cfg.Cooldown * 2)
	if got := c.Update(healthy(math.MaxInt32)); got <= after {
		t.Fatalf("threshold did not recover after cooldown: %v -> %v", after, got)
	}
}

// 脏指标防御：NaN / Inf / 负值直接丢弃，阈值不变。
func TestInvalidMetricsIgnored(t *testing.T) {
	c, _ := newTestController(t, DefaultConfig())
	before := c.Threshold()
	dirty := []Metrics{
		{InFlight: 1, CPU: math.NaN()},
		{InFlight: 1, CPU: math.Inf(1)},
		{InFlight: 1, LatencyP99: math.NaN()},
		{InFlight: 1, ErrorRate: -0.5},
		{InFlight: -1, CPU: 0.5},
		{InFlight: 1, CPU: -0.1},
	}
	for i, m := range dirty {
		if got := c.Update(m); got != before {
			t.Fatalf("dirty metrics %d changed threshold: %v -> %v", i, before, got)
		}
	}
	if p := c.State().Pressure; p != 0 {
		t.Fatalf("dirty metrics polluted pressure EMA: %v", p)
	}
}

// 迟滞带：压力处于 [0.8, 1.0] 区间时阈值保持稳定（防抖）。
func TestHysteresisBandStable(t *testing.T) {
	c, clk := newTestController(t, DefaultConfig())
	// 让 EMA 稳定在迟滞带内：CPU 使压力 ~= 0.9。
	m := Metrics{InFlight: math.MaxInt32, CPU: 0.9 * c.cfg.TargetCPU, LatencyP99: 0}
	for i := 0; i < 30; i++ {
		c.Update(m) // 先让 EMA 收敛
	}
	stable := c.Threshold()
	for i := 0; i < 50; i++ {
		clk.advance(3 * time.Second)
		if got := c.Update(m); got != stable {
			t.Fatalf("threshold moved inside hysteresis band: %v -> %v", stable, got)
		}
	}
}

// 并发安全：多 goroutine 混合读写，阈值永远在 [Min, Max]（配合 -race）。
func TestConcurrentUpdateSafety(t *testing.T) {
	c, err := NewController(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var readers, writers sync.WaitGroup
	violations := make(chan float64, 1)
	// 读线程：持续断言阈值在界内。
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					s := c.State()
					if s.Threshold < c.cfg.Min || s.Threshold > c.cfg.Max ||
						math.IsNaN(s.Threshold) || math.IsNaN(s.Pressure) {
						select {
						case violations <- s.Threshold:
						default:
						}
						return
					}
				}
			}
		}()
	}
	// 写线程：随机指标洪峰（含越界压力）。
	for i := 0; i < 32; i++ {
		writers.Add(1)
		go func(seed int64) {
			defer writers.Done()
			r := seed
			next := func() float64 {
				r = r*6364136223846793005 + 1442695040888963407
				return float64(uint64(r)>>33) / float64(1<<31)
			}
			for j := 0; j < 2000; j++ {
				c.Update(Metrics{
					InFlight:   int(next() * 1024),
					CPU:        next() * 1.5,
					LatencyP99: next(),
					ErrorRate:  next() * 0.1,
				})
			}
		}(int64(i) + 1)
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	select {
	case v := <-violations:
		t.Fatalf("threshold out of bounds concurrently: %v", v)
	default:
	}
	if final := c.Threshold(); final < c.cfg.Min || final > c.cfg.Max {
		t.Fatalf("final threshold out of bounds: %v", final)
	}
}
