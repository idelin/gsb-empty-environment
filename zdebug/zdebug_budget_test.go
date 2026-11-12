package zdebug

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"
)

const (
	testPageSize = 64
	testBudget   = 3 * testPageSize
)

func TestMultipleConsumersCompeteForBudget(t *testing.T) {
	const sectionCount = 8

	var blob bytes.Buffer
	want := make(map[string][]byte, sectionCount)
	sections := make([]Section, 0, sectionCount)

	for i := 0; i < sectionCount; i++ {
		name := fmt.Sprintf(".zdebug_%02d", i)
		// Each section spans 4-6 pages.
		payload := bytes.Repeat([]byte{byte('A' + i), byte('0' + (i % 10))}, 140+i*13)
		encoded := EncodeFrames(payload)

		offset := int64(blob.Len())
		blob.Write(encoded)

		sections = append(sections, Section{
			Name:            name,
			Method:          MethodFrame,
			PhysicalOffset:  offset,
			PhysicalLength:  int64(len(encoded)),
			MaxDecompressed: int64(len(payload)),
		})
		want[name] = payload
	}

	data := blob.Bytes()
	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, testPageSize, testBudget)

	host := mustHost(t, bytes.NewReader(data), int64(len(data)), sections, cfg)
	t.Cleanup(func() { host.Close() })

	const consumers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, consumers)

	start := make(chan struct{})
	for consumer := 0; consumer < consumers; consumer++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			<-start

			rng := rand.New(rand.NewSource(seed))
			for round := 0; round < 25; round++ {
				name := sections[rng.Intn(len(sections))].Name
				payload := want[name]

				startOff := rng.Int63n(int64(len(payload)))
				length := 1 + rng.Intn(int(int64(len(payload))-startOff))
				buf := make([]byte, length)

				n, err := host.ReadAt(name, buf, startOff)
				if err != nil && !errors.Is(err, io.EOF) {
					errCh <- fmt.Errorf("read %s at %d: %w", name, startOff, err)
					return
				}
				if !bytes.Equal(buf[:n], payload[startOff:startOff+int64(n)]) {
					errCh <- fmt.Errorf("content mismatch in %s at %d", name, startOff)
					return
				}
			}
		}(int64(consumer) + 1)
	}

	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	if used := host.BudgetUsed(); used > testBudget {
		t.Fatalf("global budget exceeded under concurrency: %d > %d", used, testBudget)
	}

	// Pages actually cached never exceed the budget-derived page count.
	totalPages := 0
	host.budget.mu.Lock()
	totalPages = host.budget.lru.Len()
	host.budget.mu.Unlock()
	if totalPages > testBudget/testPageSize {
		t.Fatalf("cached pages %d exceed budget slot count", totalPages)
	}
}

func TestConcurrentConsumersShareOneDecodingRun(t *testing.T) {
	payload := bytes.Repeat([]byte("singleflight-"), 80) // 1080 bytes
	encoded := EncodeFrames(payload)

	section := Section{
		Name:            "shared",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload)),
	}

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var once sync.Once
	blocking := DecoderFactory(func(r io.Reader) (Decoder, error) {
		decoder, err := frameFactory(r)
		if err != nil {
			return nil, err
		}
		once.Do(func() {
			entered <- struct{}{}
			<-release
		})
		return decoder, nil
	})

	cfg := Config{
		PageSize:    testPageSize,
		BudgetBytes: testBudget,
		Factories:   map[Method]DecoderFactory{MethodFrame: blocking},
	}

	host := mustHost(t, bytes.NewReader(encoded), int64(len(encoded)), []Section{section}, cfg)
	t.Cleanup(func() {
		host.Close()
	})

	const waiters = 12
	var wg sync.WaitGroup
	errCh := make(chan error, waiters)
	start := make(chan struct{})

	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			buf := make([]byte, 64)
			if _, err := host.ReadAt(section.Name, buf, 0); err != nil {
				errCh <- err
				return
			}
			if !bytes.Equal(buf, payload[:64]) {
				errCh <- errors.New("shared run returned wrong bytes")
			}
		}()
	}

	close(start)
	<-entered
	close(release)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	if got := decoderRuns(host, section.Name); got != 1 {
		t.Fatalf("want exactly 1 decoder for %d concurrent consumers, got %d", waiters, got)
	}
}

func TestBudgetEvictsAndRematerializes(t *testing.T) {
	first := bytes.Repeat([]byte("first-"), 100) // 600 bytes, ~10 pages
	second := bytes.Repeat([]byte("second-"), 100)

	firstEncoded := EncodeFrames(first)
	secondEncoded := EncodeFrames(second)

	var blob bytes.Buffer
	firstOffset := int64(blob.Len())
	blob.Write(firstEncoded)
	secondOffset := int64(blob.Len())
	blob.Write(secondEncoded)

	sections := []Section{
		{
			Name:            "first",
			Method:          MethodFrame,
			PhysicalOffset:  firstOffset,
			PhysicalLength:  int64(len(firstEncoded)),
			MaxDecompressed: int64(len(first)),
		},
		{
			Name:            "second",
			Method:          MethodFrame,
			PhysicalOffset:  secondOffset,
			PhysicalLength:  int64(len(secondEncoded)),
			MaxDecompressed: int64(len(second)),
		},
	}

	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, testPageSize, testBudget)
	host := mustHost(t, bytes.NewReader(blob.Bytes()), int64(blob.Len()), sections, cfg)
	t.Cleanup(func() { host.Close() })

	if _, err := readAll(t, host, "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := readAll(t, host, "second"); err != nil {
		t.Fatal(err)
	}

	// Reading the tail of first forces a re-materialization; content must
	// remain byte-identical through eviction cycles.
	tail := make([]byte, 64)
	if _, err := host.ReadAt("first", tail, int64(len(first)-64)); err != nil {
		t.Fatalf("re-materialize evicted first: %v", err)
	}
	if !bytes.Equal(tail, first[len(first)-64:]) {
		t.Fatal("first section content corrupted by eviction")
	}

	decoderRuns := factory.NewCount.Load()
	if decoderRuns < 2 {
		t.Fatalf("expected eviction to trigger another decode run, got %d", decoderRuns)
	}
	if used := host.BudgetUsed(); used > testBudget {
		t.Fatalf("budget invariant broken: %d > %d", used, testBudget)
	}
}

func TestReadAtSemanticsAndEmptySection(t *testing.T) {
	payload := []byte("0123456789abcdef") // 16 bytes
	encoded := EncodeFrames(payload)

	sections := []Section{
		{
			Name:            "data",
			Method:          MethodFrame,
			PhysicalOffset:  0,
			PhysicalLength:  int64(len(encoded)),
			MaxDecompressed: int64(len(payload)),
		},
		{
			Name:            "empty",
			Method:          MethodFrame,
			PhysicalOffset:  0,
			PhysicalLength:  0,
			MaxDecompressed: 0,
		},
	}

	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, 8, 0)
	host := mustHost(t, bytes.NewReader(encoded), int64(len(encoded)), sections, cfg)
	t.Cleanup(func() { host.Close() })

	size, err := host.Size("data")
	if err != nil || size != 16 {
		t.Fatalf("size = %d, %v", size, err)
	}

	buf := make([]byte, 4)
	if n, err := host.ReadAt("data", buf, 12); err != nil || n != 4 {
		t.Fatalf("read near end: n=%d err=%v", n, err)
	}
	if string(buf) != "cdef" {
		t.Fatalf("got %q want cdef", buf)
	}

	// A read past the end returns io.EOF with zero bytes.
	if n, err := host.ReadAt("data", make([]byte, 1), 16); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("past end: n=%d err=%v", n, err)
	}

	// A read crossing the end returns the available bytes and io.EOF.
	partial := make([]byte, 8)
	n, err := host.ReadAt("data", partial, 12)
	if n != 4 || !errors.Is(err, io.EOF) {
		t.Fatalf("crossing end: n=%d err=%v", n, err)
	}
	if string(partial[:4]) != "cdef" {
		t.Fatalf("partial got %q", partial[:4])
	}

	emptySize, err := host.Size("empty")
	if err != nil || emptySize != 0 {
		t.Fatalf("empty size = %d, %v", emptySize, err)
	}
	if n, err := host.ReadAt("empty", make([]byte, 1), 0); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("empty read: n=%d err=%v", n, err)
	}
}

func TestCloseReleasesBudgetAndFailsLaterReads(t *testing.T) {
	payload := bytes.Repeat([]byte("close-"), 20)
	encoded := EncodeFrames(payload)
	section := Section{
		Name:            "close",
		Method:          MethodFrame,
		PhysicalOffset:  0,
		PhysicalLength:  int64(len(encoded)),
		MaxDecompressed: int64(len(payload)),
	}

	factory := NewCountingFactory(frameFactory)
	cfg := testConfig(factory, 16, 64)
	host := mustHost(t, bytes.NewReader(encoded), int64(len(encoded)), []Section{section}, cfg)

	if _, err := readAll(t, host, section.Name); err != nil {
		t.Fatal(err)
	}
	if host.BudgetUsed() == 0 {
		t.Fatal("expected cached pages before close")
	}
	if err := host.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if host.BudgetUsed() != 0 {
		t.Fatalf("close must release all pages, used=%d", host.BudgetUsed())
	}
	if _, err := host.Size(section.Name); !errors.Is(err, ErrClosed) {
		t.Fatalf("read after close: want ErrClosed, got %v", err)
	}
}

func decoderRuns(host *Host, name string) int64 {
	host.mu.Lock()
	defer host.mu.Unlock()
	state := host.entries[name]
	if state == nil {
		return 0
	}
	return state.runs
}
