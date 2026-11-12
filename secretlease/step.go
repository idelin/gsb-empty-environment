package secretlease

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrStepFinished is returned when a finished step is asked for resources.
var ErrStepFinished = errors.New("secretlease: step has already finished")

// StepContext is the execution context handed to step callbacks. It embeds
// context.Context so cancellation propagates through lease callbacks and
// checkpoints, and identifies the step whose authority is in effect.
type StepContext interface {
	context.Context

	// StepID is the unique ID of the executing step.
	StepID() uint64
	// StepName is the human-readable step name.
	StepName() string
	// Alloc obtains a derived buffer owned by this step.
	Alloc(size int) (*Buffer, error)
	// Checkpoint blocks while the step's gate is paused, or returns
	// immediately when the step has no gate. It returns the context error if
	// the step is canceled while paused.
	Checkpoint() error
	// Host returns the build host that owns the step.
	Host() *Host
}

// Host owns the memory pool and mints steps and leases for a Go build driver.
type Host struct {
	pool *MemPool
	seq  atomic.Uint64
}

// NewHost creates a host backed by the given pool.
func NewHost(pool *MemPool) *Host { return &Host{pool: pool} }

// Pool returns the backing memory pool.
func (h *Host) Pool() *MemPool { return h.pool }

// NewLease creates a lease whose secret is copied into the host pool.
func (h *Host) NewLease(name string, secret []byte) (*Lease, error) {
	return NewLease(name, secret, h.pool)
}

// NewRootStep creates a top-level step.
func (h *Host) NewRootStep(name string, opts ...StepOption) *Step {
	return h.newStep(name, nil, opts...)
}

func (h *Host) newStep(name string, parent *Step, opts ...StepOption) *Step {
	s := &Step{
		host:    h,
		id:      h.seq.Add(1),
		name:    name,
		parent:  parent,
		baseCtx: context.Background(),
		buffers: make(map[*Buffer]struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.ctx, s.cancel = context.WithCancel(s.baseCtx)
	return s
}

// StepOption customizes a step at creation.
type StepOption func(*Step)

// WithGate attaches a pause gate whose [StepContext.Checkpoint] calls block
// while paused.
func WithGate(g *Gate) StepOption {
	return func(s *Step) { s.gate = g }
}

// WithContext sets the parent context for a root step.
func WithContext(ctx context.Context) StepOption {
	return func(s *Step) {
		if ctx != nil {
			s.baseCtx = ctx
		}
	}
}

// Step is one build step with its own identity, cancellation and derived
// buffers.
type Step struct {
	host    *Host
	id      uint64
	name    string
	parent  *Step
	gate    *Gate
	baseCtx context.Context
	ctx     context.Context
	cancel  context.CancelFunc

	mu       sync.Mutex
	buffers  map[*Buffer]struct{}
	finished bool

	children sync.WaitGroup
}

// ID returns the unique step ID.
func (s *Step) ID() uint64 { return s.id }

// Name returns the step name.
func (s *Step) Name() string { return s.name }

// Parent returns the parent step, or nil for a root step.
func (s *Step) Parent() *Step { return s.parent }

// Cancel cancels the step context, unblocking checkpoints and causing
// lease callbacks to observe context cancellation.
func (s *Step) Cancel() { s.cancel() }

// Done is closed when the step context is canceled.
func (s *Step) Done() <-chan struct{} { return s.ctx.Done() }

// Finished reports whether Run has completed cleanup.
func (s *Step) Finished() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished
}

// SubStep creates a derived step. The child inherits the parent's context for
// cancellation but never inherits lease authorization: leases must grant the
// child's own ID.
func (s *Step) SubStep(name string, opts ...StepOption) *Step {
	return s.host.newStep(name, s, append(opts, WithContext(s.ctx))...)
}

// Run executes fn in the step context and guarantees cleanup: it waits for
// all sub-steps started via [Step.Go], then zeroes every derived buffer,
// whether fn returned normally, returned an error or panicked. A panic is
// re-raised after cleanup so callers still observe the failure.
func (s *Step) Run(fn func(StepContext) error) (err error) {
	defer s.finish()
	return fn(&stepContext{step: s})
}

// Go runs fn in a sub-step goroutine. The parent step's Run does not finish
// cleanup until the sub-step returns.
func (s *Step) Go(sub *Step, fn func(StepContext)) {
	s.children.Add(1)
	go func() {
		defer s.children.Done()
		sub.Run(func(ctx StepContext) error {
			fn(ctx)
			return nil
		})
	}()
}

// finish performs the guaranteed cleanup ordering: stop accepting new work,
// wait for all sub-steps (so cancellation and exceptions still get cleanup
// confirmation), then cancel the step context and zero every derived buffer.
func (s *Step) finish() {
	s.children.Wait()
	s.cancel()

	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	buffers := s.buffers
	s.buffers = nil
	s.mu.Unlock()

	for buf := range buffers {
		buf.Destroy()
	}
}

func (s *Step) alloc(size int) (*Buffer, error) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil, ErrStepFinished
	}
	s.mu.Unlock()

	buf, err := s.host.pool.Alloc(size)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		buf.Destroy()
		return nil, ErrStepFinished
	}
	s.buffers[buf] = struct{}{}
	s.mu.Unlock()
	return buf, nil
}

// stepContext is the per-callback view of a [Step].
type stepContext struct {
	step *Step
}

func (c *stepContext) Deadline() (deadline time.Time, ok bool) {
	return c.step.ctx.Deadline()
}

func (c *stepContext) Done() <-chan struct{} { return c.step.ctx.Done() }

func (c *stepContext) Err() error { return c.step.ctx.Err() }

func (c *stepContext) Value(key any) any { return c.step.ctx.Value(key) }

func (c *stepContext) StepID() uint64 { return c.step.id }

func (c *stepContext) StepName() string { return c.step.name }

func (c *stepContext) Alloc(size int) (*Buffer, error) { return c.step.alloc(size) }

func (c *stepContext) Checkpoint() error {
	if c.step.gate == nil {
		select {
		case <-c.step.ctx.Done():
			return c.step.ctx.Err()
		default:
			return nil
		}
	}
	return c.step.gate.wait(c.step.ctx.Done())
}

func (c *stepContext) Host() *Host { return c.step.host }
