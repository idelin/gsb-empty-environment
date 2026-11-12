package netsec

import (
	"crypto/rand"
	"errors"
	"math/big"
	"sync"
	"time"
)

// Sentinel errors returned by the state machine. Identity hosts can map
// these onto HTTP / OAuth error responses.
var (
	// ErrUnknownGrant is returned when no grant matches the code.
	ErrUnknownGrant = errors.New("netsec: unknown grant or code")
	// ErrCodeSpaceExhausted is returned when a unique code cannot be issued
	// within the configured number of collision retries.
	ErrCodeSpaceExhausted = errors.New("netsec: code space exhausted")
	// ErrAlreadyClaimed is returned when a short code is presented more
	// than once: only the first successful claim counts.
	ErrAlreadyClaimed = errors.New("netsec: user code already claimed")
	// ErrGrantExpired is returned when a terminal user action targets a
	// grant whose lifetime has already elapsed.
	ErrGrantExpired = errors.New("netsec: grant expired")
	// ErrTerminal is returned when a user action targets a grant that is
	// already in a terminal state.
	ErrTerminal = errors.New("netsec: grant already in terminal state")
	// ErrPollingTooFast is returned by Poll before the grant's earliest
	// next-poll instant. The caller must wait and retry without the
	// authorization lifetime being extended.
	ErrPollingTooFast = errors.New("netsec: polling too fast")
)

const (
	defaultLifetime     = 10 * time.Minute
	defaultPollInterval = 5 * time.Second
	defaultCodeAttempts = 8
	userCodeAlphabet    = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	userCodeLength      = 8
	deviceCodeLength    = 32
	deviceCodeAlphabet  = "0123456789abcdef"
)

// CodeGenerator produces candidate identifier strings. Implementations do
// not need to be unique on their own: the manager retries on collision.
type CodeGenerator func() (string, error)

// ManagerConfig parameterizes a Manager.
type ManagerConfig struct {
	Clock         Clock
	Lifetime      time.Duration
	PollInterval  time.Duration
	UserCodeGen   CodeGenerator
	DeviceCodeGen CodeGenerator
	// CodeAttempts bounds collision retries when issuing codes.
	CodeAttempts int
}

// Option customizes a Manager at construction time.
type Option func(*ManagerConfig)

// WithClock overrides the clock (mainly for tests).
func WithClock(c Clock) Option { return func(cfg *ManagerConfig) { cfg.Clock = c } }

// WithLifetime overrides the grant lifetime. Slow polls never extend it.
func WithLifetime(d time.Duration) Option {
	return func(cfg *ManagerConfig) { cfg.Lifetime = d }
}

// WithPollInterval overrides the base polling interval. Each slow_down adds
// this interval to the earliest next-poll instant.
func WithPollInterval(d time.Duration) Option {
	return func(cfg *ManagerConfig) { cfg.PollInterval = d }
}

// WithCodeGenerators overrides the device and user code generators.
func WithCodeGenerators(device, user CodeGenerator) Option {
	return func(cfg *ManagerConfig) {
		cfg.DeviceCodeGen = device
		cfg.UserCodeGen = user
	}
}

// WithCodeAttempts bounds collision retries.
func WithCodeAttempts(n int) Option {
	return func(cfg *ManagerConfig) { cfg.CodeAttempts = n }
}

// Manager is the in-memory device-authorization state machine. It is safe
// for concurrent use.
type Manager struct {
	cfg ManagerConfig

	mu         sync.Mutex
	byDevice   map[string]*grant
	byUserCode map[string]*grant
}

// NewManager builds a Manager with the given options.
func NewManager(opts ...Option) *Manager {
	cfg := ManagerConfig{
		Clock:         SystemClock{},
		Lifetime:      defaultLifetime,
		PollInterval:  defaultPollInterval,
		UserCodeGen:   randomUserCode,
		DeviceCodeGen: randomDeviceCode,
		CodeAttempts:  defaultCodeAttempts,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Manager{
		cfg:        cfg,
		byDevice:   make(map[string]*grant),
		byUserCode: make(map[string]*grant),
	}
}

// CreateGrant issues a fresh grant. Device code (presented by the
// headless device) and user short code (presented by a human on another
// device) live in separate namespaces: a collision in one index never
// shadows the other, and each index retries independently.
func (m *Manager) CreateGrant() (GrantView, error) {
	now := m.cfg.Clock.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	deviceCode, err := m.uniqueCode(m.cfg.DeviceCodeGen, m.byDevice)
	if err != nil {
		return GrantView{}, err
	}
	userCode, err := m.uniqueCode(m.cfg.UserCodeGen, m.byUserCode)
	if err != nil {
		return GrantView{}, err
	}

	g := &grant{
		deviceCode: deviceCode,
		userCode:   userCode,
		state:      StatePending,
		createdAt:  now,
		expiresAt:  now.Add(m.cfg.Lifetime),
		// The first poll is allowed immediately after issuance.
		nextPollAt: now,
		pollStep:   m.cfg.PollInterval,
	}
	m.byDevice[deviceCode] = g
	m.byUserCode[userCode] = g
	return g.snapshot(), nil
}

// uniqueCode must be called with m.mu held so that the collision check and
// index insertion cannot interleave with a concurrent CreateGrant.
func (m *Manager) uniqueCode(gen CodeGenerator, index map[string]*grant) (string, error) {
	for attempt := 0; attempt < m.cfg.CodeAttempts; attempt++ {
		code, err := gen()
		if err != nil {
			return "", err
		}
		if _, exists := index[code]; !exists {
			return code, nil
		}
	}
	return "", ErrCodeSpaceExhausted
}

// Poll is called by the device using its device code.
//
// The rate-limit check and the update of the next-poll instant happen
// under the grant's own lock, so concurrent poll requests cannot observe
// the same "due" instant and bypass the interval. A too-fast poll pushes
// the next allowed poll out by one full interval (RFC 8628 slow_down) and
// returns ErrPollingTooFast; it never extends the grant's expiry.
func (m *Manager) Poll(deviceCode string) (GrantView, error) {
	g, err := m.lookupByDevice(deviceCode)
	if err != nil {
		return GrantView{}, err
	}

	now := m.cfg.Clock.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	if view, done := m.finalizeIfExpiredLocked(g, now); done {
		return view, nil
	}
	if g.state.IsTerminal() {
		return g.snapshot(), nil
	}
	if now.Before(g.nextPollAt) {
		// Backpressure: slow the client down rather than extending the
		// authorization window.
		g.nextPollAt = g.nextPollAt.Add(m.cfg.PollInterval)
		return GrantView{}, ErrPollingTooFast
	}

	// A well-paced poll schedules the next attempt one interval later.
	g.nextPollAt = now.Add(g.pollStep)
	return g.snapshot(), nil
}

// Claim is the first step of the user interaction: a human presents the
// short code on a browser-equipped device. A code can be claimed exactly
// once; repeat presentations fail with ErrAlreadyClaimed and reveal no
// grant data.
func (m *Manager) Claim(userCode string) (GrantView, error) {
	g, err := m.lookupByUserCode(userCode)
	if err != nil {
		return GrantView{}, err
	}

	now := m.cfg.Clock.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	if view, done := m.finalizeIfExpiredLocked(g, now); done {
		return view, ErrGrantExpired
	}
	if g.state.IsTerminal() {
		return GrantView{}, ErrTerminal
	}
	if g.state == StateClaimed {
		return GrantView{}, ErrAlreadyClaimed
	}
	g.state = StateClaimed
	return g.snapshot(), nil
}

// Approve records the user's allow decision. It works on a claimed grant;
// it may also be called on a still-pending grant when the host performs
// claim and consent in one step.
func (m *Manager) Approve(userCode string) (GrantView, error) {
	return m.decide(userCode, StateApproved)
}

// Deny records the user's reject decision.
func (m *Manager) Deny(userCode string) (GrantView, error) {
	return m.decide(userCode, StateDenied)
}

func (m *Manager) decide(userCode string, target GrantState) (GrantView, error) {
	g, err := m.lookupByUserCode(userCode)
	if err != nil {
		return GrantView{}, err
	}

	now := m.cfg.Clock.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	if view, done := m.finalizeIfExpiredLocked(g, now); done {
		return view, ErrGrantExpired
	}
	if g.state.IsTerminal() {
		return g.snapshot(), ErrTerminal
	}
	g.state = target
	return g.snapshot(), nil
}

// SweepExpired transitions every non-terminal grant whose lifetime has
// elapsed to StateExpired and returns the number of grants finalized.
// Expiry is also applied lazily on any interaction, so hosts do not have
// to run sweeps for correctness.
func (m *Manager) SweepExpired() int {
	now := m.cfg.Clock.Now()

	m.mu.Lock()
	grants := make([]*grant, 0, len(m.byDevice))
	for _, g := range m.byDevice {
		grants = append(grants, g)
	}
	m.mu.Unlock()

	finalized := 0
	for _, g := range grants {
		g.mu.Lock()
		if !g.state.IsTerminal() && now.After(g.expiresAt) {
			g.state = StateExpired
			finalized++
		}
		g.mu.Unlock()
	}
	return finalized
}

// finalizeIfExpiredLocked applies lazy expiry. The boundary is inclusive:
// the lifetime includes its expiry instant, so an approval recorded at
// exactly that instant is honored; once the clock moves past it, the only
// observable state is the expired terminal state. Must be called with
// g.mu held.
func (m *Manager) finalizeIfExpiredLocked(g *grant, now time.Time) (GrantView, bool) {
	if !g.state.IsTerminal() && now.After(g.expiresAt) {
		g.state = StateExpired
		return g.snapshot(), true
	}
	return GrantView{}, false
}

func (m *Manager) lookupByDevice(code string) (*grant, error) {
	m.mu.Lock()
	g := m.byDevice[code]
	m.mu.Unlock()
	if g == nil {
		return nil, ErrUnknownGrant
	}
	return g, nil
}

func (m *Manager) lookupByUserCode(code string) (*grant, error) {
	m.mu.Lock()
	g := m.byUserCode[code]
	m.mu.Unlock()
	if g == nil {
		return nil, ErrUnknownGrant
	}
	return g, nil
}

func randomUserCode() (string, error) {
	return randomString(userCodeAlphabet, userCodeLength)
}

func randomDeviceCode() (string, error) {
	return randomString(deviceCodeAlphabet, deviceCodeLength)
}

func randomString(alphabet string, length int) (string, error) {
	buf := make([]byte, length)
	limit := big.NewInt(int64(len(alphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		buf[i] = alphabet[n.Int64()]
	}
	return string(buf), nil
}
