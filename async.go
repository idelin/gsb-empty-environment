package netsec

import (
	"context"
)

// SpawnPolicy is deliberately non-zero at its zero value: SpawnAsync callers
// must choose NoIdentity or Capture explicitly.
type SpawnPolicy uint8

const (
	PolicyUnspecified SpawnPolicy = iota
	// NoIdentity runs internal work without any client identity. Resource
	// calls through the task handle are denied.
	NoIdentity
	// Capture copies the caller's authenticated identity into a read-only
	// snapshot. The snapshot defaults to Identification.
	Capture
)

func (p SpawnPolicy) String() string {
	switch p {
	case NoIdentity:
		return "NoIdentity"
	case Capture:
		return "Capture"
	default:
		return "Unspecified"
	}
}

// AsyncFunc is the callback shape. All resource access must go through the
// supplied TaskContext so that the handle is revalidated for each call.
type AsyncFunc func(context.Context, *TaskContext) error

// Grant is an unforgeable host capability. It binds an authorization to one
// request generation and a maximum level and deadline.
type Grant struct {
	host      *Host
	id        uint64
	request   *Request
	connID    string
	requestID string
	gen       uint64
	level     Level
	deadline  Instant
	active    bool
}

func (g *Grant) ID() uint64 {
	if g == nil {
		return 0
	}
	return g.id
}

func (g *Grant) Level() Level {
	if g == nil {
		return Identification
	}
	return g.level
}

func (g *Grant) Deadline() Instant {
	if g == nil {
		return 0
	}
	return g.deadline
}

func (g *Grant) RequestID() string {
	if g == nil {
		return ""
	}
	return g.requestID
}

func (g *Grant) Active() bool {
	if g == nil || g.host == nil {
		return false
	}
	g.host.mu.Lock()
	defer g.host.mu.Unlock()
	return g.active
}

type spawnConfig struct {
	captureLevel Level
	levelSet     bool
	grant        *Grant
	deadline     Instant
	deadlineSet  bool
}

// SpawnOption configures a capture. Applying an invalid combination returns an
// error from SpawnAsync rather than silently weakening the policy.
type SpawnOption func(*spawnConfig) error

// CaptureAtLevel asks to retain authority above Identification. grant must be
// a live host authorization covering the request, level, and task deadline.
func CaptureAtLevel(level Level, grant *Grant) SpawnOption {
	return func(cfg *spawnConfig) error {
		if !level.Valid() {
			return ErrInvalidLevel
		}
		cfg.captureLevel = level
		cfg.levelSet = true
		cfg.grant = grant
		return nil
	}
}

func WithCaptureLevel(level Level, grant *Grant) SpawnOption {
	return CaptureAtLevel(level, grant)
}

// WithTaskDeadline sets the task's absolute host-monotonic deadline. It can
// only narrow the request deadline.
func WithTaskDeadline(deadline Instant) SpawnOption {
	return func(cfg *spawnConfig) error {
		cfg.deadline = deadline
		cfg.deadlineSet = true
		return nil
	}
}

func WithDeadline(deadline Instant) SpawnOption {
	return WithTaskDeadline(deadline)
}

type taskState uint8

const (
	taskPending taskState = iota
	taskRunning
	taskDone
	taskRevoked
	taskExpired
	taskCanceled
)

// Task is a request-generation-bound async handle. The handle, not only its
// snapshot, is checked at callback start and on every resource call.
type Task struct {
	host         *Host
	id           uint64
	request      *Request
	connectionID string
	requestID    string
	generation   uint64
	policy       SpawnPolicy
	snapshot     Snapshot
	deadline     Instant
	grant        *Grant
	fn           AsyncFunc
	state        taskState
	err          error
}

func (t *Task) ID() uint64 {
	if t == nil {
		return 0
	}
	return t.id
}

func (t *Task) Policy() SpawnPolicy {
	if t == nil {
		return PolicyUnspecified
	}
	return t.policy
}

func (t *Task) RequestID() string {
	if t == nil {
		return ""
	}
	return t.requestID
}

func (t *Task) ConnectionID() string {
	if t == nil {
		return ""
	}
	return t.connectionID
}

func (t *Task) Generation() uint64 {
	if t == nil {
		return 0
	}
	return t.generation
}

func (t *Task) Deadline() Instant {
	if t == nil {
		return 0
	}
	return t.deadline
}

// Identity returns the read-only snapshot. It is informational; it does not
// carry access authority.
func (t *Task) Identity() Snapshot {
	if t == nil {
		return Snapshot{}
	}
	return t.snapshot
}

// Snapshot is an alias for Identity.
func (t *Task) Snapshot() Snapshot {
	return t.Identity()
}

func (t *Task) HasIdentity() bool {
	if t == nil {
		return false
	}
	return t.policy == Capture
}

func (t *Task) Done() bool {
	if t == nil || t.host == nil {
		return true
	}
	t.host.mu.Lock()
	defer t.host.mu.Unlock()
	switch t.state {
	case taskPending, taskRunning:
		return false
	default:
		return true
	}
}

func (t *Task) Err() error {
	if t == nil || t.host == nil {
		return ErrInvalidToken
	}
	t.host.mu.Lock()
	defer t.host.mu.Unlock()
	return t.err
}

// Verify revalidates the handle at the current instant. It fails after the
// request ends, after connection generation reuse, after the task deadline, or
// after the callback has returned.
func (t *Task) Verify() error {
	if t == nil || t.host == nil {
		return ErrInvalidToken
	}
	h := t.host
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.verifyTaskLocked(t)
}

func (h *Host) verifyTaskLocked(t *Task) error {
	if t == nil || t.host != h {
		return ErrInvalidToken
	}
	switch t.state {
	case taskRevoked:
		return ErrTaskRevoked
	case taskExpired:
		return ErrTaskExpired
	case taskDone, taskCanceled:
		return ErrTaskFinished
	}
	if !h.clock.Now().Before(t.deadline) {
		t.state = taskExpired
		t.err = ErrTaskExpired
		return ErrTaskExpired
	}
	if t.request == nil {
		t.state = taskRevoked
		t.err = ErrTaskRevoked
		return ErrTaskRevoked
	}
	if err := h.verifyRequestLocked(t.request); err != nil {
		t.state = taskRevoked
		t.err = ErrTaskRevoked
		return ErrTaskRevoked
	}
	if t.generation != t.request.generation {
		t.state = taskRevoked
		t.err = ErrTaskRevoked
		return ErrTaskRevoked
	}
	if t.grant != nil {
		if !t.grant.active {
			t.state = taskRevoked
			t.err = ErrGrantRevoked
			return ErrGrantRevoked
		}
		if t.grant.request != t.request || t.grant.gen != t.generation {
			t.state = taskRevoked
			t.err = ErrInvalidGrant
			return ErrInvalidGrant
		}
	}
	return nil
}

// SpawnAsync creates a deferred async handle from the request's authenticated
// token. Capture defaults to Identification; any higher level needs a grant.
// If the host has an Executor, the new task is submitted to it.
func (h *Host) SpawnAsync(
	req *Request,
	policy SpawnPolicy,
	fn AsyncFunc,
	options ...SpawnOption,
) (*Task, error) {
	if h == nil {
		return nil, ErrDifferentHost
	}
	if req == nil || req.host != h || req.token == nil {
		return nil, ErrRequestMismatch
	}
	return h.spawnAsync(req, req.token, policy, fn, options...)
}

// SpawnAsyncOn is the thread-strict variant. For Capture it snapshots the
// thread's current token, which must belong to req. This is how nested scopes
// are captured without ever borrowing another request's thread token.
func (h *Host) SpawnAsyncOn(
	thread *Thread,
	req *Request,
	policy SpawnPolicy,
	fn AsyncFunc,
	options ...SpawnOption,
) (*Task, error) {
	if h == nil {
		return nil, ErrDifferentHost
	}
	if thread == nil || thread.host != h || req == nil || req.host != h {
		return nil, ErrDifferentHost
	}
	tok, bound := thread.accessBinding()
	if bound != req {
		return nil, ErrWrongRequest
	}
	if policy == Capture {
		if tok == nil || tok.primary || tok.request != req {
			return nil, ErrWrongRequest
		}
	} else {
		tok = req.token
	}
	return h.spawnAsync(req, tok, policy, fn, options...)
}

func (h *Host) spawnAsync(
	req *Request,
	source *Token,
	policy SpawnPolicy,
	fn AsyncFunc,
	options ...SpawnOption,
) (*Task, error) {
	if policy == PolicyUnspecified {
		return nil, ErrPolicyRequired
	}
	if policy != NoIdentity && policy != Capture {
		return nil, ErrInvalidPolicy
	}
	if fn == nil {
		return nil, ErrNilCallback
	}
	var cfg spawnConfig
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&cfg); err != nil {
			return nil, err
		}
	}
	if policy == NoIdentity && (cfg.levelSet || cfg.grant != nil) {
		return nil, ErrNoIdentityOption
	}

	h.mu.Lock()
	task, err := h.spawnLocked(req, source, policy, fn, cfg)
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if h.executor != nil {
		if err := h.executor.Submit(task); err != nil {
			h.mu.Lock()
			task.state = taskRevoked
			task.err = err
			h.mu.Unlock()
			return task, err
		}
	}
	return task, nil
}

func (h *Host) spawnLocked(
	req *Request,
	source *Token,
	policy SpawnPolicy,
	fn AsyncFunc,
	cfg spawnConfig,
) (*Task, error) {
	if err := h.verifyRequestLocked(req); err != nil {
		return nil, err
	}
	if source == nil || source.request != req {
		return nil, ErrTokenRequestMismatch
	}
	if err := h.verifyTokenLocked(source); err != nil {
		return nil, err
	}

	taskDeadline := req.deadline
	if policy == Capture {
		// A capture is a read-only copy of the source token. It may narrow
		// authority, but it may never outlive the token it copied.
		taskDeadline = source.deadline
	}
	if cfg.deadlineSet {
		taskDeadline = cfg.deadline
	}
	if taskDeadline.After(req.deadline) {
		return nil, ErrDeadlineExceedsRequest
	}
	if policy == Capture && taskDeadline.After(source.deadline) {
		return nil, ErrDeadlineExceedsToken
	}
	if !h.clock.Now().Before(taskDeadline) {
		return nil, ErrDeadlineInPast
	}

	snapshot := Snapshot{}
	chosen := Identification
	switch policy {
	case NoIdentity:
		if cfg.deadlineSet && cfg.deadline.After(req.deadline) {
			return nil, ErrDeadlineExceedsRequest
		}
	case Capture:
		chosen = Identification
		if cfg.levelSet {
			chosen = cfg.captureLevel
		}
		if !chosen.Valid() {
			return nil, ErrInvalidLevel
		}
		if chosen > source.level {
			return nil, ErrCaptureEscalation
		}
		if chosen > Identification {
			if cfg.grant == nil {
				return nil, ErrGrantRequired
			}
			if err := h.verifyGrantLocked(cfg.grant, req, chosen, taskDeadline); err != nil {
				return nil, err
			}
		}
		snapshot = Snapshot{
			name:         h.primary.Name,
			connectionID: req.conn.id,
			requestID:    req.id,
			generation:   req.generation,
			level:        chosen,
			deadline:     taskDeadline,
		}
	default:
		return nil, ErrInvalidPolicy
	}

	task := &Task{
		host:         h,
		id:           h.nextTask(),
		request:      req,
		connectionID: req.conn.id,
		requestID:    req.id,
		generation:   req.generation,
		policy:       policy,
		snapshot:     snapshot,
		deadline:     taskDeadline,
		grant:        cfg.grant,
		fn:           fn,
		state:        taskPending,
	}
	h.tasks[task.id] = task
	req.tasks[task.id] = task
	return task, nil
}

// Run executes the callback once. It revalidates the handle at callback start;
// TaskContext.Access revalidates again before every resource call. A panic is
// propagated after the task state is made terminal.
func (t *Task) Run(ctx context.Context) (err error) {
	if t == nil || t.host == nil {
		return ErrInvalidToken
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h := t.host

	h.mu.Lock()
	if err := h.verifyTaskLocked(t); err != nil {
		h.mu.Unlock()
		return err
	}
	if t.state != taskPending {
		h.mu.Unlock()
		if t.state == taskRunning {
			return ErrTaskAlreadyRun
		}
		return ErrTaskFinished
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		t.state = taskCanceled
		t.err = ctxErr
		h.mu.Unlock()
		return ctxErr
	}
	t.state = taskRunning
	h.mu.Unlock()

	tctx := &TaskContext{host: h, task: t, ctx: ctx, snapshot: t.snapshot}
	defer func() {
		recovered := recover()
		h.mu.Lock()
		if t.state == taskRunning {
			t.state = taskDone
		}
		h.mu.Unlock()
		if recovered != nil {
			panic(recovered)
		}
	}()

	err = t.fn(ctx, tctx)

	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil && t.state == taskRunning && !h.clock.Now().Before(t.deadline) {
		t.state = taskExpired
		err = ErrTaskExpired
	}
	if err == nil {
		switch t.state {
		case taskRevoked:
			err = ErrTaskRevoked
		case taskExpired:
			err = ErrTaskExpired
		}
	}
	if t.state == taskRunning {
		t.state = taskDone
	}
	if err != nil {
		t.err = err
	}
	return err
}

// Start is an alias for Run.
func (t *Task) Start(ctx context.Context) error {
	return t.Run(ctx)
}

// TaskContext is the only resource-call surface exposed to a callback.
type TaskContext struct {
	host     *Host
	task     *Task
	ctx      context.Context
	snapshot Snapshot
}

func (c *TaskContext) Context() context.Context {
	if c == nil || c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// Identity returns the immutable snapshot, including after revocation. Use
// ReadIdentity when the caller needs the revocation check as well.
func (c *TaskContext) Identity() Snapshot {
	if c == nil {
		return Snapshot{}
	}
	return c.snapshot
}

func (c *TaskContext) HasIdentity() bool {
	return c != nil && c.task != nil && c.task.policy == Capture
}

func (c *TaskContext) Verify() error {
	if c == nil || c.task == nil {
		return ErrInvalidToken
	}
	if c.ctx != nil {
		if err := c.ctx.Err(); err != nil {
			return err
		}
	}
	return c.task.Verify()
}

func (c *TaskContext) ReadIdentity() (Snapshot, error) {
	if c == nil || c.task == nil {
		return Snapshot{}, ErrInvalidToken
	}
	if c.ctx != nil {
		if err := c.ctx.Err(); err != nil {
			return Snapshot{}, err
		}
	}
	if err := c.task.Verify(); err != nil {
		return Snapshot{}, err
	}
	if c.task.policy != Capture {
		return Snapshot{}, ErrNoIdentity
	}
	return c.snapshot, nil
}

// Check revalidates the task handle and authority level for one resource call.
func (c *TaskContext) Check(target ResourceKind) error {
	if c == nil || c.task == nil || c.host == nil {
		return ErrInvalidToken
	}
	if c.ctx != nil {
		if err := c.ctx.Err(); err != nil {
			return err
		}
	}
	c.host.mu.Lock()
	defer c.host.mu.Unlock()
	if err := c.host.verifyTaskLocked(c.task); err != nil {
		return err
	}
	if c.task.state != taskRunning {
		return ErrTaskNotRunning
	}
	if c.task.policy != Capture {
		return ErrNoIdentity
	}
	return checkLevel(c.task.snapshot.level, target)
}

// Access is the callback-wide resource entry point. It checks immediately
// before the call, so a request ending between two calls revokes the second.
func (c *TaskContext) Access(target ResourceKind, call func() error) error {
	if err := c.Check(target); err != nil {
		return err
	}
	if call == nil {
		return nil
	}
	return call()
}

func (c *TaskContext) Local(call func() error) error {
	return c.Access(LocalResource, call)
}

func (c *TaskContext) Remote(call func() error) error {
	return c.Access(RemoteResource, call)
}
