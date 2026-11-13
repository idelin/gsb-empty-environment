package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type replayEntropy struct {
	mu        sync.Mutex
	seed      string
	calls     int
	failures  map[int]error
	shortRead map[int]bool
}

func newReplayEntropy(seed string) *replayEntropy {
	return &replayEntropy{
		seed:      seed,
		failures:  make(map[int]error),
		shortRead: make(map[int]bool),
	}
}

func (e *replayEntropy) Entropy(n int) ([]byte, error) {
	e.mu.Lock()
	e.calls++
	call := e.calls
	failure := e.failures[call]
	short := e.shortRead[call]
	seed := e.seed
	e.mu.Unlock()

	if failure != nil {
		return nil, failure
	}
	if short {
		return make([]byte, n-1), nil
	}
	return replayBytes(seed, call, n), nil
}

func (e *replayEntropy) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *replayEntropy) Fail(call int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.failures[call] = err
}

func (e *replayEntropy) Short(call int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.shortRead[call] = true
}

func (e *replayEntropy) Recover() {
	e.mu.Lock()
	defer e.mu.Unlock()
	clear(e.failures)
	clear(e.shortRead)
}

func replayBytes(seed string, call, n int) []byte {
	out := make([]byte, n)
	for off := 0; off < n; off += sha256.Size {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%d", seed, call, off)))
		copy(out[off:], sum[:])
	}
	return out
}

type gatedEntropy struct {
	mu      sync.Mutex
	seed    string
	calls   int
	gate    <-chan struct{}
	entered chan<- struct{}
}

func (e *gatedEntropy) Entropy(n int) ([]byte, error) {
	e.mu.Lock()
	e.calls++
	call := e.calls
	gate := e.gate
	entered := e.entered
	e.gate = nil
	e.entered = nil
	seed := e.seed
	e.mu.Unlock()

	if gate != nil {
		if entered != nil {
			entered <- struct{}{}
		}
		<-gate
	}
	return replayBytes(seed, call, n), nil
}

func (e *gatedEntropy) Arm(gate <-chan struct{}, entered chan<- struct{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.gate = gate
	e.entered = entered
}

func (e *gatedEntropy) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type generationBox struct {
	mu    sync.RWMutex
	value GenerationID
	calls atomic.Int64
}

func newGenerationBox(g GenerationID) *generationBox {
	return &generationBox{value: g}
}

func (b *generationBox) Get() GenerationID {
	b.calls.Add(1)
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.value
}

func (b *generationBox) Set(g GenerationID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.value = g
}

func (b *generationBox) Calls() int64 {
	return b.calls.Load()
}

type clockBox struct {
	mu    sync.RWMutex
	value int64
	calls atomic.Int64
}

func newClockBox(v int64) *clockBox {
	return &clockBox{value: v}
}

func (b *clockBox) Get() int64 {
	b.calls.Add(1)
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.value
}

func (b *clockBox) Now() int64 {
	return b.Get()
}

func (b *clockBox) Set(v int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.value = v
}

func (b *clockBox) Calls() int64 {
	return b.calls.Load()
}

func newSource(t *testing.T, ent EntropySource, gen func() GenerationID, clock Clock) *Source {
	t.Helper()
	src, err := New(Config{Entropy: ent, Generation: gen, Clock: clock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return src
}

func mustBytes(t *testing.T, src *Source, n int) []byte {
	t.Helper()
	b, err := src.Bytes(n)
	if err != nil {
		t.Fatalf("Bytes(%d): %v", n, err)
	}
	if len(b) != n {
		t.Fatalf("Bytes(%d) returned %d bytes", n, len(b))
	}
	return b
}

func TestConfigValidation(t *testing.T) {
	gen := func() GenerationID { return GenerationID{ProcessID: 7, VMGenerationID: "vm-1"} }
	clock := ClockFunc(func() int64 { return 1 })
	if _, err := New(Config{Generation: gen, Clock: clock}); !errors.Is(err, ErrConfig) {
		t.Fatalf("nil entropy error = %v, want ErrConfig", err)
	}
	if _, err := New(Config{Entropy: newReplayEntropy("x"), Clock: clock}); !errors.Is(err, ErrConfig) {
		t.Fatalf("nil generation error = %v, want ErrConfig", err)
	}
	if _, err := New(Config{Entropy: newReplayEntropy("x"), Generation: gen}); !errors.Is(err, ErrConfig) {
		t.Fatalf("nil clock error = %v, want ErrConfig", err)
	}

	src := newSource(t, newReplayEntropy("config"), gen, clock)
	if _, err := src.Bytes(-1); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("negative length error = %v, want ErrInvalidLength", err)
	}
	if b, err := src.Bytes(0); err != nil || len(b) != 0 {
		t.Fatalf("Bytes(0) = %v, %v", b, err)
	}
	if n, err := src.Read(nil); n != 0 || err != nil {
		t.Fatalf("Read(nil) = %d, %v; want 0, nil", n, err)
	}
}

func TestFunctionAdapters(t *testing.T) {
	ent := EntropyFunc(newReplayEntropy("adapters").Entropy)
	gen := newGenerationBox(GenerationID{ProcessID: 11, VMGenerationID: "vm-1"})
	clock := ClockFunc(newClockBox(42).Get)
	src := newSource(t, ent, gen.Get, clock)

	if n, err := src.Read(make([]byte, 0)); n != 0 || err != nil {
		t.Fatalf("zero-length read = %d, %v", n, err)
	}
	_ = mustBytes(t, src, 16)
}

func TestStableGenerationDoesNotReseed(t *testing.T) {
	ent := newReplayEntropy("stable")
	gen := newGenerationBox(GenerationID{ProcessID: 41, VMGenerationID: "vm-a"})
	clock := newClockBox(1000)
	src := newSource(t, ent, gen.Get, clock)

	a := mustBytes(t, src, 32)
	clock.Set(1010)
	b := mustBytes(t, src, 32)
	clock.Set(1020)
	c := mustBytes(t, src, 32)

	if got := ent.Calls(); got != 1 {
		t.Fatalf("entropy calls = %d, want 1", got)
	}
	if bytes.Equal(a, b) || bytes.Equal(b, c) || bytes.Equal(a, c) {
		t.Fatal("counter-mode outputs repeated under unchanged generation")
	}
	if gen.Calls() != 3 || clock.Calls() != 3 {
		t.Fatalf("generation/clock calls = %d/%d, want 3/3", gen.Calls(), clock.Calls())
	}
}

func TestGenerationChangeReseeds(t *testing.T) {
	cases := []struct {
		name   string
		before GenerationID
		after  GenerationID
	}{
		{"process id", GenerationID{ProcessID: 100, VMGenerationID: "vm-1"}, GenerationID{ProcessID: 101, VMGenerationID: "vm-1"}},
		{"vm generation", GenerationID{ProcessID: 100, VMGenerationID: "vm-1"}, GenerationID{ProcessID: 100, VMGenerationID: "vm-2"}},
		{"both", GenerationID{ProcessID: 100, VMGenerationID: "vm-1"}, GenerationID{ProcessID: 101, VMGenerationID: "vm-2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ent := newReplayEntropy("generation")
			gen := newGenerationBox(tc.before)
			clock := newClockBox(5000)
			src := newSource(t, ent, gen.Get, clock)

			first := mustBytes(t, src, 32)
			gen.Set(tc.after)
			after := mustBytes(t, src, 32)
			trailing := mustBytes(t, src, 32)

			if got := ent.Calls(); got != 2 {
				t.Fatalf("entropy calls = %d, want 2", got)
			}
			if bytes.Equal(first, after) {
				t.Fatal("generation change produced identical output")
			}

			gen.Set(tc.before)
			back := mustBytes(t, src, 32)
			if got := ent.Calls(); got != 3 {
				t.Fatalf("entropy calls after changing generation back = %d, want 3", got)
			}
			if bytes.Equal(trailing, back) {
				t.Fatal("generation change back produced identical output")
			}
		})
	}
}

func TestClockRegressionReseeds(t *testing.T) {
	ent := newReplayEntropy("clock")
	gen := newGenerationBox(GenerationID{ProcessID: 9, VMGenerationID: "vm-1"})
	clock := newClockBox(1000)
	src := newSource(t, ent, gen.Get, clock)

	first := mustBytes(t, src, 32)
	clock.Set(2000)
	advanced := mustBytes(t, src, 32)
	if got := ent.Calls(); got != 1 {
		t.Fatalf("advancing clock reseeded: entropy calls = %d, want 1", got)
	}

	clock.Set(1500)
	regressed := mustBytes(t, src, 32)
	if got := ent.Calls(); got != 2 {
		t.Fatalf("clock regression did not reseed: entropy calls = %d, want 2", got)
	}
	if bytes.Equal(advanced, regressed) || bytes.Equal(first, regressed) {
		t.Fatal("clock regression produced repeated output")
	}

	clock.Set(1500)
	equal := mustBytes(t, src, 32)
	clock.Set(1501)
	resumed := mustBytes(t, src, 32)
	if got := ent.Calls(); got != 2 {
		t.Fatalf("non-regressing clock reseeded: entropy calls = %d, want 2", got)
	}
	if bytes.Equal(equal, resumed) {
		t.Fatal("counter-mode outputs repeated")
	}
}

func TestEntropyFailureProducesNoOutputAndDiscardsBuffer(t *testing.T) {
	ent := newReplayEntropy("failure")
	gen := newGenerationBox(GenerationID{ProcessID: 77, VMGenerationID: "vm-1"})
	clock := newClockBox(7000)
	src := newSource(t, ent, gen.Get, clock)

	// Consume only part of a generated block so the Source holds buffered
	// bytes that must be thrown away by the next reseed.
	_ = mustBytes(t, src, 5)
	if src.bufLen != 11 {
		t.Fatalf("buffered bytes = %d, want 11", src.bufLen)
	}
	oldBuffered := append([]byte(nil), src.buf[src.bufOff:src.bufOff+src.bufLen]...)

	boom := errors.New("entropy unavailable")
	ent.Fail(2, boom)
	ent.Fail(3, boom)
	gen.Set(GenerationID{ProcessID: 78, VMGenerationID: "vm-1"})

	p := bytes.Repeat([]byte{0xAA}, 32)
	n, err := src.Read(p)
	if n != 0 {
		t.Fatalf("failed read returned %d bytes", n)
	}
	if !errors.Is(err, ErrEntropy) || !errors.Is(err, boom) {
		t.Fatalf("read error = %v, want ErrEntropy wrapping boom", err)
	}
	if !bytes.Equal(p, bytes.Repeat([]byte{0xAA}, 32)) {
		t.Fatal("failed read modified the output buffer")
	}
	if src.bufLen != 0 || src.bufOff != 0 {
		t.Fatalf("buffer not discarded on failure: off=%d len=%d", src.bufOff, src.bufLen)
	}
	if !src.needSeed {
		t.Fatal("source did not remain in need-seed state after failure")
	}

	// A second read retries the seed instead of falling back to old state.
	p2 := bytes.Repeat([]byte{0xBB}, 32)
	if n, err := src.Read(p2); n != 0 || !errors.Is(err, ErrEntropy) {
		t.Fatalf("second failed read = %d, %v; want 0, ErrEntropy", n, err)
	}
	if !bytes.Equal(p2, bytes.Repeat([]byte{0xBB}, 32)) {
		t.Fatal("second failed read modified the output buffer")
	}
	if got := ent.Calls(); got != 3 {
		t.Fatalf("entropy calls = %d, want 3", got)
	}

	ent.Recover()
	recovered := mustBytes(t, src, 32)
	if got := ent.Calls(); got != 4 {
		t.Fatalf("entropy calls after recovery = %d, want 4", got)
	}
	if bytes.HasPrefix(recovered, oldBuffered) {
		t.Fatal("recovery exposed bytes buffered before the reseed")
	}
}

func TestInitialAndShortEntropyFailures(t *testing.T) {
	gen := newGenerationBox(GenerationID{ProcessID: 5, VMGenerationID: "vm-1"})
	clock := newClockBox(10)

	t.Run("initial failure", func(t *testing.T) {
		ent := newReplayEntropy("initial-failure")
		ent.Fail(1, errors.New("no entropy"))
		src := newSource(t, ent, gen.Get, clock)
		p := bytes.Repeat([]byte{0xCC}, 16)
		if n, err := src.Read(p); n != 0 || !errors.Is(err, ErrEntropy) {
			t.Fatalf("initial read = %d, %v; want 0, ErrEntropy", n, err)
		}
		if !bytes.Equal(p, bytes.Repeat([]byte{0xCC}, 16)) {
			t.Fatal("initial failure modified output")
		}
		_ = mustBytes(t, src, 16)
		if got := ent.Calls(); got != 2 {
			t.Fatalf("entropy calls = %d, want 2", got)
		}
	})

	t.Run("short entropy", func(t *testing.T) {
		ent := newReplayEntropy("short")
		ent.Short(1)
		src := newSource(t, ent, gen.Get, clock)
		if _, err := src.Bytes(16); !errors.Is(err, ErrEntropy) {
			t.Fatalf("short entropy error = %v, want ErrEntropy", err)
		}
		_ = mustBytes(t, src, 16)
		if got := ent.Calls(); got != 2 {
			t.Fatalf("entropy calls = %d, want 2", got)
		}
	})
}

func TestCounterExhaustionForcesReseed(t *testing.T) {
	ent := newReplayEntropy("counter")
	gen := newGenerationBox(GenerationID{ProcessID: 13, VMGenerationID: "vm-1"})
	clock := newClockBox(100)
	src := newSource(t, ent, gen.Get, clock)
	_ = mustBytes(t, src, 16)
	if got := ent.Calls(); got != 1 {
		t.Fatalf("entropy calls = %d, want 1", got)
	}

	src.discardBufferLocked()
	src.counter = ^uint64(0)
	if n, err := src.Read(make([]byte, 16)); n != 0 || !errors.Is(err, ErrCounterExhausted) {
		t.Fatalf("exhausted read = %d, %v; want 0, ErrCounterExhausted", n, err)
	}
	if !src.needSeed {
		t.Fatal("counter exhaustion did not force a reseed")
	}
	_ = mustBytes(t, src, 16)
	if got := ent.Calls(); got != 2 {
		t.Fatalf("entropy calls after recovery = %d, want 2", got)
	}
}

func TestConcurrentReadersBlockDuringReseed(t *testing.T) {
	ent := &gatedEntropy{seed: "gated"}
	gen := newGenerationBox(GenerationID{ProcessID: 300, VMGenerationID: "vm-1"})
	clock := newClockBox(9000)
	src := newSource(t, ent, gen.Get, clock)
	_ = mustBytes(t, src, 16)

	const workers = 24
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	ent.Arm(gate, entered)
	gen.Set(GenerationID{ProcessID: 301, VMGenerationID: "vm-1"})

	var completed atomic.Int64
	results := make(chan []byte, workers+1)
	errs := make(chan error, workers+1)
	triggerDone := make(chan struct{})

	go func() {
		defer close(triggerDone)
		b, err := src.Bytes(32)
		completed.Add(1)
		if err != nil {
			errs <- err
			return
		}
		results <- b
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reseed did not reach the entropy source")
	}

	started := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started <- struct{}{}
			b, err := src.Bytes(32)
			completed.Add(1)
			if err != nil {
				errs <- err
				return
			}
			results <- b
		}()
	}
	for i := 0; i < workers; i++ {
		<-started
	}

	select {
	case <-triggerDone:
		t.Fatal("reader completed while reseed was blocked")
	case <-time.After(50 * time.Millisecond):
	}
	if got := completed.Load(); got != 0 {
		t.Fatalf("%d readers completed while reseed was blocked", got)
	}

	close(gate)
	<-triggerDone
	wg.Wait()
	close(results)
	close(errs)

	if len(errs) != 0 {
		t.Fatalf("concurrent reads failed: %v", <-errs)
	}
	if got := completed.Load(); got != workers+1 {
		t.Fatalf("completed reads = %d, want %d", got, workers+1)
	}

	seen := make(map[string]bool, workers+1)
	count := 0
	for b := range results {
		count++
		key := string(b)
		if seen[key] {
			t.Fatal("duplicate output from concurrent reads")
		}
		seen[key] = true
	}
	if count != workers+1 {
		t.Fatalf("collected %d outputs, want %d", count, workers+1)
	}
	if got := ent.Calls(); got != 2 {
		t.Fatalf("entropy calls = %d, want 2 (one initial seed and one reseed)", got)
	}
}

func TestConcurrentDrawsAreUnique(t *testing.T) {
	ent := newReplayEntropy("concurrent")
	gen := newGenerationBox(GenerationID{ProcessID: 400, VMGenerationID: "vm-1"})
	clock := newClockBox(12000)
	src := newSource(t, ent, gen.Get, clock)

	const (
		workers = 32
		perCall = 64
		size    = 24
	)
	outputs := make(chan []byte, workers*perCall)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perCall; j++ {
				b, err := src.Bytes(size)
				if err != nil {
					t.Errorf("Bytes: %v", err)
					return
				}
				outputs <- b
			}
		}()
	}
	wg.Wait()
	close(outputs)

	seen := make(map[string]bool, workers*perCall)
	count := 0
	for b := range outputs {
		count++
		key := string(b)
		if seen[key] {
			t.Fatalf("duplicate %d-byte output among concurrent draws", size)
		}
		seen[key] = true
	}
	if count != workers*perCall {
		t.Fatalf("collected %d outputs, want %d", count, workers*perCall)
	}
}

func TestClonesWithSharedEntropyReplayDiverge(t *testing.T) {
	cases := []struct {
		name   string
		cloneA GenerationID
		cloneB GenerationID
	}{
		{
			name:   "process id differs",
			cloneA: GenerationID{ProcessID: 500, VMGenerationID: "vm-snapshot"},
			cloneB: GenerationID{ProcessID: 501, VMGenerationID: "vm-snapshot"},
		},
		{
			name:   "vm generation differs",
			cloneA: GenerationID{ProcessID: 500, VMGenerationID: "vm-clone-a"},
			cloneB: GenerationID{ProcessID: 500, VMGenerationID: "vm-clone-b"},
		},
		{
			name:   "both differ",
			cloneA: GenerationID{ProcessID: 500, VMGenerationID: "vm-clone-a"},
			cloneB: GenerationID{ProcessID: 501, VMGenerationID: "vm-clone-b"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both clones start with the same replayed entropy stream, the
			// same generator state, and the same clock reading.
			entA := newReplayEntropy("shared-replay")
			entB := newReplayEntropy("shared-replay")
			initial := GenerationID{ProcessID: 499, VMGenerationID: "vm-snapshot"}
			genA := newGenerationBox(initial)
			genB := newGenerationBox(initial)
			clockA := newClockBox(20000)
			clockB := newClockBox(20000)
			srcA := newSource(t, entA, genA.Get, clockA)
			srcB := newSource(t, entB, genB.Get, clockB)

			preA := mustBytes(t, srcA, 32)
			preB := mustBytes(t, srcB, 32)
			if !bytes.Equal(preA, preB) {
				t.Fatal("test setup: clones did not share the same pre-fork output")
			}

			// Simulate the first read on each side after a snapshot restore or
			// fork. The entropy replay returns the same bytes to both clones.
			genA.Set(tc.cloneA)
			genB.Set(tc.cloneB)
			firstA := mustBytes(t, srcA, 32)
			firstB := mustBytes(t, srcB, 32)
			if bytes.Equal(firstA, firstB) {
				t.Fatal("clone outputs matched after fork")
			}
			if entA.Calls() != 2 || entB.Calls() != 2 {
				t.Fatalf("entropy calls = %d/%d, want 2/2", entA.Calls(), entB.Calls())
			}

			const tokens = 256
			setA := make(map[string]bool, tokens)
			setB := make(map[string]bool, tokens)
			setA[string(firstA)] = true
			setB[string(firstB)] = true
			for i := 0; i < tokens-1; i++ {
				b := mustBytes(t, srcA, 32)
				if setA[string(b)] {
					t.Fatal("duplicate output within clone A")
				}
				setA[string(b)] = true
			}
			for i := 0; i < tokens-1; i++ {
				b := mustBytes(t, srcB, 32)
				if setB[string(b)] {
					t.Fatal("duplicate output within clone B")
				}
				setB[string(b)] = true
			}
			for b := range setA {
				if setB[b] {
					t.Fatal("outputs of the two clones overlap")
				}
			}
		})
	}
}
