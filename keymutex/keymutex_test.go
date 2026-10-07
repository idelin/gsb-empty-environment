package keymutex

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"
)

// activeSetChecker tracks the set of currently held keys and fails the
// test if a key is ever entered twice or released inconsistently.
type activeSetChecker struct {
	t    *testing.T
	mu   sync.Mutex
	held map[string]Token
}

func newActiveSetChecker(t *testing.T) *activeSetChecker {
	t.Helper()
	return &activeSetChecker{t: t, held: make(map[string]Token)}
}

func (c *activeSetChecker) enter(tok Token) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, ok := c.held[tok.key]; ok {
		c.t.Errorf("key %q entered concurrently: gen %d token and gen %d token",
			tok.key, prev.gen, tok.gen)
		return
	}
	c.held[tok.key] = tok
}

func (c *activeSetChecker) exit(tok Token) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.held[tok.key]
	if !ok {
		c.t.Errorf("key %q released without being held", tok.key)
		return
	}
	if cur != tok {
		c.t.Errorf("key %q released with mismatched token (held gen %d, got gen %d)",
			tok.key, cur.gen, tok.gen)
		return
	}
	delete(c.held, tok.key)
}

func (c *activeSetChecker) empty() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.held) == 0
}

// barrier is a reusable rendezvous for n goroutines.
type barrier struct {
	mu    sync.Mutex
	n     int
	count int
	ch    chan struct{}
}

func newBarrier(n int) *barrier {
	return &barrier{n: n, ch: make(chan struct{})}
}

func (b *barrier) await() {
	b.mu.Lock()
	b.count++
	if b.count == b.n {
		close(b.ch)
		b.ch = make(chan struct{})
		b.count = 0
		b.mu.Unlock()
		return
	}
	ch := b.ch
	b.mu.Unlock()
	<-ch
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
	t.Fatalf("timeout waiting for %s", what)
}

func mustPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic")
		}
	}()
	fn()
}

// TestExpandBlocksSameKeyAcrossGenerations: a holder from the old
// generation must block a new-generation acquirer of the same key until
// the old token is released.
func TestExpandBlocksSameKeyAcrossGenerations(t *testing.T) {
	m := New(1)
	checker := newActiveSetChecker(t)
	ctx := context.Background()

	tokA, err := m.Acquire(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	checker.enter(tokA)
	if got := tokA.Generation(); got != 1 {
		t.Fatalf("token generation = %d, want 1", got)
	}

	granted := make(chan Token, 1)
	go func() {
		tok, err := m.Acquire(ctx, "k")
		if err == nil {
			granted <- tok
		}
	}()
	waitFor(t, "waiter to queue", func() bool { return m.queued("k") == 1 })

	if gen := m.Expand(8); gen != 2 {
		t.Fatalf("Expand returned generation %d, want 2", gen)
	}

	select {
	case tok := <-granted:
		t.Fatalf("same key granted across generations while old holder active: %+v", tok)
	case <-time.After(50 * time.Millisecond):
	}

	checker.exit(tokA)
	m.Release(tokA) // gen-1 token released under the gen-2 table

	var tokB Token
	select {
	case tokB = <-granted:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter not granted after old holder released")
	}
	if got := tokB.Generation(); got != 2 {
		t.Fatalf("granted token generation = %d, want 2", got)
	}
	checker.enter(tokB)
	checker.exit(tokB)
	m.Release(tokB)
}

// TestAcquireReleaseDuringExpansion uses a barrier to overlap an
// in-flight Expand with the acquire and release of the same key.
func TestAcquireReleaseDuringExpansion(t *testing.T) {
	m := New(2)
	checker := newActiveSetChecker(t)
	ctx := context.Background()
	bar := newBarrier(2)

	// Goroutine A: acquires "k" in generation 1, holds it while the main
	// goroutine expands the table, then releases the old-generation token.
	done := make(chan struct{})
	go func() {
		defer close(done)
		tokA, err := m.Acquire(ctx, "k")
		if err != nil {
			t.Errorf("acquire: %v", err)
			return
		}
		checker.enter(tokA)
		bar.await() // A holds "k"; main may now expand
		bar.await() // wait for the expansion to finish
		checker.exit(tokA)
		m.Release(tokA)
	}()

	bar.await() // A holds "k"
	if gen := m.Expand(16); gen != 2 {
		t.Fatalf("Expand returned generation %d, want 2", gen)
	}
	// Queue a waiter on the same key while A still holds it.
	granted := make(chan Token, 1)
	go func() {
		tok, err := m.Acquire(ctx, "k")
		if err == nil {
			granted <- tok
		}
	}()
	waitFor(t, "waiter to queue", func() bool { return m.queued("k") == 1 })
	bar.await() // expansion done; A releases

	select {
	case tokB := <-granted:
		checker.enter(tokB)
		checker.exit(tokB)
		m.Release(tokB)
	case <-time.After(5 * time.Second):
		t.Fatal("waiter not granted after release during expansion")
	}
	<-done
}

// TestConsecutiveExpansions: two expansions back to back while a gen-1
// holder is active; FIFO waiters must be served in order afterwards, and
// a different key must proceed in parallel throughout.
func TestConsecutiveExpansions(t *testing.T) {
	m := New(1)
	checker := newActiveSetChecker(t)
	ctx := context.Background()

	tokA, err := m.Acquire(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	checker.enter(tokA)

	m.Expand(4)  // gen 2
	m.Expand(16) // gen 3, consecutive, gen-1 holder still active
	if got := m.Generation(); got != 3 {
		t.Fatalf("generation = %d, want 3", got)
	}

	// A different key is granted immediately even while "k" is held.
	tokOther, err := m.Acquire(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	checker.enter(tokOther)

	grantB := make(chan Token, 1)
	grantC := make(chan Token, 1)
	go func() {
		if tok, err := m.Acquire(ctx, "k"); err == nil {
			grantB <- tok
		}
	}()
	waitFor(t, "first waiter to queue", func() bool { return m.queued("k") == 1 })
	go func() {
		if tok, err := m.Acquire(ctx, "k"); err == nil {
			grantC <- tok
		}
	}()
	waitFor(t, "second waiter to queue", func() bool { return m.queued("k") == 2 })

	checker.exit(tokA)
	m.Release(tokA) // gen-1 token, two expansions later

	var tokB Token
	select {
	case tokB = <-grantB:
	case <-time.After(5 * time.Second):
		t.Fatal("first waiter not granted")
	}
	if got := tokB.Generation(); got != 3 {
		t.Fatalf("token generation = %d, want 3", got)
	}
	checker.enter(tokB)

	select {
	case tok := <-grantC:
		t.Fatalf("second waiter granted out of order: %+v", tok)
	case <-time.After(50 * time.Millisecond):
	}

	checker.exit(tokB)
	m.Release(tokB)

	var tokC Token
	select {
	case tokC = <-grantC:
	case <-time.After(5 * time.Second):
		t.Fatal("second waiter not granted")
	}
	checker.enter(tokC)
	checker.exit(tokC)
	m.Release(tokC)

	checker.exit(tokOther)
	m.Release(tokOther)

	if !checker.empty() {
		t.Fatal("checker not empty at end of test")
	}
}

// TestCancelOnlyAffectsWaiting: cancellation aborts a queued wait but
// never affects a granted token.
func TestCancelOnlyAffectsWaiting(t *testing.T) {
	m := New(1)
	checker := newActiveSetChecker(t)
	bg := context.Background()

	tokA, err := m.Acquire(bg, "k")
	if err != nil {
		t.Fatal(err)
	}
	checker.enter(tokA)

	ctx, cancel := context.WithCancel(bg)
	errCh := make(chan error, 1)
	go func() {
		_, err := m.Acquire(ctx, "k")
		errCh <- err
	}()
	waitFor(t, "waiter to queue", func() bool { return m.queued("k") == 1 })
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait returned %v, want context.Canceled", err)
	}
	waitFor(t, "waiter to leave the queue", func() bool { return m.queued("k") == 0 })

	// The cancelled waiter must not consume the next grant: a fresh
	// waiter is served when A releases.
	granted := make(chan Token, 1)
	go func() {
		if tok, err := m.Acquire(bg, "k"); err == nil {
			granted <- tok
		}
	}()
	waitFor(t, "new waiter to queue", func() bool { return m.queued("k") == 1 })
	checker.exit(tokA)
	m.Release(tokA)
	select {
	case tok := <-granted:
		checker.enter(tok)
		checker.exit(tok)
		m.Release(tok)
	case <-time.After(5 * time.Second):
		t.Fatal("grant was lost to a cancelled waiter")
	}

	// Cancelling a context after the grant must not affect the token.
	ctx2, cancel2 := context.WithCancel(bg)
	tokB, err := m.Acquire(ctx2, "k2")
	if err != nil {
		t.Fatal(err)
	}
	cancel2()
	checker.enter(tokB)
	checker.exit(tokB)
	m.Release(tokB)

	// A free key is granted immediately even to an already-cancelled
	// context: cancellation only affects waiting.
	ctx3, cancel3 := context.WithCancel(bg)
	cancel3()
	tokC, err := m.Acquire(ctx3, "k3")
	if err != nil {
		t.Fatalf("immediate grant with cancelled ctx failed: %v", err)
	}
	m.Release(tokC)
}

// TestCancelGrantRace hammers the race between cancellation and grant;
// either outcome is valid, but the key must never be leaked or doubly
// held.
func TestCancelGrantRace(t *testing.T) {
	m := New(1)
	checker := newActiveSetChecker(t)
	bg := context.Background()

	for i := 0; i < 2000; i++ {
		tokA, err := m.Acquire(bg, "k")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(bg)
		res := make(chan struct {
			tok Token
			err error
		}, 1)
		go func() {
			tok, err := m.Acquire(ctx, "k")
			res <- struct {
				tok Token
				err error
			}{tok, err}
		}()
		cancel()
		m.Release(tokA)
		r := <-res
		switch {
		case r.err == nil:
			checker.enter(r.tok)
			checker.exit(r.tok)
			m.Release(r.tok)
		case errors.Is(r.err, context.Canceled):
			// Lost the race; nothing to release.
		default:
			t.Fatalf("unexpected error: %v", r.err)
		}
	}
	if got := m.queued("k"); got != 0 {
		t.Fatalf("%d waiters leaked", got)
	}
}

// TestTokenBindsAcquisitionGeneration checks that a token keeps the
// generation it was acquired under and that releasing it after expansions
// is valid, while stale tokens panic.
func TestTokenBindsAcquisitionGeneration(t *testing.T) {
	m := New(2)
	bg := context.Background()

	tok1, err := m.Acquire(bg, "k")
	if err != nil {
		t.Fatal(err)
	}
	m.Expand(8)  // gen 2
	m.Expand(32) // gen 3
	if got := tok1.Generation(); got != 1 {
		t.Fatalf("token generation = %d, want 1 (bound at acquisition)", got)
	}
	m.Release(tok1) // gen-1 token under the gen-3 table: valid

	tok3, err := m.Acquire(bg, "k")
	if err != nil {
		t.Fatal(err)
	}
	if got := tok3.Generation(); got != 3 {
		t.Fatalf("token generation = %d, want 3", got)
	}
	m.Release(tok3)

	mustPanic(t, func() { m.Release(tok1) })    // stale, already released
	mustPanic(t, func() { m.Release(Token{}) }) // zero token
}

// TestStressResharding runs many goroutines over a small key space with
// the checker watching for double-entry, while a background goroutine
// keeps expanding (and shrinking) the table, including consecutive
// expansions with no delay.
func TestStressResharding(t *testing.T) {
	m := New(2)
	checker := newActiveSetChecker(t)
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 400; i++ {
				key := keys[rng.Intn(len(keys))]
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				tok, err := m.Acquire(ctx, key)
				cancel()
				if err != nil {
					t.Errorf("acquire %q: %v", key, err)
					return
				}
				checker.enter(tok)
				runtime.Gosched()
				checker.exit(tok)
				m.Release(tok)
			}
		}(int64(g))
	}

	sizes := []int{3, 7, 1, 16, 4, 2, 8}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 21; i++ {
			m.Expand(sizes[i%len(sizes)])
			if i%3 == 0 {
				m.Expand(sizes[(i+1)%len(sizes)]) // consecutive expansion
			}
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()
	<-done

	if got := m.queued("a"); got != 0 {
		t.Fatalf("waiters leaked: %d", got)
	}
	if !checker.empty() {
		t.Fatal("checker not empty at end of stress test")
	}
}

// TestDifferentKeysParallel: many distinct keys can be held at once even
// with a single shard.
func TestDifferentKeysParallel(t *testing.T) {
	m := New(1)
	checker := newActiveSetChecker(t)
	bg := context.Background()

	const n = 64
	toks := make([]Token, n)
	for i := 0; i < n; i++ {
		tok, err := m.Acquire(bg, fmt.Sprintf("key-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		checker.enter(tok)
		toks[i] = tok
	}
	for _, tok := range toks {
		checker.exit(tok)
		m.Release(tok)
	}
}
