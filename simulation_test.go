package idemexec

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

// simEnv is a crashable deployment of the executor: one durable store,
// one injector, and a fake clock. Restarting produces a fresh "process"
// (new store facade, new executor) over the same durable state.
type simEnv struct {
	inj   *Injector
	ttl   time.Duration
	mu    sync.Mutex
	store *MemStore
	now   time.Time
}

func newSimEnv(ttl time.Duration) *simEnv {
	inj := NewInjector()
	return &simEnv{
		inj:   inj,
		ttl:   ttl,
		store: NewMemStore(inj),
		now:   time.Unix(1000, 0),
	}
}

func (env *simEnv) restart() {
	env.mu.Lock()
	env.store = env.store.Restart()
	env.mu.Unlock()
}

func (env *simEnv) executor() *Executor {
	env.mu.Lock()
	store := env.store
	env.mu.Unlock()
	e := NewExecutor(store, env.ttl)
	e.SetClock(func() time.Time {
		env.mu.Lock()
		defer env.mu.Unlock()
		return env.now
	})
	return e
}

func (env *simEnv) advance(d time.Duration) {
	env.mu.Lock()
	env.now = env.now.Add(d)
	env.mu.Unlock()
}

func (env *simEnv) effectCount(t *testing.T) int {
	t.Helper()
	n := 0
	err := env.executor().store.View(func(tx Tx) error {
		if v, ok := tx.EffectGet("counters", "execs"); ok {
			n, _ = strconv.Atoi(string(v))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// tryOp runs op, converting a simulated crash into crashed=true. Any other
// panic is a real bug and is re-raised.
func tryOp(e *Executor, op func(e *Executor) error) (err error, crashed bool) {
	defer func() {
		if v := recover(); v != nil {
			if IsCrash(v) {
				crashed = true
				return
			}
			panic(v)
		}
	}()
	err = op(e)
	return err, false
}

// runOp models a client that lost the receipt and retries: on a simulated
// crash the process restarts and op runs again, until it completes without
// crashing. The returned error is op's final business outcome.
func (env *simEnv) runOp(op func(e *Executor) error) error {
	for {
		err, crashed := tryOp(env.executor(), op)
		if crashed {
			env.restart()
			continue
		}
		return err
	}
}

// crashScenario is the full request lifecycle used for cut-point
// enumeration: execute (receipt may be lost), retry, expire, sweep, then
// verify the tombstone forbids replay forever.
func crashScenario(t *testing.T, env *simEnv) {
	t.Helper()
	const key = "order-123"
	params := []byte(`{"sku":"A","qty":2}`)

	// First attempt: the commit may crash before or after applying, and
	// the receipt may be lost. runOp restarts and retries until a
	// receipt is delivered.
	var first []byte
	if err := env.runOp(func(e *Executor) error {
		var err error
		first, err = e.Do(key, params, countingEffect)
		return err
	}); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if string(first) != "result-1" {
		t.Fatalf("first receipt = %q, want result-1", first)
	}

	// The client retries the identical request (it cannot know whether
	// the first attempt committed): it must get the same receipt back
	// without re-executing the effect.
	var second []byte
	if err := env.runOp(func(e *Executor) error {
		var err error
		second, err = e.Do(key, params, countingEffect)
		return err
	}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if string(second) != string(first) {
		t.Fatalf("retry receipt = %q, want cached %q", second, first)
	}

	// Expire the result and sweep; the sweep commit may itself crash.
	env.advance(2 * env.ttl)
	if err := env.runOp(func(e *Executor) error {
		_, err := e.Sweep()
		return err
	}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	// Replay is permanently forbidden, for matching and mismatched
	// parameters alike.
	if err := env.runOp(func(e *Executor) error {
		_, err := e.Do(key, params, countingEffect)
		return err
	}); err != ErrTombstoned {
		t.Fatalf("replay after expiry: got %v, want ErrTombstoned", err)
	}
	if err := env.runOp(func(e *Executor) error {
		_, err := e.Do(key, []byte(`{"sku":"B","qty":1}`), countingEffect)
		return err
	}); err != ErrParamsMismatch {
		t.Fatalf("mismatched replay: got %v, want ErrParamsMismatch", err)
	}

	// Invariants: exactly-once effect, clean tombstone, stable cleanup.
	if n := env.effectCount(t); n != 1 {
		t.Fatalf("effect applied %d times, want exactly once", n)
	}
	rec, found, err := env.executor().Lookup(key)
	if err != nil || !found {
		t.Fatalf("tombstone missing: found=%v err=%v", found, err)
	}
	if rec.State != StateTombstone || rec.Result != nil {
		t.Fatalf("record = %+v, want clean tombstone", rec)
	}
	swept := 0
	if err := env.runOp(func(e *Executor) error {
		var err error
		swept, err = e.Sweep()
		return err
	}); err != nil || swept != 0 {
		t.Fatalf("repeat sweep: swept=%d err=%v, want 0", swept, err)
	}
	if _, found, _ := env.executor().Lookup(key); !found {
		t.Fatal("sweep deleted the tombstone")
	}
}

// TestCutPointEnumeration enumerates the persistence cut points of the
// whole request lifecycle and, for each one, crashes there, restarts, and
// verifies exactly-once semantics, receipt stability, and cleanup.
func TestCutPointEnumeration(t *testing.T) {
	// Probe run with the injector disarmed to learn how many cut points
	// the lifecycle traverses.
	probe := newSimEnv(time.Minute)
	crashScenario(t, probe)
	total := probe.inj.Hits()
	if total == 0 {
		t.Fatal("scenario traversed no persistence cut points")
	}

	for crashAt := 1; crashAt <= total; crashAt++ {
		crashAt := crashAt
		t.Run(fmt.Sprintf("crash@%d", crashAt), func(t *testing.T) {
			env := newSimEnv(time.Minute)
			env.inj.Arm(crashAt)
			crashScenario(t, env)
			if env.inj.Hits() < crashAt {
				t.Fatalf("cut point %d never reached (only %d hits)", crashAt, env.inj.Hits())
			}
		})
	}
}

// TestConcurrentDuplicates hammers one key from many goroutines and
// requires exactly one effect and one agreed receipt.
func TestConcurrentDuplicates(t *testing.T) {
	env := newSimEnv(0)
	const n = 32
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := env.executor().Do("k", []byte("p"), countingEffect)
			if err == nil {
				results[i] = string(r)
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i] != "result-1" {
			t.Fatalf("goroutine %d: receipt %q, want result-1", i, results[i])
		}
	}
	if got := env.effectCount(t); got != 1 {
		t.Fatalf("effect applied %d times, want exactly once", got)
	}
}

// TestConcurrentMismatchedParams races two parameter sets for one key:
// exactly one may commit, every loser must be rejected, and the effect
// must still run exactly once.
func TestConcurrentMismatchedParams(t *testing.T) {
	env := newSimEnv(0)
	const n = 32
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			params := []byte("params-a")
			if i%2 == 1 {
				params = []byte("params-b")
			}
			_, errs[i] = env.executor().Do("k", params, countingEffect)
		}(i)
	}
	wg.Wait()
	succeeded, rejected := 0, 0
	for i := 0; i < n; i++ {
		switch errs[i] {
		case nil:
			succeeded++
		case ErrParamsMismatch:
			rejected++
		default:
			t.Fatalf("goroutine %d: unexpected error %v", i, errs[i])
		}
	}
	if succeeded == 0 || rejected == 0 {
		t.Fatalf("succeeded=%d rejected=%d, want both > 0", succeeded, rejected)
	}
	if got := env.effectCount(t); got != 1 {
		t.Fatalf("effect applied %d times, want exactly once", got)
	}
}

// TestConcurrentDuplicatesWithCrash loses a receipt mid-stampede: every
// goroutine retries until it gets a receipt, and all must agree.
func TestConcurrentDuplicatesWithCrash(t *testing.T) {
	env := newSimEnv(0)
	env.inj.Arm(1) // crash the very first commit attempt
	const n = 16
	results := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = env.runOp(func(e *Executor) error {
				r, err := e.Do("k", []byte("p"), countingEffect)
				if err == nil {
					results[i] = string(r)
				}
				return err
			})
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i] != "result-1" {
			t.Fatalf("goroutine %d: receipt %q, want result-1", i, results[i])
		}
	}
	if got := env.effectCount(t); got != 1 {
		t.Fatalf("effect applied %d times, want exactly once", got)
	}
}
