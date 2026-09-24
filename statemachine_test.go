package negcache

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

// The state machine in this file is an independent model of the cache. It
// re-derives expected states (entry kind, class, TTLs, expiries) from the
// operation history and checks the real cache against it.

type smState int

const (
	smEmpty smState = iota
	smPositive
	smNegative
)

func (s smState) String() string {
	switch s {
	case smPositive:
		return "positive"
	case smNegative:
		return "negative"
	default:
		return "empty"
	}
}

// TestStateMachine_SuccessSurvivesLateNotFound drives the cache through
// Empty -> InFlight(not-found) -> Success(v2) and proves the late not-found
// response cannot drag the state back to Negative.
func TestStateMachine_SuccessSurvivesLateNotFound(t *testing.T) {
	clock := NewFakeClock(t0)
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	mode := "notfound"
	src := func(context.Context, string) Response[string] {
		mu.Lock()
		m := mode
		mu.Unlock()
		if m == "notfound" {
			close(started)
			<-release
			return Response[string]{Version: 1, Err: NotFound(ErrNotFound)}
		}
		return Response[string]{Value: "success-v2", Version: 2}
	}
	c := New[string](Config[string]{Source: src, Clock: clock})
	defer c.Close()

	first := make(chan error, 1)
	go func() {
		_, err := c.Get(context.Background(), "t", "k")
		first <- err
	}()
	<-started // state: InFlight(not-found v1)

	// A real success commits while the not-found fetch is still in flight.
	c.Invalidate("t", "k")
	mu.Lock()
	mode = "success"
	mu.Unlock()
	if v, err := c.Get(context.Background(), "t", "k"); err != nil || v != "success-v2" {
		t.Fatalf("state want Positive(v2), got %q, %v", v, err)
	}
	close(release) // late not-found v1 lands now

	if err := <-first; err != nil {
		// The waiter of the stale fetch is upgraded to the fresh success.
		t.Fatalf("stale waiter should observe success, got %v", err)
	}
	info, _ := c.Inspect("t", "k")
	if info.Negative || info.Version != 2 {
		t.Fatalf("late not-found overwrote success: %+v", info)
	}
	if v, err := c.Get(context.Background(), "t", "k"); err != nil || v != "success-v2" {
		t.Fatalf("final state want Positive(v2), got %q, %v", v, err)
	}
}

// TestStateMachine_OldErrorCannotOverwriteSuccess is the same race with a
// late transient error instead of a not-found.
func TestStateMachine_OldErrorCannotOverwriteSuccess(t *testing.T) {
	clock := NewFakeClock(t0)
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	mode := "flaky"
	src := func(context.Context, string) Response[string] {
		mu.Lock()
		m := mode
		mu.Unlock()
		if m == "flaky" {
			close(started)
			<-release
			return Response[string]{Version: 1, Err: Transient(errors.New("boom"))}
		}
		return Response[string]{Value: "ok", Version: 2}
	}
	c := New[string](Config[string]{Source: src, Clock: clock})
	defer c.Close()

	done := make(chan struct{})
	go func() { c.Get(context.Background(), "t", "k"); close(done) }()
	<-started
	c.Invalidate("t", "k")
	mu.Lock()
	mode = "ok"
	mu.Unlock()
	c.Get(context.Background(), "t", "k") // commits success v2
	close(release)
	<-done

	info, _ := c.Inspect("t", "k")
	if info.Negative || info.Version != 2 {
		t.Fatalf("late transient error overwrote success: %+v", info)
	}
}

// TestStateMachine_TransientBackoff checks the backoff ladder against the
// model min(base << failures, max) and that success resets it.
func TestStateMachine_TransientBackoff(t *testing.T) {
	clock := NewFakeClock(t0)
	const base = time.Second
	const max = 4 * time.Second
	var fail atomic.Bool
	fail.Store(true)
	src := func(context.Context, string) Response[string] {
		if fail.Load() {
			return Response[string]{Version: 1, Err: Transient(errors.New("net down"))}
		}
		return Response[string]{Value: "up", Version: 2}
	}
	c := New[string](Config[string]{
		Source: src, Clock: clock,
		PositiveTTL:  10 * time.Second,
		TransientTTL: base, BackoffMax: max, NotFoundTTL: time.Minute,
	})
	defer c.Close()

	// Model: consecutive transient failures -> TTL = min(base<<n, max).
	wantTTLs := []time.Duration{base, 2 * base, max, max}
	for step, want := range wantTTLs {
		if step > 0 {
			clock.Advance(wantTTLs[step-1]) // previous entry expired
		}
		_, err := c.Get(context.Background(), "t", "k")
		if Classify(err) != ClassTransient {
			t.Fatalf("step %d: want transient, got %v", step, err)
		}
		info, ok := c.Inspect("t", "k")
		if !ok || info.Class != ClassTransient {
			t.Fatalf("step %d: want cached transient, got %+v", step, info)
		}
		if got := info.SoftExpiry.Sub(clock.Now()); got != want {
			t.Fatalf("step %d: backoff TTL = %v, model wants %v", step, got, want)
		}
	}

	// Success resets the ladder.
	clock.Advance(max)
	fail.Store(false)
	if _, err := c.Get(context.Background(), "t", "k"); err != nil {
		t.Fatalf("want success, got %v", err)
	}
	clock.Advance(11 * time.Second) // positive entry expired
	fail.Store(true)
	c.Get(context.Background(), "t", "k")
	info, _ := c.Inspect("t", "k")
	if got := info.SoftExpiry.Sub(clock.Now()); got != base {
		t.Fatalf("backoff not reset by success: TTL = %v, want %v", got, base)
	}
}

// TestStateMachine_HardTTL proves no entry outlives its hard TTL even when
// its class TTL is longer.
func TestStateMachine_HardTTL(t *testing.T) {
	clock := NewFakeClock(t0)
	var calls int
	src := func(context.Context, string) Response[string] {
		calls++
		return Response[string]{Value: fmt.Sprint(calls), Version: uint64(calls)}
	}
	c := New[string](Config[string]{
		Source: src, Clock: clock,
		PositiveTTL: time.Hour, HardTTL: 10 * time.Second,
	})
	defer c.Close()

	v1, _ := c.Get(context.Background(), "t", "k")
	clock.Advance(9 * time.Second)
	if v, _ := c.Get(context.Background(), "t", "k"); v != v1 {
		t.Fatal("entry must live within hard TTL")
	}
	clock.Advance(2 * time.Second) // now past hard TTL, class TTL not reached
	v2, _ := c.Get(context.Background(), "t", "k")
	if v2 == v1 {
		t.Fatal("entry survived past hard TTL")
	}
	info, _ := c.Inspect("t", "k")
	if !info.HardExpiry.Equal(t0.Add(11*time.Second + 10*time.Second)) {
		t.Fatalf("hard expiry = %v", info.HardExpiry)
	}
}

// TestStateMachine_RandomModel runs a randomized operation sequence against
// an independent model and compares every observable outcome.
func TestStateMachine_RandomModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	clock := NewFakeClock(t0)

	const (
		positiveTTL  = 20 * time.Second
		notFoundTTL  = 8 * time.Second
		transientTTL = 2 * time.Second
		permanentTTL = time.Minute
		hardTTL      = 30 * time.Second
		backoffMax   = 8 * time.Second
	)

	// Source program, mutated by model ops.
	var mu sync.Mutex
	modes := map[string]Class{}
	versions := map[string]uint64{}
	src := func(_ context.Context, key string) Response[string] {
		mu.Lock()
		defer mu.Unlock()
		versions[key]++
		switch modes[key] {
		case ClassNotFound:
			return Response[string]{Version: versions[key], Err: NotFound(ErrNotFound)}
		case ClassTransient:
			return Response[string]{Version: versions[key], Err: Transient(errors.New("flaky"))}
		case ClassPermanent:
			return Response[string]{Version: versions[key], Err: Permanent(errors.New("gone"))}
		default:
			return Response[string]{Value: fmt.Sprintf("%s@%d", key, versions[key]), Version: versions[key]}
		}
	}
	c := New[string](Config[string]{
		Source: src, Clock: clock, Capacity: 64,
		PositiveTTL: positiveTTL, NotFoundTTL: notFoundTTL,
		TransientTTL: transientTTL, PermanentTTL: permanentTTL,
		HardTTL: hardTTL, BackoffMax: backoffMax,
	})
	defer c.Close()

	// Independent model.
	type mEntry struct {
		neg      bool
		class    Class
		version  uint64
		value    string
		soft, hd time.Time
	}
	model := map[string]*mEntry{}
	failures := map[string]int{}
	keys := []string{"a", "b", "c"}

	valid := func(e *mEntry, now time.Time) bool {
		return now.Before(e.soft) && now.Before(e.hd)
	}
	negTTL := func(key string, class Class) time.Duration {
		switch class {
		case ClassNotFound:
			return notFoundTTL
		case ClassPermanent:
			return permanentTTL
		default:
			d := transientTTL
			for i := 1; i < failures[key] && d < backoffMax; i++ {
				d *= 2
				if d > backoffMax {
					d = backoffMax
				}
			}
			return d
		}
	}

	for step := 0; step < 2000; step++ {
		key := keys[rng.Intn(len(keys))]
		now := clock.Now()
		switch rng.Intn(10) {
		case 0, 1: // advance time
			clock.Advance(time.Duration(rng.Intn(15)) * time.Second)
		case 2: // invalidate
			c.Invalidate("t", key)
			delete(model, key)
			delete(failures, key)
		case 3: // change source behavior
			mu.Lock()
			modes[key] = Class(rng.Intn(4)) // none, not-found, transient, permanent
			mu.Unlock()
		default: // Get and compare with the model
			e, ok := model[key]
			hit := ok && valid(e, now)
			v, err := c.Get(context.Background(), "t", key)
			if hit {
				if e.neg {
					var se *SourceError
					if !errors.As(err, &se) || se.Class != e.class {
						t.Fatalf("step %d key %s: model wants cached %s, got %v", step, key, e.class, err)
					}
				} else if err != nil || v != e.value {
					t.Fatalf("step %d key %s: model wants %q, got %q, %v", step, key, e.value, v, err)
				}
			} else {
				// Miss: the source answered and the result was committed.
				mu.Lock()
				mode := modes[key]
				ver := versions[key]
				mu.Unlock()
				ne := &mEntry{version: ver, hd: now.Add(hardTTL)}
				if mode == ClassNone {
					ne.value = fmt.Sprintf("%s@%d", key, ver)
					ne.soft = now.Add(positiveTTL)
					failures[key] = 0
					if err != nil || v != ne.value {
						t.Fatalf("step %d key %s: want fresh success %q, got %q, %v", step, key, ne.value, v, err)
					}
				} else {
					ne.neg = true
					ne.class = mode
					if mode == ClassTransient {
						failures[key]++
					}
					ne.soft = now.Add(negTTL(key, mode))
					var se *SourceError
					if !errors.As(err, &se) || se.Class != mode {
						t.Fatalf("step %d key %s: want fresh %s, got %v", step, key, mode, err)
					}
				}
				model[key] = ne
				// The cache must agree on the committed entry and expiries.
				info, ok := c.Inspect("t", key)
				if !ok || info.Negative != ne.neg || info.Class != ne.class || info.Version != ne.version {
					t.Fatalf("step %d key %s: cache %+v, model %+v", step, key, info, ne)
				}
				if !info.SoftExpiry.Equal(ne.soft) || !info.HardExpiry.Equal(ne.hd) {
					t.Fatalf("step %d key %s: expiry %v/%v, model %v/%v",
						step, key, info.SoftExpiry, info.HardExpiry, ne.soft, ne.hd)
				}
			}
		}
	}
}
