package cacheaudit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func newManualClock() *manualClock { return &manualClock{t: time.Unix(1_700_000_000, 0)} }

func (c *manualClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// versionedLoader returns a Loader that hands out increasing versions per key.
func versionedLoader(clk *manualClock, acquired, released *atomic.Int64) Loader {
	var mu sync.Mutex
	versions := map[string]uint64{}
	return func(ctx context.Context, key string) (uint64, any, func(), error) {
		mu.Lock()
		versions[key]++
		v := versions[key]
		mu.Unlock()
		if acquired != nil {
			acquired.Add(1)
		}
		return v, key + "-value", func() {
			if released != nil {
				released.Add(1)
			}
		}, nil
	}
}

func TestHitMissStatsAndEvents(t *testing.T) {
	clk := newManualClock()
	c := New(Options{Load: versionedLoader(clk, nil, nil), Now: clk.now})
	defer c.Close()
	ctx := context.Background()

	if _, v, err := c.Get(ctx, "a"); err != nil || v != 1 {
		t.Fatalf("first Get = v%d, err %v; want v1", v, err)
	}
	clk.advance(time.Second)
	if _, v, err := c.Get(ctx, "a"); err != nil || v != 1 {
		t.Fatalf("second Get = v%d, err %v; want v1", v, err)
	}

	st := c.Stats()
	if st.Gets != 2 || st.Hits != 1 || st.Misses != 1 || st.Loads != 1 || st.Publishes != 1 {
		t.Fatalf("stats = %+v", st)
	}
	if got := st.HitRate(); got != 0.5 {
		t.Fatalf("HitRate = %v, want 0.5", got)
	}

	h := c.History()
	var kinds []Kind
	for _, e := range h.Events {
		kinds = append(kinds, e.Kind)
	}
	for _, want := range []Kind{KindLoad, KindPublish, KindCall} {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing event kind %v in %v", want, kinds)
		}
	}
	if rep := Audit(h); rep.Verdict != Consistent {
		t.Fatalf("audit = %v, want CONSISTENT (%+v)", rep.Verdict, rep)
	}
}

func TestUnpublishedLoadNotCountedAsHit(t *testing.T) {
	clk := newManualClock()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	loader := func(ctx context.Context, key string) (uint64, any, func(), error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
			return 0, nil, nil, ctx.Err()
		}
		return 1, "v", func() {}, nil
	}
	c := New(Options{Load: loader, Now: clk.now})
	defer c.Close()
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); c.Get(ctx, "k") }()
	<-entered // first call is now loading, unpublished
	go func() { defer wg.Done(); c.Get(ctx, "k") }()
	// Wait until the second call has attached to the in-flight load:
	// the waiting path increments Misses before blocking, and the loading
	// call cannot reach its own Misses increment while blocked.
	for i := 0; i < 1000 && c.Stats().Misses == 0; i++ {
		time.Sleep(time.Millisecond)
	}
	clk.advance(time.Second)
	close(release)
	wg.Wait()

	st := c.Stats()
	if st.Gets != 2 || st.Hits != 0 || st.Misses != 2 {
		t.Fatalf("stats = %+v; want Gets=2 Hits=0 Misses=2 (unpublished load is never a hit)", st)
	}
	if got := st.HitRate(); got != 0 {
		t.Fatalf("HitRate = %v, want 0", got)
	}
	for _, e := range c.History().Events {
		if e.Kind == KindCall && e.Outcome == OutcomeHit {
			t.Fatalf("call recorded as hit while load unpublished: %v", e)
		}
	}
}

func TestResourceReleaseOnEvictReplaceClose(t *testing.T) {
	clk := newManualClock()
	var acquired, released atomic.Int64
	c := New(Options{Load: versionedLoader(clk, &acquired, &released), Now: clk.now})
	ctx := context.Background()

	if _, _, err := c.Get(ctx, "a"); err != nil { // acquire a#1
		t.Fatal(err)
	}
	var pubReleased atomic.Int64
	if err := c.Publish("a", 2, "external", func() { pubReleased.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if released.Load() != 1 { // a#1 replaced by publish
		t.Fatalf("released = %d after replace, want 1", released.Load())
	}
	c.Evict("a") // releases the published entry
	if pubReleased.Load() != 1 {
		t.Fatalf("published entry not released on evict")
	}
	if _, _, err := c.Get(ctx, "b"); err != nil { // acquire b#1
		t.Fatal(err)
	}
	c.Close()
	c.Close() // idempotent
	if got, want := released.Load(), acquired.Load(); got != want {
		t.Fatalf("released = %d, acquired = %d; every acquired resource must be released exactly once", got, want)
	}
}

func TestRingOverwriteReportsInsufficientEvidence(t *testing.T) {
	clk := newManualClock()
	c := New(Options{Load: versionedLoader(clk, nil, nil), Now: clk.now, LogCap: 2})
	defer c.Close()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		c.Get(ctx, "a")
		clk.advance(time.Second)
		c.Evict("a")
	}
	h := c.History()
	if h.Meta.Dropped == 0 {
		t.Fatalf("expected dropped events with LogCap=2, meta=%+v", h.Meta)
	}
	if len(h.Events) > 2 {
		t.Fatalf("log is not bounded: retained %d events", len(h.Events))
	}
	rep := Audit(h)
	if rep.Verdict != Inconclusive {
		t.Fatalf("audit = %v, want INCONCLUSIVE on ring overwrite", rep.Verdict)
	}
	if len(rep.Incomplete) == 0 {
		t.Fatalf("missing incomplete-evidence explanation")
	}
}

func TestLogFailureMarksEvidenceIncomplete(t *testing.T) {
	clk := newManualClock()
	c := New(Options{
		Load: versionedLoader(clk, nil, nil),
		Now:  clk.now,
		Fail: func(op string) error {
			if op == "log" {
				return errors.New("sink unavailable")
			}
			return nil
		},
	})
	defer c.Close()
	if _, _, err := c.Get(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	st := c.Stats()
	if st.EventsLost == 0 || st.EvidenceComplete() {
		t.Fatalf("lost audit events not accounted: %+v", st)
	}
	if rep := Audit(c.History()); rep.Verdict != Inconclusive {
		t.Fatalf("audit = %v, want INCONCLUSIVE when audit events are dropped", rep.Verdict)
	}
}

func TestLoadFailureAndFixedDenominator(t *testing.T) {
	clk := newManualClock()
	boom := errors.New("boom")
	failLoad := true
	c := New(Options{
		Load: versionedLoader(clk, nil, nil),
		Now:  clk.now,
		Fail: func(op string) error {
			if op == "load" && failLoad {
				failLoad = false
				return boom
			}
			return nil
		},
	})
	defer c.Close()
	ctx := context.Background()

	if _, _, err := c.Get(ctx, "a"); !errors.Is(err, boom) { // error
		t.Fatalf("want injected load error, got %v", err)
	}
	if _, _, err := c.Get(ctx, "a"); err != nil { // miss + load
		t.Fatal(err)
	}
	if _, _, err := c.Get(ctx, "a"); err != nil { // hit
		t.Fatal(err)
	}
	st := c.Stats()
	if st.Gets != 3 || st.Hits != 1 || st.Misses != 1 || st.Errors != 1 || st.LoadErrors != 1 {
		t.Fatalf("stats = %+v", st)
	}
	// Fixed denominator: every Get call, including the failed one.
	if got, want := st.HitRate(), 1.0/3.0; got != want {
		t.Fatalf("HitRate = %v, want %v", got, want)
	}
	var sawErrorCall bool
	for _, e := range c.History().Events {
		if e.Kind == KindCall && e.Outcome == OutcomeError {
			sawErrorCall = true
		}
	}
	if !sawErrorCall {
		t.Fatalf("failed call not recorded")
	}
}
