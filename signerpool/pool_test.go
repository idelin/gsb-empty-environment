package signerpool

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func assertLifecycle(t *testing.T, hsm *HSM, wantHandles int) {
	t.Helper()
	if got := hsm.DoubleDestroys(); got != 0 {
		t.Fatalf("double destroys = %d, want 0", got)
	}
	if got := hsm.UseAfterDestroy(); got != 0 {
		t.Fatalf("use-after-destroy = %d, want 0", got)
	}
	events := hsm.Events()
	generates, destroys := 0, 0
	perSerial := map[int]int{}
	destroyed := map[int]bool{}
	for _, e := range events {
		switch e.Op {
		case "generate":
			generates++
		case "destroy":
			destroys++
			perSerial[e.Serial]++
			if destroyed[e.Serial] {
				t.Fatalf("serial %d destroyed twice in event log", e.Serial)
			}
			destroyed[e.Serial] = true
		case "sign", "verify":
			if destroyed[e.Serial] {
				t.Fatalf("event %q on serial %d happened after its destroy", e.Op, e.Serial)
			}
		}
	}
	if generates != wantHandles {
		t.Fatalf("generate events = %d, want %d", generates, wantHandles)
	}
	if destroys != wantHandles {
		t.Fatalf("destroy events = %d, want %d", destroys, wantHandles)
	}
	for serial, count := range perSerial {
		if count != 1 {
			t.Fatalf("serial %d destroyed %d times, want 1", serial, count)
		}
	}
}

func mustRotate(t *testing.T, p *Pool) int {
	t.Helper()
	gen, err := p.Rotate()
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	return gen
}

// In-flight operations must keep using the generation they started on, and
// the retired handle must survive until every one of them has finished.
func TestRotatePinsInflightGeneration(t *testing.T) {
	hsm := NewHSM()
	pool := NewPool(hsm)
	g1 := mustRotate(t, pool)

	const n = 25
	hsm.Pause()

	var wg sync.WaitGroup
	type result struct {
		sig Signature
		msg []byte
		err error
	}
	results := make([]result, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg := []byte(fmt.Sprintf("msg-%d", i))
			sig, err := pool.Sign(msg)
			results[i] = result{sig: sig, msg: msg, err: err}
		}(i)
	}
	hsm.WaitBlocked(n)

	var (
		g2      int
		rotErr  error
		rotDone = make(chan struct{})
	)
	go func() {
		g2, rotErr = pool.Rotate()
		close(rotDone)
	}()
	hsm.WaitBlocked(n + 1) // n pinned signs + self-test sign blocked behind them

	select {
	case <-rotDone:
		t.Fatal("rotation completed while the HSM was paused")
	default:
	}
	if got := pool.CurrentGeneration(); got != g1 {
		t.Fatalf("generation switched to %d before rotation finished, want %d", got, g1)
	}

	hsm.Resume()
	wg.Wait()
	<-rotDone
	if rotErr != nil {
		t.Fatalf("Rotate after resume: %v", rotErr)
	}
	if g2 != g1+1 {
		t.Fatalf("new generation = %d, want %d", g2, g1+1)
	}

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("sign %d: %v", i, r.err)
		}
		if r.sig.Generation != g1 {
			t.Fatalf("sign %d used generation %d, want pinned %d", i, r.sig.Generation, g1)
		}
		if !pool.Verify(r.msg, r.sig) {
			t.Fatalf("sign %d failed verification", i)
		}
	}
	if got := pool.CurrentGeneration(); got != g2 {
		t.Fatalf("current generation = %d, want %d", got, g2)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertLifecycle(t, hsm, 2)
}

// Two rotations in a row must retire each old generation, keep signatures
// tied to their own generation, and still verify retired generations.
func TestTwoConsecutiveRotations(t *testing.T) {
	hsm := NewHSM()
	pool := NewPool(hsm)

	g1 := mustRotate(t, pool)
	sig1, err := pool.Sign([]byte("one"))
	if err != nil {
		t.Fatalf("Sign g1: %v", err)
	}
	if sig1.Generation != g1 {
		t.Fatalf("sig1 generation = %d, want %d", sig1.Generation, g1)
	}

	g2 := mustRotate(t, pool)
	sig2, err := pool.Sign([]byte("two"))
	if err != nil {
		t.Fatalf("Sign g2: %v", err)
	}
	if sig2.Generation != g2 {
		t.Fatalf("sig2 generation = %d, want %d", sig2.Generation, g2)
	}

	g3 := mustRotate(t, pool)
	sig3, err := pool.Sign([]byte("three"))
	if err != nil {
		t.Fatalf("Sign g3: %v", err)
	}
	if sig3.Generation != g3 {
		t.Fatalf("sig3 generation = %d, want %d", sig3.Generation, g3)
	}

	if g1+1 != g2 || g2+1 != g3 {
		t.Fatalf("generations not monotonic: %d %d %d", g1, g2, g3)
	}
	// Every signature verifies against its own pinned generation, including
	// generations whose hardware handles were destroyed on retirement.
	for _, tc := range []struct {
		msg string
		sig Signature
	}{
		{"one", sig1}, {"two", sig2}, {"three", sig3},
	} {
		if !pool.Verify([]byte(tc.msg), tc.sig) {
			t.Fatalf("verification failed for generation %d", tc.sig.Generation)
		}
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertLifecycle(t, hsm, 3)
}

// A failed self-test must roll back: the candidate handle is destroyed once,
// the serving generation stays installed, and a later retry succeeds.
func TestFailedRotationRollsBackAndRetries(t *testing.T) {
	t.Run("sign self-test failure", func(t *testing.T) {
		hsm := NewHSM()
		pool := NewPool(hsm)
		g1 := mustRotate(t, pool)

		hsm.FailSignAtGeneration(g1 + 1)
		if _, err := pool.Rotate(); !errors.Is(err, ErrSelfTest) {
			t.Fatalf("Rotate err = %v, want ErrSelfTest", err)
		}
		if got := pool.CurrentGeneration(); got != g1 {
			t.Fatalf("generation changed to %d after failed rotate, want %d", got, g1)
		}

		hsm.FailSignAtGeneration(-1)
		g2 := mustRotate(t, pool) // retry on a fresh generation number
		if g2 != g1+2 {
			t.Fatalf("retry generation = %d, want %d (failed number stays skipped)", g2, g1+2)
		}
		sig, err := pool.Sign([]byte("after-retry"))
		if err != nil {
			t.Fatalf("Sign after retry: %v", err)
		}
		if sig.Generation != g2 || !pool.Verify([]byte("after-retry"), sig) {
			t.Fatal("retry generation not serving traffic correctly")
		}

		if err := pool.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		assertLifecycle(t, hsm, 3) // g1 handle, rolled-back candidate, g2 handle
	})

	t.Run("verify self-test failure", func(t *testing.T) {
		hsm := NewHSM()
		pool := NewPool(hsm)
		g1 := mustRotate(t, pool)

		hsm.FailVerifies(1)
		if _, err := pool.Rotate(); !errors.Is(err, ErrSelfTest) {
			t.Fatalf("Rotate err = %v, want ErrSelfTest", err)
		}
		if got := pool.CurrentGeneration(); got != g1 {
			t.Fatalf("generation changed to %d after failed rotate, want %d", got, g1)
		}

		g2 := mustRotate(t, pool)
		sig, err := pool.Sign([]byte("verified-retry"))
		if err != nil {
			t.Fatalf("Sign after retry: %v", err)
		}
		if sig.Generation != g2 || !pool.Verify([]byte("verified-retry"), sig) {
			t.Fatal("retry generation not serving traffic correctly")
		}

		if err := pool.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		assertLifecycle(t, hsm, 3) // g1 handle, rolled-back candidate, g2 handle
	})
}

// Close racing a burst of signs must never double-destroy the handle nor sign
// on a destroyed one; repeated Close calls stay idempotent.
func TestConcurrentCloseUnderSignLoad(t *testing.T) {
	hsm := NewHSM()
	pool := NewPool(hsm)
	g1 := mustRotate(t, pool)

	const n = 200
	var (
		wg       sync.WaitGroup
		ok       atomic.Int64
		rejected atomic.Int64
		mu       sync.Mutex
		sigs     []Signature
		msgs     [][]byte
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			msg := []byte(fmt.Sprintf("load-%d", i))
			sig, err := pool.Sign(msg)
			if errors.Is(err, ErrPoolClosed) {
				rejected.Add(1)
				return
			}
			if err != nil {
				t.Errorf("Sign: %v", err)
				return
			}
			if sig.Generation != g1 {
				t.Errorf("sign used generation %d, want %d", sig.Generation, g1)
			}
			mu.Lock()
			sigs = append(sigs, sig)
			msgs = append(msgs, msg)
			mu.Unlock()
			ok.Add(1)
		}(i)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	wg.Wait()

	if ok.Load()+rejected.Load() != n {
		t.Fatalf("accounted ops = %d, want %d", ok.Load()+rejected.Load(), n)
	}
	for i, sig := range sigs {
		if !pool.Verify(msgs[i], sig) {
			t.Fatalf("verification failed for sign %d", i)
		}
	}
	if _, err := pool.Sign([]byte("post-close")); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("post-close Sign err = %v, want ErrPoolClosed", err)
	}
	assertLifecycle(t, hsm, 1)
}

func TestSignBeforeFirstRotate(t *testing.T) {
	pool := NewPool(NewHSM())
	if _, err := pool.Sign([]byte("x")); !errors.Is(err, ErrNoSigner) {
		t.Fatalf("err = %v, want ErrNoSigner", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close empty pool: %v", err)
	}
}

// Concurrent rotations must publish unique, monotonic generations and retire
// every superseded handle exactly once.
func TestConcurrentRotations(t *testing.T) {
	hsm := NewHSM()
	pool := NewPool(hsm)

	const rotations = 8
	type rotResult struct {
		gen int
		err error
	}
	gens := make(chan rotResult, rotations)
	var wg sync.WaitGroup
	for i := 0; i < rotations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gen, err := pool.Rotate()
			if err != nil && !errors.Is(err, ErrStaleRotation) {
				t.Errorf("Rotate: %v", err)
				return
			}
			gens <- rotResult{gen: gen, err: err}
		}()
	}
	wg.Wait()
	close(gens)

	committed := 0
	seen := map[int]bool{}
	var max int
	stale := 0
	for r := range gens {
		if errors.Is(r.err, ErrStaleRotation) {
			stale++
			continue
		}
		gen := r.gen
		committed++
		if seen[gen] {
			t.Fatalf("generation %d published twice", gen)
		}
		seen[gen] = true
		if gen > max {
			max = gen
		}
	}
	if committed+stale != rotations || max != rotations || committed != len(seen) {
		t.Fatalf("committed=%d stale=%d max=%d, want all %d rotations accounted for",
			committed, stale, max, rotations)
	}

	sig, err := pool.Sign([]byte("concurrent"))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !pool.Verify([]byte("concurrent"), sig) {
		t.Fatal("committed generation is not serving traffic")
	}
	if sig.Generation != pool.CurrentGeneration() || sig.Generation != max {
		t.Fatalf("current generation = %d, want newest %d", pool.CurrentGeneration(), max)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	assertLifecycle(t, hsm, rotations) // committed handles retired; stale ones self-destroyed
}
