package decayfreq

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// Event is one incoming occurrence of a key.
type Event struct {
	ID   string // unique; duplicates are ignored (idempotent processing)
	Key  string
	Size int // bytes, used when the key is admitted to the cache
}

// Deps bundles the injectable functions a Processor relies on. Nothing here
// touches a remote cache or the network.
type Deps struct {
	// Next is the data source: it returns the next event, or ok=false when
	// the stream is exhausted.
	Next func() (e Event, ok bool)
	// Now is the clock, used to schedule periodic decay.
	Now func() time.Time
	// Fail injects failures; it may be nil. Returning true for op
	// "checkpoint" makes the next Checkpoint call fail without side effects.
	Fail func(op string) bool
	// Close releases the data source. It is called exactly once by
	// Processor.Close.
	Close func() error
}

// Processor consumes events, updates the sketch and the admission cache, and
// decays the sketch periodically according to the injected clock.
type Processor struct {
	sketch        *Sketch
	cache         *Cache
	deps          Deps
	seen          map[string]struct{}
	decayInterval time.Duration
	lastDecay     time.Time
	closed        bool
}

// Config controls Processor construction.
type Config struct {
	Rows          int
	Width         int
	ByteQuota     int
	DecayInterval time.Duration
	Opts          []Option // sketch options (seed, hasher)
}

// NewProcessor builds a Processor from config and injectable deps.
func NewProcessor(cfg Config, deps Deps) *Processor {
	sk := NewSketch(cfg.Rows, cfg.Width, cfg.Opts...)
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Processor{
		sketch:        sk,
		cache:         NewCache(sk, cfg.ByteQuota),
		deps:          deps,
		seen:          make(map[string]struct{}),
		decayInterval: cfg.DecayInterval,
		lastDecay:     deps.Now(),
	}
}

// Sketch exposes the underlying sketch, e.g. for estimates.
func (p *Processor) Sketch() *Sketch { return p.sketch }

// Cache exposes the admission cache.
func (p *Processor) Cache() *Cache { return p.cache }

// Process applies one event. Duplicate event IDs are ignored entirely, so
// reprocessing is idempotent. It reports whether the event was a duplicate
// and, for fresh events, whether the key was admitted to the cache.
func (p *Processor) Process(e Event) (admitted bool, duplicate bool) {
	if _, dup := p.seen[e.ID]; dup {
		return false, true
	}
	p.seen[e.ID] = struct{}{}
	p.sketch.Add(e.Key, 1)
	return p.cache.Admit(e.Key, e.Size), false
}

// maybeDecay decays once per elapsed interval, driven by the injected clock.
func (p *Processor) maybeDecay() {
	if p.decayInterval <= 0 {
		return
	}
	now := p.deps.Now()
	for !now.Before(p.lastDecay.Add(p.decayInterval)) {
		p.sketch.Decay()
		p.lastDecay = p.lastDecay.Add(p.decayInterval)
	}
}

// Run drains the data source until it is exhausted.
func (p *Processor) Run() error {
	if p.deps.Next == nil {
		return errors.New("decayfreq: nil data source")
	}
	for {
		e, ok := p.deps.Next()
		if !ok {
			return nil
		}
		p.maybeDecay()
		p.Process(e)
	}
}

// Close releases the data source exactly once.
func (p *Processor) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if p.deps.Close != nil {
		return p.deps.Close()
	}
	return nil
}

// checkpoint is the gob-serialized processor state.
type checkpoint struct {
	Sketch        SketchState
	Entries       []CacheEntry // front-to-back LRU order
	Seen          []string
	LastDecayUnix int64
}

// Checkpoint atomically writes a snapshot to w. If the injected failure
// function rejects the "checkpoint" op, nothing is written and the processor
// state is untouched. Encoding happens into a buffer first, so a failed or
// partial write never corrupts a previously good checkpoint at the caller.
func (p *Processor) Checkpoint(w io.Writer) error {
	if p.deps.Fail != nil && p.deps.Fail("checkpoint") {
		return errors.New("decayfreq: checkpoint failed (injected)")
	}
	seen := make([]string, 0, len(p.seen))
	for id := range p.seen {
		seen = append(seen, id)
	}
	sort.Strings(seen)
	cp := checkpoint{
		Sketch:        p.sketch.Snapshot(),
		Entries:       p.cache.Snapshot(),
		Seen:          seen,
		LastDecayUnix: p.lastDecay.Unix(),
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(cp); err != nil {
		return fmt.Errorf("decayfreq: encode checkpoint: %w", err)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("decayfreq: write checkpoint: %w", err)
	}
	return nil
}

// RestoreProcessor rebuilds a Processor from a checkpoint. The config and
// deps must match the original run for decisions to be identical.
func RestoreProcessor(cfg Config, deps Deps, r io.Reader) (*Processor, error) {
	var cp checkpoint
	if err := gob.NewDecoder(r).Decode(&cp); err != nil {
		return nil, fmt.Errorf("decayfreq: decode checkpoint: %w", err)
	}
	p := NewProcessor(cfg, deps)
	p.sketch = RestoreSketch(cp.Sketch, cfg.Opts...)
	p.cache = NewCache(p.sketch, cfg.ByteQuota)
	p.cache.restore(cp.Entries)
	for _, id := range cp.Seen {
		p.seen[id] = struct{}{}
	}
	p.lastDecay = time.Unix(cp.LastDecayUnix, 0)
	return p, nil
}
