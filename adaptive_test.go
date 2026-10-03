package hexgrid

import (
	"math"
	"runtime"
	"sync"
	"testing"
	"time"
)

func testConfig() AdaptiveConfig {
	return AdaptiveConfig{
		Min: 10, Max: 1000, Initial: 500,
		HighWatermark: 0.75, LowWatermark: 0.30,
		IncreaseStep: 25, DecreaseFactor: 0.7,
		Smoothing: 0.5, Cooldown: 100 * time.Millisecond,
		PressureGain: 1.0,
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestController(t *testing.T, cfg AdaptiveConfig) (*AdaptiveController, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Unix(0, 0)}
	c, err := NewAdaptiveController(cfg, WithClock(clk.now))
	if err != nil {
		t.Fatalf("NewAdaptiveController: %v", err)
	}
	return c, clk
}

func TestEnvMetricsPressure(t *testing.T) {
	if got := (EnvMetrics{}).Pressure(); got != 0 {
		t.Fatalf("zero metrics pressure = %v, want 0", got)
	}
	full := EnvMetrics{CPUUtil: 1, MemUtil: 1, QueueDepth: 1, ErrorRate: 1}
	if got := full.Pressure(); got != 1 {
		t.Fatalf("full metrics pressure = %v, want 1", got)
	}
	// Out-of-range inputs are clamped.
	over := EnvMetrics{CPUUtil: 5, MemUtil: -1}
	if got := over.Pressure(); got != weightCPU {
		t.Fatalf("clamped pressure = %v, want %v", got, weightCPU)
	}
	// CPU dominates.
	cpuOnly := EnvMetrics{CPUUtil: 1}
	if got := cpuOnly.Pressure(); math.Abs(got-weightCPU) > 1e-12 {
		t.Fatalf("cpu-only pressure = %v, want %v", got, weightCPU)
	}
}

func TestControllerConfigValidation(t *testing.T) {
	valid := testConfig()
	cases := map[string]func(*AdaptiveConfig){
		"min<=0":         func(c *AdaptiveConfig) { c.Min = 0 },
		"max<min":        func(c *AdaptiveConfig) { c.Max = c.Min - 1 },
		"initial<min":    func(c *AdaptiveConfig) { c.Initial = c.Min - 1 },
		"initial>max":    func(c *AdaptiveConfig) { c.Initial = c.Max + 1 },
		"low<0":          func(c *AdaptiveConfig) { c.LowWatermark = -0.1 },
		"high>1":         func(c *AdaptiveConfig) { c.HighWatermark = 1.1 },
		"low>=high":      func(c *AdaptiveConfig) { c.LowWatermark = c.HighWatermark },
		"step<=0":        func(c *AdaptiveConfig) { c.IncreaseStep = 0 },
		"factor<=0":      func(c *AdaptiveConfig) { c.DecreaseFactor = 0 },
		"factor>=1":      func(c *AdaptiveConfig) { c.DecreaseFactor = 1 },
		"smoothing<=0":   func(c *AdaptiveConfig) { c.Smoothing = 0 },
		"smoothing>1":    func(c *AdaptiveConfig) { c.Smoothing = 1.5 },
		"cooldown<0":     func(c *AdaptiveConfig) { c.Cooldown = -time.Second },
		"pressureGain<0": func(c *AdaptiveConfig) { c.PressureGain = -1 },
	}
	for name, mutate := range cases {
		cfg := valid
		mutate(&cfg)
		if _, err := NewAdaptiveController(cfg); err == nil {
			t.Errorf("case %s: expected validation error", name)
		}
	}
	if _, err := NewAdaptiveController(valid); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}

func TestControllerDecreaseUnderOverload(t *testing.T) {
	c, clk := newTestController(t, testConfig())
	hot := EnvMetrics{CPUUtil: 1, MemUtil: 1, QueueDepth: 1, ErrorRate: 1}

	prev := c.Threshold()
	for i := 0; i < 20; i++ {
		clk.advance(150 * time.Millisecond)
		adj := c.Observe(hot)
		if adj.NewThreshold > adj.OldThreshold {
			t.Fatalf("tick %d: threshold rose under overload: %v -> %v",
				i, adj.OldThreshold, adj.NewThreshold)
		}
		prev = adj.NewThreshold
	}
	if got := c.Threshold(); got != c.cfg.Min {
		t.Fatalf("sustained overload should floor at Min=%v, got %v (last %v)",
			c.cfg.Min, got, prev)
	}
	// Further samples must not push below Min (strict clamp, no drift).
	clk.advance(150 * time.Millisecond)
	c.Observe(hot)
	if got := c.Threshold(); got != c.cfg.Min {
		t.Fatalf("threshold drifted below Min: %v", got)
	}
}

func TestControllerIncreaseWhenIdle(t *testing.T) {
	c, clk := newTestController(t, testConfig())
	idle := EnvMetrics{CPUUtil: 0.02, QueueDepth: 0.01}

	// Drive threshold down first so there is room to grow.
	hot := EnvMetrics{CPUUtil: 1, MemUtil: 1, QueueDepth: 1, ErrorRate: 1}
	for i := 0; i < 20; i++ {
		clk.advance(150 * time.Millisecond)
		c.Observe(hot)
	}
	floor := c.Threshold()

	rose := false
	for i := 0; i < 100; i++ {
		clk.advance(150 * time.Millisecond)
		adj := c.Observe(idle)
		if adj.NewThreshold < adj.OldThreshold {
			t.Fatalf("tick %d: threshold fell while idle", i)
		}
		if adj.NewThreshold > adj.OldThreshold {
			rose = true
		}
	}
	if !rose {
		t.Fatal("threshold never recovered while idle")
	}
	if got := c.Threshold(); got != c.cfg.Max {
		t.Fatalf("sustained idle should cap at Max=%v, got %v (floor was %v)",
			c.cfg.Max, got, floor)
	}
}

func TestControllerHysteresisBand(t *testing.T) {
	c, clk := newTestController(t, testConfig())
	// Load sits inside the [low, high] hysteresis band: no adjustment
	// may ever fire, however many samples arrive.
	mid := EnvMetrics{CPUUtil: 0.5, QueueDepth: 0.5, MemUtil: 0.5, ErrorRate: 0.5}
	for i := 0; i < 50; i++ {
		clk.advance(150 * time.Millisecond)
		adj := c.Observe(mid)
		if adj.Adjusted {
			t.Fatalf("tick %d: adjustment inside hysteresis band", i)
		}
	}
	if got := c.Threshold(); got != c.cfg.Initial {
		t.Fatalf("threshold moved inside hysteresis band: %v", got)
	}
}

func TestControllerCooldownBlocksAdjustment(t *testing.T) {
	c, clk := newTestController(t, testConfig())
	hot := EnvMetrics{CPUUtil: 1, MemUtil: 1, QueueDepth: 1}

	clk.advance(150 * time.Millisecond)
	first := c.Observe(hot)
	if !first.Adjusted {
		t.Fatal("first hot sample should adjust")
	}
	// Within the cooldown window: no adjustment even under max load.
	for i := 0; i < 5; i++ {
		clk.advance(10 * time.Millisecond)
		adj := c.Observe(hot)
		if adj.Adjusted {
			t.Fatalf("adjustment during cooldown at tick %d", i)
		}
	}
	// After cooldown expires: adjustment resumes.
	clk.advance(200 * time.Millisecond)
	if adj := c.Observe(hot); !adj.Adjusted {
		t.Fatal("adjustment should resume after cooldown")
	}
}

func TestControllerEMASmoothing(t *testing.T) {
	cfg := testConfig()
	cfg.Smoothing = 0.5
	c, clk := newTestController(t, cfg)

	clk.advance(time.Second)
	c.Observe(EnvMetrics{CPUUtil: 1}) // first sample seeds the EMA
	if got := c.Snapshot().SmoothedLoad; got != weightCPU {
		t.Fatalf("first sample should seed ema to %v, got %v", weightCPU, got)
	}
	clk.advance(time.Second)
	c.Observe(EnvMetrics{}) // zero load
	want := weightCPU + 0.5*(0-weightCPU)
	if got := c.Snapshot().SmoothedLoad; math.Abs(got-want) > 1e-12 {
		t.Fatalf("ema = %v, want %v", got, want)
	}
}

func TestControllerPressureProportionalBackoff(t *testing.T) {
	cfg := testConfig()
	cfg.PressureGain = 2.0
	cfg.Smoothing = 1.0 // no smoothing: ema == instantaneous load

	mild, mildClk := newTestController(t, cfg)
	severe, severeClk := newTestController(t, cfg)

	mildClk.advance(time.Second)
	severeClk.advance(time.Second)
	mildAdj := mild.Observe(EnvMetrics{CPUUtil: 1, QueueDepth: 1, MemUtil: 0.6})                 // pressure 0.77, just above the 0.75 watermark
	severeAdj := severe.Observe(EnvMetrics{CPUUtil: 1, MemUtil: 1, QueueDepth: 1, ErrorRate: 1}) // pressure 1.0

	mildCut := mildAdj.OldThreshold - mildAdj.NewThreshold
	severeCut := severeAdj.OldThreshold - severeAdj.NewThreshold
	if severeCut <= mildCut {
		t.Fatalf("severe pressure cut (%v) should exceed mild cut (%v)", severeCut, mildCut)
	}
}

func TestControllerAllow(t *testing.T) {
	c, _ := newTestController(t, testConfig())
	if !c.Allow(100) {
		t.Fatal("cost below threshold should be admitted")
	}
	if c.Allow(1e9) {
		t.Fatal("cost above threshold should be rejected")
	}
	if !c.Allow(c.Threshold()) {
		t.Fatal("cost exactly at threshold should be admitted")
	}
}

func TestControllerConcurrencySafety(t *testing.T) {
	c, clk := newTestController(t, testConfig())

	const workers = 32
	const samples = 500
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < samples; i++ {
				load := float64((w+i)%100) / 100
				c.Observe(EnvMetrics{CPUUtil: load, QueueDepth: load})
				c.Allow(load * 100)
				c.Snapshot()
				c.Threshold()
				_ = c.String()
			}
		}(w)
	}
	// Concurrently advance time to exercise the cooldown path.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				clk.advance(time.Millisecond)
			}
		}
	}()
	wg.Wait()
	close(done)

	snap := c.Snapshot()
	if snap.Threshold < snap.Min || snap.Threshold > snap.Max {
		t.Fatalf("threshold %v escaped bounds [%v, %v]", snap.Threshold, snap.Min, snap.Max)
	}
	if snap.SmoothedLoad < 0 || snap.SmoothedLoad > 1 {
		t.Fatalf("ema %v escaped [0, 1]", snap.SmoothedLoad)
	}
}

func TestControllerNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	for i := 0; i < 50; i++ {
		c, clk := newTestController(t, testConfig())
		for j := 0; j < 10; j++ {
			clk.advance(150 * time.Millisecond)
			c.Observe(EnvMetrics{CPUUtil: float64(j) / 10})
		}
	}
	// The controller spawns no goroutines; allow brief scheduler settle.
	var after int
	for i := 0; i < 100; i++ {
		after = runtime.NumGoroutine()
		if after <= before {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}
