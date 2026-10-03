// Package adaptive implements a self-tuning concurrency limiter that
// scales its internal threshold dynamically in response to observed
// latency and external load metrics.
//
// The controller combines three classic mechanisms:
//
//   - AIMD threshold scaling: additive increase when latency is healthy,
//     multiplicative decrease when latency degrades or work is dropped.
//   - Load awareness: an external pressure signal (CPU, queue depth, ...)
//     shrinks the effective threshold so the system sheds load early
//     instead of cascading (avalanche avoidance).
//   - Recovery jitter + cooldown: increases are randomized and decreases
//     are rate-limited, preventing thundering-herd oscillation.
//
// All state transitions are mutex-guarded and safe for concurrent use.
package adaptive

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// ErrLimitExceeded is returned by TryAcquire when the effective threshold
// is saturated.
var ErrLimitExceeded = errors.New("adaptive: concurrency limit exceeded")

// Config tunes a Limiter. Zero fields take documented defaults.
type Config struct {
	MinLimit int // hard floor for the threshold (default 1)
	MaxLimit int // hard ceiling for the threshold (default 100)
	Initial  int // starting threshold (default MinLimit)

	// IncreaseStep is the additive increase per healthy sample window
	// (default 1). DecreaseFactor is the multiplicative decrease applied
	// on congestion, in (0,1) (default 0.9).
	IncreaseStep   int
	DecreaseFactor float64

	// LoadSensitivity in [0,1] controls how strongly external load
	// shrinks the effective threshold: eff = limit * (1 - sens*load).
	// Default 0.5.
	LoadSensitivity float64

	// Smoothing is the EWMA alpha for latency baselines in (0,1]
	// (default 0.2). LatencyTolerance is how far above the long-term
	// baseline latency may drift before congestion is declared
	// (default 1.5, i.e. 150% of baseline).
	Smoothing        float64
	LatencyTolerance float64

	// Cooldown is the minimum interval between two decreases
	// (default 200ms), damping oscillation.
	Cooldown time.Duration

	// MetricsFunc, when non-nil, is sampled every SampleInterval to
	// obtain external load in [0,1]. SampleInterval defaults to 1s.
	MetricsFunc    func() float64
	SampleInterval time.Duration

	// Now supplies the clock; injectable for deterministic tests.
	Now func() time.Time
}

// Stats is a point-in-time snapshot of limiter state.
type Stats struct {
	Limit          int     // current adaptive threshold
	EffectiveLimit int     // threshold after load shedding
	InFlight       int     // outstanding permits
	Rejected       uint64  // total rejected acquisitions
	Admitted       uint64  // total granted permits
	Load           float64 // last sampled external load
	RTTMin         float64 // long-term latency baseline (ns)
	RTTAvg         float64 // smoothed recent latency (ns)
}

// Limiter is a concurrency limiter with adaptive threshold scaling.
type Limiter struct {
	cfg Config

	mu          sync.Mutex
	limit       float64
	inFlight    int
	rejected    uint64
	admitted    uint64
	load        float64
	rttMin      float64 // nanoseconds; 0 until first sample
	rttAvg      float64 // nanoseconds; 0 until first sample
	lastDecr    time.Time
	rnd         *rand.Rand
	cond        *sync.Cond
	closed      bool
	samplerWG   sync.WaitGroup
	samplerStop chan struct{}
}

// NewLimiter builds a Limiter and starts the optional metrics sampler.
func NewLimiter(cfg Config) *Limiter {
	if cfg.MinLimit <= 0 {
		cfg.MinLimit = 1
	}
	if cfg.MaxLimit <= 0 {
		cfg.MaxLimit = 100
	}
	if cfg.MinLimit > cfg.MaxLimit {
		cfg.MaxLimit = cfg.MinLimit
	}
	if cfg.Initial <= 0 {
		cfg.Initial = cfg.MinLimit
	}
	if cfg.Initial > cfg.MaxLimit {
		cfg.Initial = cfg.MaxLimit
	}
	if cfg.IncreaseStep <= 0 {
		cfg.IncreaseStep = 1
	}
	if cfg.DecreaseFactor <= 0 || cfg.DecreaseFactor >= 1 {
		cfg.DecreaseFactor = 0.9
	}
	if cfg.LoadSensitivity <= 0 || cfg.LoadSensitivity > 1 {
		cfg.LoadSensitivity = 0.5
	}
	if cfg.Smoothing <= 0 || cfg.Smoothing > 1 {
		cfg.Smoothing = 0.2
	}
	if cfg.LatencyTolerance <= 1 {
		cfg.LatencyTolerance = 1.5
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 200 * time.Millisecond
	}
	if cfg.SampleInterval <= 0 {
		cfg.SampleInterval = time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	l := &Limiter{
		cfg:         cfg,
		limit:       float64(cfg.Initial),
		rnd:         rand.New(rand.NewSource(cfg.Now().UnixNano())),
		samplerStop: make(chan struct{}),
	}
	l.cond = sync.NewCond(&l.mu)
	if cfg.MetricsFunc != nil {
		l.samplerWG.Add(1)
		go l.sampleLoop()
	}
	return l
}

// Permit represents one granted slot. Exactly one of Release/Drop must be
// called when work finishes; failing to do so leaks the slot.
type Permit struct {
	l     *Limiter
	start time.Time
	once  sync.Once
}

// Release returns the slot, reporting a successful operation. The observed
// round-trip time feeds the adaptive controller.
func (p *Permit) Release() { p.finish(false) }

// Drop returns the slot, reporting a failed/dropped operation; the
// controller treats this as congestion regardless of latency.
func (p *Permit) Drop() { p.finish(true) }

func (p *Permit) finish(dropped bool) {
	p.once.Do(func() {
		rtt := p.l.cfg.Now().Sub(p.start)
		p.l.release(rtt, dropped)
	})
}

// TryAcquire grants a slot if the effective threshold is not saturated,
// otherwise it returns ErrLimitExceeded immediately.
func (l *Limiter) TryAcquire() (*Permit, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, errors.New("adaptive: limiter closed")
	}
	if l.inFlight >= l.effectiveLocked() {
		l.rejected++
		return nil, ErrLimitExceeded
	}
	l.inFlight++
	l.admitted++
	return &Permit{l: l, start: l.cfg.Now()}, nil
}

// Acquire blocks until a slot is free or ctx is done. The context-aware
// wait uses a short poll so it remains Go 1.11 compatible.
func (l *Limiter) Acquire(ctx context.Context) (*Permit, error) {
	for {
		p, err := l.TryAcquire()
		if err == nil {
			return p, nil
		}
		if err != ErrLimitExceeded {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(l.backoff()):
		}
	}
}

// backoff computes a jittered, load-scaled retry delay: higher load and
// higher rejection pressure mean longer waits, decorrelating retries so a
// saturated downstream is not stampedes (avalanche avoidance).
func (l *Limiter) backoff() time.Duration {
	l.mu.Lock()
	load := l.load
	inFlight := l.inFlight
	eff := l.effectiveLocked()
	jitter := l.rnd.Float64()
	l.mu.Unlock()

	pressure := 1.0
	if eff > 0 {
		pressure = float64(inFlight) / float64(eff)
	}
	base := time.Duration(5+50*load+25*pressure) * time.Millisecond
	return time.Duration(float64(base) * (0.5 + jitter))
}

func (l *Limiter) release(rtt time.Duration, dropped bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight > 0 {
		l.inFlight--
	}
	ns := float64(rtt)
	if ns <= 0 {
		ns = 1
	}
	if l.rttMin == 0 || ns < l.rttMin {
		l.rttMin = ns
	}
	if l.rttAvg == 0 {
		l.rttAvg = ns
	} else {
		l.rttAvg = l.cfg.Smoothing*ns + (1-l.cfg.Smoothing)*l.rttAvg
	}

	congested := dropped || l.rttAvg > l.rttMin*l.cfg.LatencyTolerance
	now := l.cfg.Now()
	switch {
	case congested:
		if now.Sub(l.lastDecr) >= l.cfg.Cooldown {
			l.limit *= l.cfg.DecreaseFactor
			l.lastDecr = now
		}
	default:
		// Additive increase with jitter in [0.5, 1.0] of the step so
		// concurrent recoveries do not move in lock-step.
		step := float64(l.cfg.IncreaseStep) * (0.5 + 0.5*l.rnd.Float64())
		l.limit += step
	}
	l.clampLocked()
}

// SetLoad records an external pressure reading in [0,1]. Values outside
// the range are clamped. Safe for concurrent use.
func (l *Limiter) SetLoad(load float64) {
	if load < 0 {
		load = 0
	}
	if load > 1 {
		load = 1
	}
	l.mu.Lock()
	l.load = load
	l.mu.Unlock()
}

// Stats returns a consistent snapshot of the controller state.
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{
		Limit:          int(l.limit),
		EffectiveLimit: l.effectiveLocked(),
		InFlight:       l.inFlight,
		Rejected:       l.rejected,
		Admitted:       l.admitted,
		Load:           l.load,
		RTTMin:         l.rttMin,
		RTTAvg:         l.rttAvg,
	}
}

// Close stops the metrics sampler and rejects future acquisitions. It is
// idempotent and waits for the sampler goroutine to exit (no leaks).
func (l *Limiter) Close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	close(l.samplerStop)
	l.mu.Unlock()
	l.samplerWG.Wait()
}

func (l *Limiter) sampleLoop() {
	defer l.samplerWG.Done()
	t := time.NewTicker(l.cfg.SampleInterval)
	defer t.Stop()
	for {
		select {
		case <-l.samplerStop:
			return
		case <-t.C:
			l.SetLoad(l.cfg.MetricsFunc())
		}
	}
}

// effectiveLocked applies load-aware shedding to the adaptive threshold.
func (l *Limiter) effectiveLocked() int {
	eff := l.limit * (1 - l.cfg.LoadSensitivity*l.load)
	if eff < float64(l.cfg.MinLimit) {
		return l.cfg.MinLimit
	}
	return int(eff)
}

func (l *Limiter) clampLocked() {
	if l.limit < float64(l.cfg.MinLimit) {
		l.limit = float64(l.cfg.MinLimit)
	}
	if l.limit > float64(l.cfg.MaxLimit) {
		l.limit = float64(l.cfg.MaxLimit)
	}
}
