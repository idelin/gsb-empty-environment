package netsec

import "sync"

// Limits bounds per-tenant watch fan-out.
type Limits struct {
	// MaxWatches is the maximum number of concurrently open watches per
	// tenant. Zero or negative values fall back to the default.
	MaxWatches int
	// EventsPerSecond caps events delivered to all watches of one
	// tenant. A value of zero disables rate limiting.
	EventsPerSecond float64
	// EventBurst is the token-bucket burst size.
	EventBurst int
}

// Authorizer is the importable watch-authorization component. A key-value
// service creates one Authorizer, registers each tenant with its namespace
// prefixes, routes every write through Publish and opens watches through
// Watch.
//
// All methods are safe for concurrent use.
type Authorizer struct {
	mu      sync.Mutex
	tenants map[string]*tenant
	log     *eventLog
	limits  Limits
	clock   Clock
	chanCap int
	closed  bool
	nextID  uint64
}

type tenant struct {
	name     string
	prefixes []string
	bucket   *tokenBucket
	watches  map[uint64]*Watch
	dropped  int64
}

// Watch is an authorized, prefix-scoped subscription. Its event channel
// only ever carries events whose keys are inside both the watch prefix
// and the tenant's current namespace.
type Watch struct {
	auth   *Authorizer
	id     uint64
	tenant string
	prefix string
	ch     chan Event

	closeOnce sync.Once
	errMu     sync.RWMutex
	err       error
}

// Tenant returns the tenant that owns the watch.
func (w *Watch) Tenant() string { return w.tenant }

// Prefix returns the authorized watch prefix.
func (w *Watch) Prefix() string { return w.prefix }

// Events returns the receive side of the event channel. The channel is
// closed when the watch is canceled, closed, revoked by a permission
// change or shut down with the authorizer; inspect Err for the cause.
func (w *Watch) Events() <-chan Event { return w.ch }

// Err returns the terminal error after the event channel is closed, or
// nil while the watch is active or when it was closed by the caller.
func (w *Watch) Err() error {
	w.errMu.RLock()
	defer w.errMu.RUnlock()
	return w.err
}

// Close releases the watch and removes it from the tenant's watch count.
// It is idempotent and returns the terminal error, if any.
func (w *Watch) Close() error {
	w.auth.removeWatch(w, nil)
	return w.Err()
}

func (w *Watch) terminate(err error) {
	w.closeOnce.Do(func() {
		w.errMu.Lock()
		w.err = err
		w.errMu.Unlock()
		close(w.ch)
	})
}

func (a *Authorizer) removeWatch(w *Watch, cause error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, ok := a.tenants[w.tenant]; ok {
		delete(t.watches, w.id)
	}
	w.terminate(cause)
}

// Option customizes an Authorizer.
type Option func(*config)

type config struct {
	limits          Limits
	historyCapacity int
	channelCapacity int
	clock           Clock
}

// WithLimits overrides the default per-tenant limits.
func WithLimits(l Limits) Option {
	return func(c *config) { c.limits = l }
}

// WithHistoryCapacity sets how many historical events are retained for
// replay. The minimum capacity is one.
func WithHistoryCapacity(n int) Option {
	return func(c *config) { c.historyCapacity = n }
}

// WithChannelCapacity sets the buffered-event capacity of each watch
// channel. Replay events may grow a channel beyond this size temporarily.
func WithChannelCapacity(n int) Option {
	return func(c *config) { c.channelCapacity = n }
}

// WithClock injects a time source, primarily for deterministic tests.
func WithClock(clock Clock) Option {
	return func(c *config) { c.clock = clock }
}

const (
	defaultMaxWatches      = 16
	defaultEventsPerSecond = 100.0
	defaultEventBurst      = 200
	defaultHistoryCapacity = 1024
	defaultChannelCapacity = 16
)

// NewAuthorizer constructs an Authorizer with the given options.
func NewAuthorizer(opts ...Option) *Authorizer {
	cfg := config{
		limits: Limits{
			MaxWatches:      defaultMaxWatches,
			EventsPerSecond: defaultEventsPerSecond,
			EventBurst:      defaultEventBurst,
		},
		historyCapacity: defaultHistoryCapacity,
		channelCapacity: defaultChannelCapacity,
		clock:           SystemClock{},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	cfg.limits = normalizeLimits(cfg.limits)
	if cfg.historyCapacity < 1 {
		cfg.historyCapacity = 1
	}
	if cfg.channelCapacity < 1 {
		cfg.channelCapacity = 1
	}
	return &Authorizer{
		tenants: make(map[string]*tenant),
		log:     newEventLog(cfg.historyCapacity),
		limits:  cfg.limits,
		clock:   cfg.clock,
		chanCap: cfg.channelCapacity,
	}
}

func normalizeLimits(l Limits) Limits {
	if l.MaxWatches < 1 {
		l.MaxWatches = defaultMaxWatches
	}
	if l.EventBurst < 1 {
		l.EventBurst = defaultEventBurst
	}
	return l
}
