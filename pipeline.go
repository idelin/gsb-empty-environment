package freqsketch

import (
	"errors"
	"sync"
	"time"
)

// ErrClosed is returned by operations on a closed Pipeline.
var ErrClosed = errors.New("freqsketch: pipeline is closed")

// Event is one input record. ID must be unique per logical event;
// redeliveries with the same ID are idempotent no-ops.
type Event struct {
	ID    string
	Key   string
	Bytes int
}

// Source is an injectable data source. It returns ok=false when drained.
type Source func() (ev Event, ok bool, err error)

// Clock is an injectable clock.
type Clock func() time.Time

// FailHook is an injectable failure point. It is consulted before the
// named operation mutates any state; returning an error aborts the
// operation atomically (no partial state is applied).
type FailHook func(op string) error

// PipelineConfig wires a Pipeline. All dependencies are injectable;
// there is no remote cache or other hidden external state.
type PipelineConfig struct {
	Sketch     *Sketch       // required
	Table      *Table        // optional
	Clock      Clock         // default time.Now
	Fail       FailHook      // default nil (never fails)
	DecayEvery time.Duration // <=0 disables periodic decay
}

// Pipeline consumes events, updates the sketch and admission table,
// deduplicates by event ID, and periodically advances the decay epoch.
type Pipeline struct {
	sketch     *Sketch
	table      *Table
	clock      Clock
	fail       FailHook
	decayEvery time.Duration

	mu        sync.Mutex
	seen      map[string]struct{}
	lastDecay time.Time
	closed    bool
	closers   []func() error
}

func NewPipeline(cfg PipelineConfig) *Pipeline {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Pipeline{
		sketch:     cfg.Sketch,
		table:      cfg.Table,
		clock:      clock,
		fail:       cfg.Fail,
		decayEvery: cfg.DecayEvery,
		seen:       map[string]struct{}{},
		lastDecay:  clock(),
	}
}

// OnClose registers a resource to release on Close. If the pipeline is
// already closed, the closer runs immediately.
func (p *Pipeline) OnClose(c func() error) {
	p.mu.Lock()
	closed := p.closed
	if !closed {
		p.closers = append(p.closers, c)
	}
	p.mu.Unlock()
	if closed {
		_ = c()
	}
}

// Process applies one event. Duplicate IDs are idempotent no-ops.
// Failures injected via FailHook abort before any state changes.
func (p *Pipeline) Process(ev Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrClosed
	}
	if p.fail != nil {
		if err := p.fail("process"); err != nil {
			return err
		}
	}
	if _, dup := p.seen[ev.ID]; dup {
		return nil
	}
	p.maybeDecayLocked(p.clock())
	p.sketch.Add(ev.Key, 1)
	if p.table != nil {
		p.table.Admit(ev.Key, ev.Bytes)
	}
	p.seen[ev.ID] = struct{}{}
	return nil
}

// Run drains a source until it is exhausted, fails, or the pipeline
// rejects an event.
func (p *Pipeline) Run(src Source) error {
	for {
		ev, ok, err := src()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := p.Process(ev); err != nil {
			return err
		}
	}
}

// maybeDecayLocked advances epochs for every fully elapsed decay
// interval. Decay is declared at these logical boundaries only.
func (p *Pipeline) maybeDecayLocked(now time.Time) {
	if p.decayEvery <= 0 {
		return
	}
	for !now.Before(p.lastDecay.Add(p.decayEvery)) {
		p.sketch.AdvanceEpoch()
		p.lastDecay = p.lastDecay.Add(p.decayEvery)
	}
}

// State is a full pipeline checkpoint.
type State struct {
	Sketch    SketchState
	Table     TableState
	HasTable  bool
	Seen      map[string]struct{}
	LastDecay time.Time
}

// Checkpoint captures a restorable snapshot of the whole pipeline.
func (p *Pipeline) Checkpoint() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]struct{}, len(p.seen))
	for id := range p.seen {
		seen[id] = struct{}{}
	}
	st := State{Sketch: p.sketch.Snapshot(), Seen: seen, LastDecay: p.lastDecay}
	if p.table != nil {
		st.Table = p.table.Snapshot()
		st.HasTable = true
	}
	return st
}

// Restore rolls the pipeline back to a checkpoint. The caller must
// ensure the injected clock reads the same logical time as at
// checkpointing for decay decisions to be identical.
func (p *Pipeline) Restore(st State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sketch.Restore(st.Sketch)
	if p.table != nil && st.HasTable {
		p.table.Restore(st.Table)
	}
	seen := make(map[string]struct{}, len(st.Seen))
	for id := range st.Seen {
		seen[id] = struct{}{}
	}
	p.seen = seen
	p.lastDecay = st.LastDecay
}

// Close releases all registered resources exactly once. It is
// idempotent; later calls return nil immediately.
func (p *Pipeline) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	closers := p.closers
	p.closers = nil
	p.mu.Unlock()
	var errs []error
	for _, c := range closers {
		if err := c(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
