// Package hedged fetches values from a primary data source and, on a cache
// miss, optionally starts a delayed second (hedge) source. Candidate results
// carry a version and a CRC32 checksum; the first valid result that meets the
// configured minimum version wins. Losing candidates are canceled, late
// results are discarded, speculative requests are limited by a global quota,
// and Close releases all resources.
//
// Data sources, the clock, and failures are all injectable; there is no
// dependency on any remote cache.
package hedged

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"sync"
	"time"
)

// ErrClosed is returned by Get after Close has been called.
var ErrClosed = errors.New("hedged: fetcher is closed")

// Result is a candidate response from a data source.
type Result struct {
	Value    []byte
	Version  uint64
	Checksum uint32 // IEEE CRC32 of Value
}

// NewResult builds a Result with the checksum computed from value.
func NewResult(value []byte, version uint64) Result {
	return Result{Value: value, Version: version, Checksum: crc32.ChecksumIEEE(value)}
}

// Valid reports whether the checksum matches the value.
func (r Result) Valid() bool {
	return crc32.ChecksumIEEE(r.Value) == r.Checksum
}

// Source fetches the value for key. It must respect ctx cancellation.
type Source func(ctx context.Context, key string) (Result, error)

// Clock abstracts timers so tests can control time.
type Clock interface {
	// AfterFunc schedules f to run after d and returns a stop function.
	// stop reports whether f was prevented from running.
	AfterFunc(d time.Duration, f func()) (stop func() bool)
}

// RealClock implements Clock with time.AfterFunc.
type RealClock struct{}

// AfterFunc implements Clock.
func (RealClock) AfterFunc(d time.Duration, f func()) func() bool {
	t := time.AfterFunc(d, f)
	return t.Stop
}

// Config configures a Fetcher.
type Config struct {
	Primary Source
	Hedge   Source
	Clock   Clock // defaults to RealClock
	// HedgeDelay is how long to wait for the primary before speculatively
	// starting the hedge source. Zero starts it as soon as the event loop runs.
	HedgeDelay time.Duration
	// MinVersion is the minimum acceptable result version.
	MinVersion uint64
	// SpeculativeQuota bounds the number of concurrently in-flight
	// speculative hedge requests across all Gets (global budget).
	// Values <= 0 mean "no speculative requests".
	SpeculativeQuota int
}

// Fetcher serves cached values and performs hedged fetches on misses.
type Fetcher struct {
	sources [2]Source // 0 = primary, 1 = hedge
	clock   Clock
	delay   time.Duration
	minVer  uint64
	sem     chan struct{} // global speculative quota

	mu     sync.Mutex
	cache  map[string]Result
	closed bool
	wg     sync.WaitGroup // tracks Get calls and candidate goroutines
}

// NewFetcher builds a Fetcher from cfg.
func NewFetcher(cfg Config) *Fetcher {
	clk := cfg.Clock
	if clk == nil {
		clk = RealClock{}
	}
	n := cfg.SpeculativeQuota
	if n < 0 {
		n = 0
	}
	return &Fetcher{
		sources: [2]Source{cfg.Primary, cfg.Hedge},
		clock:   clk,
		delay:   cfg.HedgeDelay,
		minVer:  cfg.MinVersion,
		sem:     make(chan struct{}, n),
		cache:   make(map[string]Result),
	}
}

// Get returns the cached value for key, or fetches it on a miss.
func (f *Fetcher) Get(ctx context.Context, key string) (Result, error) {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return Result{}, ErrClosed
	}
	if r, ok := f.cache[key]; ok {
		f.mu.Unlock()
		return r, nil
	}
	f.wg.Add(1)
	f.mu.Unlock()
	defer f.wg.Done()
	return f.fetch(ctx, key)
}

// Close marks the fetcher closed and waits for all in-flight fetches and
// candidate goroutines to finish. Callers should cancel their contexts to
// unblock in-flight Gets.
func (f *Fetcher) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.wg.Wait()
	return nil
}

type outcome struct {
	res Result
	err error
	src int
}

func (o outcome) classify(minVer uint64) error {
	switch {
	case o.err != nil:
		return fmt.Errorf("source %d: %w", o.src, o.err)
	case !o.res.Valid():
		return fmt.Errorf("source %d: checksum mismatch", o.src)
	default:
		return fmt.Errorf("source %d: version %d below minimum %d", o.src, o.res.Version, minVer)
	}
}

func (f *Fetcher) fetch(ctx context.Context, key string) (Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // cancels losing candidates on every return path

	resCh := make(chan outcome, 2) // buffered: late sends never block

	var mu sync.Mutex
	done := false         // winner chosen or fetch abandoned
	hedgeStarted := false // hedge launched (speculative or fallback)
	pending := 0          // started candidates still running
	var errs []error

	run := func(idx int, speculative bool) {
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			if speculative {
				defer func() { <-f.sem }() // release quota slot
			}
			r, err := f.sources[idx](ctx, key)
			resCh <- outcome{res: r, err: err, src: idx}
		}()
	}

	// Start the primary immediately.
	mu.Lock()
	pending++
	mu.Unlock()
	run(0, false)

	// Arm the hedge timer. The done check under mu makes the
	// timer-vs-completion race safe: once a winner is chosen the timer
	// callback can no longer start the hedge, and stopTimer prevents a
	// callback that has not begun.
	stopTimer := f.clock.AfterFunc(f.delay, func() {
		mu.Lock()
		if done || hedgeStarted {
			mu.Unlock()
			return
		}
		select {
		case f.sem <- struct{}{}: // acquire global speculative quota
			hedgeStarted = true
			pending++
			mu.Unlock()
			run(1, true)
		default:
			// No quota: skip the speculative request, rely on the primary.
			mu.Unlock()
		}
	})
	defer stopTimer()

	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			done = true
			mu.Unlock()
			return Result{}, ctx.Err()
		case out := <-resCh:
			mu.Lock()
			if done {
				// Late result after a winner (or cancellation): discard.
				mu.Unlock()
				continue
			}
			pending--
			if out.err == nil && out.res.Valid() && out.res.Version >= f.minVer {
				done = true
				mu.Unlock()
				stopTimer()
				cancel() // cancel losing candidates
				f.mu.Lock()
				if !f.closed {
					f.cache[key] = out.res
				}
				f.mu.Unlock()
				return out.res, nil
			}
			errs = append(errs, out.classify(f.minVer))
			if pending == 0 && !hedgeStarted {
				// The primary finished (unsuccessfully) before the hedge
				// timer fired: fall back to the hedge immediately. This is
				// no longer speculative, so it does not consume quota.
				hedgeStarted = true
				pending++
				mu.Unlock()
				stopTimer()
				run(1, false)
				continue
			}
			if pending == 0 {
				// Both sources failed (or returned invalid/stale data):
				// aggregate the reasons.
				done = true
				mu.Unlock()
				return Result{}, errors.Join(errs...)
			}
			mu.Unlock()
		}
	}
}
