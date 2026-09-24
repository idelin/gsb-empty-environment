// Package hedged implements a hedged (speculative) dual-source fetcher.
//
// On a cache miss the caller may fetch from a primary source and, after a
// configurable delay, speculatively start a secondary source. Candidates
// carry a version and a checksum; the first valid result that meets the
// minimum version wins. Losing candidates are cancelled, speculative
// requests are bounded by a global budget, and Close releases everything.
package hedged

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"sync"
	"time"
)

// ErrClosed is returned by Fetch after the Fetcher has been closed.
var ErrClosed = errors.New("hedged: fetcher closed")

// Result is a candidate value returned by a Source.
type Result struct {
	Value    []byte
	Version  uint64
	Checksum uint32 // checksum of Value, verified with Config.Checksum
}

// Source fetches a candidate result. It must honour ctx cancellation.
type Source func(ctx context.Context) (Result, error)

// Timer abstracts time.Timer so tests can inject a fake clock.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// Clock abstracts the time package.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

func (realClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

// Config configures a Fetcher. All dependencies are injectable.
type Config struct {
	Primary        Source
	Secondary      Source
	MinVersion     uint64              // results below this version can never win
	HedgeDelay     time.Duration       // delay before the secondary source is started
	MaxSpeculative int                 // global budget of concurrent speculative requests
	Clock          Clock               // defaults to the real clock
	Checksum       func([]byte) uint32 // defaults to crc32.ChecksumIEEE
}

// Fetcher fetches from two sources with hedging and validation.
type Fetcher struct {
	cfg Config
	sem chan struct{} // global speculative budget

	mu      sync.Mutex
	nextID  int
	cancels map[int]context.CancelFunc
	closed  bool

	wg sync.WaitGroup // in-flight source goroutines
}

// New validates cfg and returns a Fetcher.
func New(cfg Config) (*Fetcher, error) {
	if cfg.Primary == nil || cfg.Secondary == nil {
		return nil, errors.New("hedged: primary and secondary sources are required")
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Checksum == nil {
		cfg.Checksum = crc32.ChecksumIEEE
	}
	if cfg.MaxSpeculative <= 0 {
		cfg.MaxSpeculative = 1
	}
	return &Fetcher{
		cfg:     cfg,
		sem:     make(chan struct{}, cfg.MaxSpeculative),
		cancels: make(map[int]context.CancelFunc),
	}, nil
}

// valid reports whether r is legal: fresh enough and checksum-correct.
func (f *Fetcher) valid(r Result) bool {
	return r.Version >= f.cfg.MinVersion && f.cfg.Checksum(r.Value) == r.Checksum
}

// Fetch returns the first valid result meeting MinVersion. A result that
// arrives first but is too old or corrupt cannot win. If every candidate
// fails, the returned error aggregates all reasons via errors.Join.
func (f *Fetcher) Fetch(ctx context.Context) (Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	id, ok := f.track(cancel)
	if !ok {
		cancel()
		return Result{}, ErrClosed
	}
	defer f.untrack(id)
	defer cancel()

	type outcome struct {
		src string
		res Result
		err error
	}
	// Buffered for both candidates so late senders never block or leak.
	ch := make(chan outcome, 2)

	launch := func(src string, s Source) {
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			res, err := s(ctx)
			ch <- outcome{src: src, res: res, err: err}
		}()
	}

	// tryHedge starts the secondary at most once and only while the global
	// speculative budget allows. It reports whether a request was launched.
	var hedgeOnce sync.Once
	tryHedge := func() bool {
		launched := false
		hedgeOnce.Do(func() {
			select {
			case f.sem <- struct{}{}:
			default:
				return // speculative budget exhausted
			}
			launched = true
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				defer func() { <-f.sem }()
				res, err := f.cfg.Secondary(ctx)
				ch <- outcome{src: "secondary", res: res, err: err}
			}()
		})
		return launched
	}

	launch("primary", f.cfg.Primary)
	pending := 1

	timer := f.cfg.Clock.NewTimer(f.cfg.HedgeDelay)
	defer timer.Stop()

	var errs []error
	// handle processes one outcome; it returns the winning result if any.
	handle := func(o outcome) (Result, bool) {
		pending--
		if o.err == nil && f.valid(o.res) {
			cancel() // cancel losing candidates
			return o.res, true
		}
		if o.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", o.src, o.err))
		} else {
			errs = append(errs, fmt.Errorf("%s: rejected result: version %d (need >= %d), checksum ok=%t",
				o.src, o.res.Version, f.cfg.MinVersion,
				f.cfg.Checksum(o.res.Value) == o.res.Checksum))
		}
		// A failed candidate triggers the hedge immediately.
		if tryHedge() {
			pending++
		}
		return Result{}, false
	}

	for pending > 0 {
		select {
		case <-timer.C():
			// Timer/completion race: re-check for a completed outcome
			// before spending a speculative request.
			select {
			case o := <-ch:
				if r, ok := handle(o); ok {
					return r, nil
				}
			default:
				if tryHedge() {
					pending++
				}
			}
		case o := <-ch:
			if r, ok := handle(o); ok {
				return r, nil
			}
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	return Result{}, errors.Join(errs...)
}

// track registers cancel unless the Fetcher is already closed.
func (f *Fetcher) track(cancel context.CancelFunc) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, false
	}
	f.nextID++
	f.cancels[f.nextID] = cancel
	return f.nextID, true
}

func (f *Fetcher) untrack(id int) {
	f.mu.Lock()
	delete(f.cancels, id)
	f.mu.Unlock()
}

// Close cancels all in-flight Fetch calls, waits for every source
// goroutine to exit, and releases all resources. It is idempotent.
func (f *Fetcher) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	for _, cancel := range f.cancels {
		cancel()
	}
	f.mu.Unlock()
	f.wg.Wait()
	return nil
}
