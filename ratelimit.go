package netsec

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Dimension is one independently enforced identity axis.
type Dimension string

const (
	DimensionAddress Dimension = "address"
	DimensionAccount Dimension = "account"
	DimensionDevice  Dimension = "device"
)

// Decision reasons.
const (
	ReasonRateLimited      = "rate_limited"
	ReasonUnknownAction    = "unknown_action"
	ReasonIdentityRequired = "identity_required"
)

// Policy controls one action's sliding window. Limit is the default for every
// dimension. A positive per-dimension value overrides it; zero disables that
// dimension when no default is configured.
type Policy struct {
	Window       time.Duration
	Limit        int
	AddressLimit int
	AccountLimit int
	DeviceLimit  int
}

func (p Policy) limitFor(dimension Dimension) int {
	var specific int
	switch dimension {
	case DimensionAddress:
		specific = p.AddressLimit
	case DimensionAccount:
		specific = p.AccountLimit
	case DimensionDevice:
		specific = p.DeviceLimit
	}
	if specific > 0 {
		return specific
	}
	return p.Limit
}

func (p Policy) validate() error {
	if p.Window <= 0 {
		return fmt.Errorf("netsec: policy window must be positive")
	}
	if p.Limit < 0 || p.AddressLimit < 0 || p.AccountLimit < 0 || p.DeviceLimit < 0 {
		return fmt.Errorf("netsec: policy limits must not be negative")
	}
	if p.limitFor(DimensionAddress) == 0 &&
		p.limitFor(DimensionAccount) == 0 &&
		p.limitFor(DimensionDevice) == 0 {
		return fmt.Errorf("netsec: policy must enable at least one dimension")
	}
	return nil
}

// Decision is the result of a rate limit check.
type Decision struct {
	Allowed    bool
	Action     string
	Identity   Identity
	BlockedBy  []Dimension
	RetryAfter time.Duration
	Remaining  int
	Reason     string
}

// DeniedBy reports whether dimension is one of the blocking dimensions.
func (d Decision) DeniedBy(dimension Dimension) bool {
	for _, blocked := range d.BlockedBy {
		if blocked == dimension {
			return true
		}
	}
	return false
}

type bucketKey struct {
	action    string
	dimension Dimension
	value     string
}

type bucket struct {
	events   []time.Time
	lastSeen time.Time
}

// Limiter enforces a sliding-window policy for each action. Address, account
// and device counters are independent, and a request is allowed only when all
// applicable dimensions are below their limits.
type Limiter struct {
	mu       sync.Mutex
	policies map[string]Policy
	buckets  map[bucketKey]*bucket
	now      func() time.Time
	ops      uint64
}

// LimiterOption configures a Limiter.
type LimiterOption func(*Limiter)

// WithClock replaces the clock used by Allow and Check.
func WithClock(now func() time.Time) LimiterOption {
	return func(l *Limiter) {
		if now != nil {
			l.now = now
		}
	}
}

// NewLimiter creates a limiter from action to policy mappings.
func NewLimiter(policies map[string]Policy, opts ...LimiterOption) (*Limiter, error) {
	l := &Limiter{
		policies: make(map[string]Policy, len(policies)),
		buckets:  make(map[bucketKey]*bucket),
		now:      time.Now,
	}
	for action, policy := range policies {
		action = strings.TrimSpace(action)
		if action == "" {
			return nil, fmt.Errorf("netsec: action must not be empty")
		}
		if err := policy.validate(); err != nil {
			return nil, fmt.Errorf("netsec: invalid policy for action %q: %w", action, err)
		}
		l.policies[action] = policy
	}
	for _, opt := range opts {
		opt(l)
	}
	return l, nil
}

// MustNewLimiter is NewLimiter for static configuration.
func MustNewLimiter(policies map[string]Policy, opts ...LimiterOption) *Limiter {
	l, err := NewLimiter(policies, opts...)
	if err != nil {
		panic(err)
	}
	return l
}

// Allow checks and records an event using the configured clock.
func (l *Limiter) Allow(action string, identity Identity) Decision {
	return l.AllowAt(action, identity, l.now())
}

// Check is an alias for Allow.
func (l *Limiter) Check(action string, identity Identity) Decision {
	return l.Allow(action, identity)
}

// AllowAt checks and records an event at now.
func (l *Limiter) AllowAt(action string, identity Identity, now time.Time) Decision {
	action = strings.TrimSpace(action)
	identity = canonicalIdentity(identity)
	decision := Decision{
		Action:   action,
		Identity: identity,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	policy, ok := l.policies[action]
	if !ok {
		decision.Reason = ReasonUnknownAction
		return decision
	}
	if now.IsZero() {
		now = l.now()
	}
	if identity.Address == "" {
		decision.Reason = ReasonIdentityRequired
		return decision
	}

	l.ops++
	if l.ops%1024 == 0 {
		l.sweepLocked(now)
	}

	type activeDimension struct {
		dimension Dimension
		key       bucketKey
		limit     int
		window    time.Duration
		existing  *bucket
		count     int
	}
	dimensions := make([]activeDimension, 0, 3)
	for _, dimension := range []Dimension{DimensionAddress, DimensionAccount, DimensionDevice} {
		value, ok := dimensionValue(identity, dimension)
		if !ok {
			continue
		}
		limit := policy.limitFor(dimension)
		if limit <= 0 {
			continue
		}
		key := bucketKey{action: action, dimension: dimension, value: value}
		current := l.buckets[key]
		count := 0
		if current != nil {
			pruneBucket(current, now, policy.Window)
			count = len(current.events)
		}
		dimensions = append(dimensions, activeDimension{
			dimension: dimension,
			key:       key,
			limit:     limit,
			window:    policy.Window,
			existing:  current,
			count:     count,
		})
	}

	var retryAfter time.Duration
	for _, dimension := range dimensions {
		if dimension.count < dimension.limit {
			continue
		}
		decision.BlockedBy = append(decision.BlockedBy, dimension.dimension)
		wait := retryAfterFor(dimension.existing, now, dimension.window)
		if wait > retryAfter {
			retryAfter = wait
		}
	}
	if len(decision.BlockedBy) > 0 {
		decision.RetryAfter = retryAfter
		decision.Reason = ReasonRateLimited
		return decision
	}

	remaining := -1
	for _, dimension := range dimensions {
		current := dimension.existing
		if current == nil {
			current = &bucket{}
			l.buckets[dimension.key] = current
		}
		current.events = append(current.events, now)
		current.lastSeen = now
		left := dimension.limit - len(current.events)
		if remaining < 0 || left < remaining {
			remaining = left
		}
	}
	if remaining < 0 {
		remaining = 0
	}
	decision.Allowed = true
	decision.Remaining = remaining
	return decision
}

func retryAfterFor(bucket *bucket, now time.Time, window time.Duration) time.Duration {
	if bucket == nil || len(bucket.events) == 0 {
		return 0
	}
	wait := bucket.events[0].Add(window).Sub(now)
	if wait < 0 {
		return 0
	}
	return wait
}

func pruneBucket(bucket *bucket, now time.Time, window time.Duration) {
	if bucket == nil {
		return
	}
	cutoff := now.Add(-window)
	first := 0
	for first < len(bucket.events) && !bucket.events[first].After(cutoff) {
		first++
	}
	if first == 0 {
		return
	}
	if first == len(bucket.events) {
		bucket.events = bucket.events[:0]
		return
	}
	copy(bucket.events, bucket.events[first:])
	bucket.events = bucket.events[:len(bucket.events)-first]
}

func (l *Limiter) sweepLocked(now time.Time) {
	for key, bucket := range l.buckets {
		policy, ok := l.policies[key.action]
		if !ok {
			delete(l.buckets, key)
			continue
		}
		pruneBucket(bucket, now, policy.Window)
		if len(bucket.events) == 0 && !bucket.lastSeen.After(now.Add(-policy.Window)) {
			delete(l.buckets, key)
		}
	}
}

func dimensionValue(identity Identity, dimension Dimension) (string, bool) {
	var value string
	switch dimension {
	case DimensionAddress:
		value = identity.Address
	case DimensionAccount:
		value = identity.Account
	case DimensionDevice:
		value = identity.Device
	default:
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func canonicalIdentity(identity Identity) Identity {
	identity.Address = strings.TrimSpace(identity.Address)
	if ip, err := parseIP(identity.Address); err == nil {
		identity.Address = ip.String()
	}
	identity.Account = cleanDimensionValue(identity.Account)
	identity.Device = cleanDimensionValue(identity.Device)
	return identity
}

// SetPolicy validates and atomically replaces one action's policy. Existing
// counters remain keyed by the same action and are interpreted using the new
// window and limits.
func (l *Limiter) SetPolicy(action string, policy Policy) error {
	action = strings.TrimSpace(action)
	if action == "" {
		return fmt.Errorf("netsec: action must not be empty")
	}
	if err := policy.validate(); err != nil {
		return err
	}
	l.mu.Lock()
	l.policies[action] = policy
	l.mu.Unlock()
	return nil
}

// Policy returns the policy configured for action.
func (l *Limiter) Policy(action string) (Policy, bool) {
	action = strings.TrimSpace(action)
	l.mu.Lock()
	defer l.mu.Unlock()
	policy, ok := l.policies[action]
	return policy, ok
}

// Reload replaces all policies atomically. Existing counters are retained and
// interpreted using the new policy set.
func (l *Limiter) Reload(policies map[string]Policy) error {
	next := make(map[string]Policy, len(policies))
	for action, policy := range policies {
		action = strings.TrimSpace(action)
		if action == "" {
			return fmt.Errorf("netsec: action must not be empty")
		}
		if err := policy.validate(); err != nil {
			return fmt.Errorf("netsec: invalid policy for action %q: %w", action, err)
		}
		next[action] = policy
	}
	l.mu.Lock()
	l.policies = next
	l.mu.Unlock()
	return nil
}

// Reset clears all counters for an identity across every action.
func (l *Limiter) Reset(identity Identity) {
	identity = canonicalIdentity(identity)
	values := map[Dimension]string{
		DimensionAddress: identity.Address,
		DimensionAccount: identity.Account,
		DimensionDevice:  identity.Device,
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.buckets {
		if values[key.dimension] == key.value {
			delete(l.buckets, key)
		}
	}
}

// ResetAll clears every counter.
func (l *Limiter) ResetAll() {
	l.mu.Lock()
	l.buckets = make(map[bucketKey]*bucket)
	l.mu.Unlock()
}

// Sweep removes expired counters.
func (l *Limiter) Sweep(now time.Time) {
	l.mu.Lock()
	l.sweepLocked(now)
	l.mu.Unlock()
}

// Guard combines identity resolution and rate limiting.
type Guard struct {
	Resolver *Resolver
	Limiter  *Limiter
}

// NewGuard creates a resolver/limiter pair.
func NewGuard(resolver *Resolver, limiter *Limiter) (*Guard, error) {
	if resolver == nil || limiter == nil {
		return nil, fmt.Errorf("netsec: guard requires a resolver and limiter")
	}
	return &Guard{Resolver: resolver, Limiter: limiter}, nil
}

// Check resolves req and applies the limiter's action policy.
func (g *Guard) Check(req *http.Request, action string) (Identity, Decision, error) {
	identity, err := g.identity(req)
	if err != nil {
		return identity, Decision{Action: action, Reason: ReasonIdentityRequired}, err
	}
	return identity, g.Limiter.Allow(action, identity), nil
}

// CheckAt resolves req and applies the limiter's action policy at now.
func (g *Guard) CheckAt(req *http.Request, action string, now time.Time) (Identity, Decision, error) {
	identity, err := g.identity(req)
	if err != nil {
		return identity, Decision{Action: action, Reason: ReasonIdentityRequired}, err
	}
	return identity, g.Limiter.AllowAt(action, identity, now), nil
}

func (g *Guard) identity(req *http.Request) (Identity, error) {
	if req == nil {
		return Identity{}, fmt.Errorf("netsec: nil request")
	}
	if identity, ok := IdentityFromContext(req.Context()); ok {
		StripForwardingHeaders(req.Header)
		return identity, nil
	}
	return g.Resolver.Sanitize(req)
}
