package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// scriptedStep is a metered, deterministic StepDecompressor used to drive the
// wrapper through every failure path without a real compression library.
type scriptedStep struct {
	chunks   [][]byte
	next     int
	failStep int
	failing  bool
	trailing bool
	onStep   func(step int)

	steps    int
	sawTrail bool
}

func (s *scriptedStep) Step(ctx context.Context) ([]byte, bool, error) {
	if s.onStep != nil {
		s.onStep(s.steps)
	}
	step := s.steps
	s.steps++
	if s.failing && step == s.failStep {
		return nil, false, errors.New("scripted corrupt frame")
	}
	if s.next >= len(s.chunks) {
		return nil, false, errors.New("scripted engine called after stream end")
	}
	chunk := s.chunks[s.next]
	s.next++
	return chunk, s.next == len(s.chunks), nil
}

func (s *scriptedStep) Trailing() bool {
	s.sawTrail = true
	return s.trailing
}

type scriptedFactory struct {
	algorithm uint16
	build     func(input []byte) *scriptedStep

	mu       sync.Mutex
	calls    int
	lastIn   []byte
	lastStep *scriptedStep
}

func (f *scriptedFactory) Algorithm() uint16 { return f.algorithm }

func (f *scriptedFactory) New(input []byte) (StepDecompressor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastIn = append([]byte(nil), input...)
	s := f.build(input)
	f.lastStep = s
	return s, nil
}

func chunksOf(data string, size int) [][]byte {
	var out [][]byte
	for i := 0; i < len(data); i += size {
		end := i + size
		if end > len(data) {
			end = len(data)
		}
		out = append(out, []byte(data[i:end]))
	}
	return out
}

type parseRecord struct {
	mu        sync.Mutex
	calls     int
	algorithm uint16
	chains    [][]byte
	err       error
}

func (p *parseRecord) cb(algorithm uint16, chain []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.algorithm = algorithm
	p.chains = append(p.chains, append([]byte(nil), chain...))
	return p.err
}

func (p *parseRecord) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func testLimits() Limits {
	return Limits{MaxDeclaredLength: 64, MaxOutput: 64, MaxSteps: 8}
}

func TestNewWrapperRejectsInvalidConfig(t *testing.T) {
	good := Limits{MaxDeclaredLength: 16, MaxOutput: 16, MaxSteps: 4}
	noop := func([]byte) *scriptedStep { return &scriptedStep{} }

	cases := []struct {
		name   string
		limits Limits
		decs   []Decompressor
	}{
		{"zero declared", Limits{MaxOutput: 16, MaxSteps: 4}, nil},
		{"zero output", Limits{MaxDeclaredLength: 16, MaxSteps: 4}, nil},
		{"zero steps", Limits{MaxDeclaredLength: 16, MaxOutput: 16}, nil},
		{"no algorithms", good, nil},
		{"nil entry", good, []Decompressor{nil}},
		{"duplicate id", good, []Decompressor{
			&scriptedFactory{algorithm: AlgZlib, build: noop},
			&scriptedFactory{algorithm: AlgZlib, build: noop},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewWrapper(tc.limits, tc.decs...); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestDecompressHappyPath(t *testing.T) {
	chain := "-----full-cert-chain-----"
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: chunksOf(chain, 7)} },
	}
	wrapper, err := NewWrapper(testLimits(), factory)
	if err != nil {
		t.Fatalf("NewWrapper: %v", err)
	}

	record := &parseRecord{}
	compressed := []byte("small-payload")
	if err := wrapper.Decompress(context.Background(), AlgZlib, len(chain), compressed, record.cb); err != nil {
		t.Fatalf("Decompress: %v", err)
	}
	if record.callCount() != 1 {
		t.Fatalf("parse callback called %d times, want 1", record.callCount())
	}
	if record.algorithm != AlgZlib {
		t.Fatalf("callback algorithm = %d, want %d", record.algorithm, AlgZlib)
	}
	if got := string(record.chains[0]); got != chain {
		t.Fatalf("callback chain = %q, want %q", got, chain)
	}
	if factory.calls != 1 || string(factory.lastIn) != string(compressed) {
		t.Fatalf("factory got %d calls with %q, want 1 with %q", factory.calls, factory.lastIn, compressed)
	}
	wantSteps := len(chunksOf(chain, 7))
	if factory.lastStep.steps != wantSteps {
		t.Fatalf("engine steps = %d, want %d", factory.lastStep.steps, wantSteps)
	}
	if !factory.lastStep.sawTrail {
		t.Fatal("Trailing must be consulted once the stream ends")
	}
}

func TestDecompressRejectsUnknownAlgorithm(t *testing.T) {
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: [][]byte{[]byte("x")}} },
	}
	wrapper, _ := NewWrapper(testLimits(), factory)
	record := &parseRecord{}

	err := wrapper.Decompress(context.Background(), AlgBrotli, 1, []byte("z"), record.cb)
	if !errors.Is(err, ErrAlgorithmNotNegotiated) {
		t.Fatalf("expected ErrAlgorithmNotNegotiated, got %v", err)
	}
	if record.callCount() != 0 || factory.calls != 0 {
		t.Fatal("unknown algorithm must not reach the engine or parser")
	}
}

func TestDecompressRejectsDeclaredLength(t *testing.T) {
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build:     func([]byte) *scriptedStep { return &scriptedStep{} },
	}
	wrapper, _ := NewWrapper(testLimits(), factory)
	record := &parseRecord{}

	for _, declared := range []int{0, -1, testLimits().MaxDeclaredLength + 1} {
		err := wrapper.Decompress(context.Background(), AlgZlib, declared, []byte("z"), record.cb)
		if !errors.Is(err, ErrDeclaredLengthExceeded) {
			t.Fatalf("declared %d: expected ErrDeclaredLengthExceeded, got %v", declared, err)
		}
	}
	if factory.calls != 0 || record.callCount() != 0 {
		t.Fatal("declared-length rejection must happen before engine creation")
	}
}

func TestDecompressRejectsLengthMismatch(t *testing.T) {
	chain := "1234567890"

	t.Run("declared larger than output", func(t *testing.T) {
		factory := &scriptedFactory{
			algorithm: AlgZlib,
			build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: chunksOf(chain, 4)} },
		}
		wrapper, _ := NewWrapper(testLimits(), factory)
		record := &parseRecord{}

		err := wrapper.Decompress(context.Background(), AlgZlib, len(chain)+1, []byte("z"), record.cb)
		if !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("expected ErrLengthMismatch, got %v", err)
		}
		if record.callCount() != 0 {
			t.Fatal("short output must never reach the parser")
		}
	})

	t.Run("output larger than declared", func(t *testing.T) {
		factory := &scriptedFactory{
			algorithm: AlgZlib,
			build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: chunksOf(chain, 4)} },
		}
		wrapper, _ := NewWrapper(testLimits(), factory)
		record := &parseRecord{}

		err := wrapper.Decompress(context.Background(), AlgZlib, len(chain)-1, []byte("z"), record.cb)
		if !errors.Is(err, ErrLengthMismatch) {
			t.Fatalf("expected ErrLengthMismatch, got %v", err)
		}
		if record.callCount() != 0 {
			t.Fatal("over-production must be stopped before parser invocation")
		}
	})
}

func TestDecompressRejectsTrailingFrame(t *testing.T) {
	chain := "complete-chain"
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: chunksOf(chain, 5), trailing: true} },
	}
	wrapper, _ := NewWrapper(testLimits(), factory)
	record := &parseRecord{}

	err := wrapper.Decompress(context.Background(), AlgZlib, len(chain), []byte("frame1frame2"), record.cb)
	if !errors.Is(err, ErrTrailingData) {
		t.Fatalf("expected ErrTrailingData, got %v", err)
	}
	if record.callCount() != 0 {
		t.Fatal("even a fully produced chain with trailing bytes must not reach the parser")
	}
}

func TestDecompressEnforcesStepBudget(t *testing.T) {
	chain := "abcdefghijklmnop" // 8 steps when emitted two bytes at a time
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: chunksOf(chain, 2)} },
	}
	limits := testLimits()
	limits.MaxSteps = 3
	wrapper, _ := NewWrapper(limits, factory)
	record := &parseRecord{}

	err := wrapper.Decompress(context.Background(), AlgZlib, len(chain), []byte("z"), record.cb)
	if !errors.Is(err, ErrStepLimitExceeded) {
		t.Fatalf("expected ErrStepLimitExceeded, got %v", err)
	}
	if factory.lastStep.steps != limits.MaxSteps {
		t.Fatalf("engine ran %d steps, want cap %d", factory.lastStep.steps, limits.MaxSteps)
	}
	if record.callCount() != 0 {
		t.Fatal("step-budget failure must not reach the parser")
	}
}

func TestDecompressEnforcesOutputBudget(t *testing.T) {
	limits := Limits{MaxDeclaredLength: 128, MaxOutput: 8, MaxSteps: 32}
	factory := &scriptedFactory{
		algorithm: AlgZstd,
		build: func([]byte) *scriptedStep {
			return &scriptedStep{chunks: [][]byte{make([]byte, 6), make([]byte, 6)}}
		},
	}
	wrapper, _ := NewWrapper(limits, factory)
	record := &parseRecord{}

	err := wrapper.Decompress(context.Background(), AlgZstd, 128, []byte("tiny"), record.cb)
	if !errors.Is(err, ErrOutputLimitExceeded) {
		t.Fatalf("expected ErrOutputLimitExceeded, got %v", err)
	}
	if record.callCount() != 0 {
		t.Fatal("output-budget failure must not reach the parser")
	}
}

func TestDecompressHonorsCancellation(t *testing.T) {
	chain := "0123456789abcdef"

	t.Run("already cancelled", func(t *testing.T) {
		factory := &scriptedFactory{
			algorithm: AlgZlib,
			build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: chunksOf(chain, 4)} },
		}
		wrapper, _ := NewWrapper(testLimits(), factory)
		record := &parseRecord{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := wrapper.Decompress(ctx, AlgZlib, len(chain), []byte("z"), record.cb)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if factory.calls != 0 || record.callCount() != 0 {
			t.Fatal("pre-cancelled context must abort before engine creation")
		}
	})

	t.Run("cancelled mid stream", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		factory := &scriptedFactory{
			algorithm: AlgZlib,
			build: func([]byte) *scriptedStep {
				return &scriptedStep{
					chunks: chunksOf(chain, 4),
					onStep: func(step int) {
						if step == 1 {
							cancel()
						}
					},
				}
			},
		}
		wrapper, _ := NewWrapper(testLimits(), factory)
		record := &parseRecord{}

		err := wrapper.Decompress(ctx, AlgZlib, len(chain), []byte("z"), record.cb)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if record.callCount() != 0 {
			t.Fatal("cancellation must not expose a partially decompressed chain")
		}
		if factory.lastStep.steps != 2 {
			t.Fatalf("engine ran %d steps, want 2 before abort", factory.lastStep.steps)
		}
	})
}

func TestDecompressPropagatesEngineFailure(t *testing.T) {
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build: func([]byte) *scriptedStep {
			return &scriptedStep{chunks: chunksOf("partialchain", 4), failStep: 2, failing: true}
		},
	}
	wrapper, _ := NewWrapper(testLimits(), factory)
	record := &parseRecord{}

	err := wrapper.Decompress(context.Background(), AlgZlib, 12, []byte("z"), record.cb)
	if !errors.Is(err, ErrDecompressor) {
		t.Fatalf("expected ErrDecompressor, got %v", err)
	}
	if record.callCount() != 0 {
		t.Fatal("engine failure must not reach the parser")
	}
}

func TestDecompressRejectsNoProgress(t *testing.T) {
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build: func([]byte) *scriptedStep {
			return &scriptedStep{chunks: [][]byte{[]byte("ab"), nil, []byte("cd")}}
		},
	}
	wrapper, _ := NewWrapper(testLimits(), factory)
	record := &parseRecord{}

	err := wrapper.Decompress(context.Background(), AlgZlib, 4, []byte("z"), record.cb)
	if !errors.Is(err, ErrNoProgress) {
		t.Fatalf("expected ErrNoProgress, got %v", err)
	}
	if record.callCount() != 0 {
		t.Fatal("a stalled engine must not reach the parser")
	}
}

func TestDecompressRejectsNilCallback(t *testing.T) {
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: [][]byte{[]byte("x")}} },
	}
	wrapper, _ := NewWrapper(testLimits(), factory)

	if err := wrapper.Decompress(context.Background(), AlgZlib, 1, []byte("z"), nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestDecompressPropagatesParseFailureWithoutRetry(t *testing.T) {
	parseErr := errors.New("chain rejected by host")
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: [][]byte{[]byte("chain")}} },
	}
	wrapper, _ := NewWrapper(testLimits(), factory)
	record := &parseRecord{err: parseErr}

	err := wrapper.Decompress(context.Background(), AlgZlib, 5, []byte("z"), record.cb)
	if !errors.Is(err, parseErr) {
		t.Fatalf("expected parse callback error, got %v", err)
	}
	if record.callCount() != 1 {
		t.Fatalf("parse callback called %d times, want exactly 1", record.callCount())
	}
}

type failingFactory struct {
	algorithm uint16
}

func (f *failingFactory) Algorithm() uint16 { return f.algorithm }

func (f *failingFactory) New([]byte) (StepDecompressor, error) {
	return nil, errors.New("engine refused payload")
}

func TestDecompressWrapsEngineConstructionFailure(t *testing.T) {
	wrapper, err := NewWrapper(testLimits(), &failingFactory{algorithm: AlgBrotli})
	if err != nil {
		t.Fatalf("NewWrapper: %v", err)
	}
	record := &parseRecord{}

	err = wrapper.Decompress(context.Background(), AlgBrotli, 4, []byte("z"), record.cb)
	if !errors.Is(err, ErrDecompressor) {
		t.Fatalf("expected ErrDecompressor, got %v", err)
	}
	if record.callCount() != 0 {
		t.Fatal("engine construction failure must not reach the parser")
	}
}

func TestDecompressIsSafeForConcurrentHandshakes(t *testing.T) {
	factory := &scriptedFactory{
		algorithm: AlgZlib,
		build:     func([]byte) *scriptedStep { return &scriptedStep{chunks: [][]byte{[]byte("ok")}} },
	}
	wrapper, _ := NewWrapper(testLimits(), factory)

	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			record := &parseRecord{}
			errs <- wrapper.Decompress(context.Background(), AlgZlib, 2, []byte("z"), record.cb)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Decompress: %v", err)
		}
	}
}

func TestNegotiatedAlgorithmsOnlyIncludesConfiguredSet(t *testing.T) {
	factories := []Decompressor{
		&scriptedFactory{algorithm: AlgZstd, build: func([]byte) *scriptedStep { return &scriptedStep{} }},
		&scriptedFactory{algorithm: AlgZlib, build: func([]byte) *scriptedStep { return &scriptedStep{} }},
	}
	wrapper, err := NewWrapper(testLimits(), factories...)
	if err != nil {
		t.Fatalf("NewWrapper: %v", err)
	}

	got := wrapper.NegotiatedAlgorithms()
	want := []uint16{AlgZlib, AlgZstd}
	if len(got) != len(want) {
		t.Fatalf("algorithms = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("algorithms = %v, want ascending %v", got, want)
		}
	}
}
