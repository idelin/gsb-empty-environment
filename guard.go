package netsec

import (
	"container/list"
	"fmt"
	"sync"
	"time"
)

const (
	defaultMaxLabels     = 32
	defaultMaxTenants    = 10_000
	defaultAlertRingSize = 256
	defaultAlertCooldown = time.Minute
	maxTenantIDLength    = 256
)

// Config configures a Guard. The zero value is not usable; construct guards
// with NewGuard which applies safe defaults and validates the result.
type Config struct {
	// Keys maps every permitted label key to its policy. Label keys absent
	// from this map are rejected.
	Keys map[string]KeyPolicy
	// MaxLabels bounds how many labels a single metric may carry.
	// Defaults to 32 when zero.
	MaxLabels int
	// MaxSeriesPerTenant bounds the number of distinct label sets (series)
	// any single tenant may create. Must be positive.
	MaxSeriesPerTenant int
	// RatePerSecond and Burst form the per-tenant token-bucket limiter.
	// RatePerSecond must be positive; Burst defaults to int(RatePerSecond)+1
	// when zero and must be positive.
	RatePerSecond float64
	Burst         int
	// MaxTenants bounds the number of tenants the guard tracks. When the
	// table is full, the least-recently-used tenant is evicted. Defaults to
	// 10000 when zero.
	MaxTenants int
	// AnomalyWindow and AnomalySeriesThreshold enable cardinality-anomaly
	// detection: creating more than AnomalySeriesThreshold new series within
	// AnomalyWindow raises AlertCardinalityAnomaly. Detection is disabled when
	// either value is zero.
	AnomalySeriesThreshold int
	AnomalyWindow          time.Duration
	// AlertRingSize bounds how many alerts DrainAlerts retains. Defaults to
	// 256 when zero.
	AlertRingSize int
	// AlertCooldown suppresses repeated alerts of the same kind for a tenant.
	// Defaults to one minute when zero.
	AlertCooldown time.Duration
	// Clock is injected for testing; nil uses wall time.
	Clock Clock
}

// Guard is the importable label-constraint component. It is safe for
// concurrent use by multiple goroutines.
//
// Every bounded resource the Guard owns (per-tenant series, tenant table,
// alert ring, rate-limiter state) has an explicit upper bound, so a flood of
// unique labels cannot grow its memory without limit.
type Guard struct {
	cfg   Config
	clock Clock

	mu      sync.RWMutex
	tenants map[string]*tenantState
	lru     *list.List
	alerts  *alertRing
}

type tenantState struct {
	id        string
	series    map[uint64]struct{}
	limiter   *tokenBucket
	createdAt []time.Time
	lastAlert map[AlertKind]time.Time
	elem      *list.Element
}

// NewGuard validates cfg and returns a ready Guard.
func NewGuard(cfg Config) (*Guard, error) {
	if len(cfg.Keys) == 0 {
		return nil, fmt.Errorf("%w: at least one key policy is required", ErrConfig)
	}
	if cfg.MaxSeriesPerTenant <= 0 {
		return nil, fmt.Errorf("%w: MaxSeriesPerTenant must be positive", ErrConfig)
	}
	if cfg.RatePerSecond <= 0 {
		return nil, fmt.Errorf("%w: RatePerSecond must be positive", ErrConfig)
	}
	if cfg.Burst < 0 {
		return nil, fmt.Errorf("%w: Burst must not be negative", ErrConfig)
	}
	if cfg.MaxLabels < 0 || cfg.MaxTenants < 0 || cfg.AlertRingSize < 0 {
		return nil, fmt.Errorf("%w: numeric limits must not be negative", ErrConfig)
	}
	if cfg.AnomalyWindow < 0 {
		return nil, fmt.Errorf("%w: AnomalyWindow must not be negative", ErrConfig)
	}
	for key, policy := range cfg.Keys {
		if key == "" {
			return nil, fmt.Errorf("%w: label key must not be empty", ErrConfig)
		}
		if policy.MaxLength < 0 || policy.BucketCount < 0 {
			return nil, fmt.Errorf("%w: policy for %q has a negative limit", ErrConfig, key)
		}
		if policy.Policy == Bucket && policy.Whitelist == nil && policy.BucketCount <= 0 {
			return nil, fmt.Errorf("%w: bucket policy for %q requires BucketCount", ErrConfig, key)
		}
	}

	if cfg.MaxLabels == 0 {
		cfg.MaxLabels = defaultMaxLabels
	}
	if cfg.MaxTenants == 0 {
		cfg.MaxTenants = defaultMaxTenants
	}
	if cfg.AlertRingSize == 0 {
		cfg.AlertRingSize = defaultAlertRingSize
	}
	if cfg.AlertCooldown == 0 {
		cfg.AlertCooldown = defaultAlertCooldown
	}
	if cfg.Burst == 0 {
		cfg.Burst = int(cfg.RatePerSecond) + 1
	}
	clock := cfg.Clock
	if clock == nil {
		clock = systemClock{}
	}

	return &Guard{
		cfg:     cfg,
		clock:   clock,
		tenants: make(map[string]*tenantState),
		lru:     list.New(),
		alerts:  newAlertRing(cfg.AlertRingSize),
	}, nil
}

// Admit validates labels for a tenant and returns the normalized label set
// plus a stable series fingerprint when the request is allowed.
//
// Rejections return one of the sentinel errors (see errors.go) and a nil
// label slice. Bucketed labels are returned with their bounded bucket value.
func (g *Guard) Admit(tenant string, labels map[string]string) ([]Label, uint64, error) {
	if len(tenant) == 0 || len(tenant) > maxTenantIDLength {
		return nil, 0, ErrTenantInvalid
	}
	if len(labels) > g.cfg.MaxLabels {
		return nil, 0, ErrTooManyLabels
	}

	normalized := make([]Label, 0, len(labels))
	for key, value := range labels {
		policy, ok := g.cfg.Keys[key]
		if !ok {
			return nil, 0, fmt.Errorf("%w: %q", ErrUnknownKey, key)
		}
		v, err := policy.normalize(key, value)
		if err != nil {
			return nil, 0, err
		}
		normalized = append(normalized, Label{Key: key, Value: v})
	}
	canonicalize(normalized)
	fp := fingerprint(normalized)
	now := g.clock.Now()

	g.mu.Lock()
	defer g.mu.Unlock()

	tenantState := g.getTenant(tenant, now)
	if !tenantState.limiter.allow(now) {
		g.alertLocked(tenantState, Alert{
			Time:   now,
			Tenant: tenant,
			Kind:   AlertRateLimit,
			Detail: "token bucket exhausted",
		})
		return nil, 0, ErrRateLimited
	}
	if err := tenantState.registerSeries(g, fp, now); err != nil {
		return nil, 0, err
	}
	return normalized, fp, nil
}

// getTenant returns the state for tenant, creating it if necessary. When the
// tenant table is full the least-recently-used tenant is evicted first.
// Callers must hold g.mu.
func (g *Guard) getTenant(tenant string, now time.Time) *tenantState {
	if elem, ok := g.tenants[tenant]; ok {
		g.lru.MoveToFront(elem.elem)
		return elem
	}

	if g.lru.Len() >= g.cfg.MaxTenants {
		g.evictOldestTenant(now)
	}

	state := &tenantState{
		id:        tenant,
		series:    make(map[uint64]struct{}),
		limiter:   newTokenBucket(g.cfg.RatePerSecond, g.cfg.Burst, now),
		lastAlert: make(map[AlertKind]time.Time),
	}
	state.elem = g.lru.PushFront(state)
	g.tenants[tenant] = state
	return state
}

// evictOldestTenant drops the LRU tenant so the tenant table cannot exceed
// MaxTenants entries. Callers must hold g.mu.
func (g *Guard) evictOldestTenant(now time.Time) {
	back := g.lru.Back()
	if back == nil {
		return
	}
	victim := back.Value.(*tenantState)
	g.lru.Remove(back)
	delete(g.tenants, victim.id)
	g.alerts.push(Alert{
		Time:    now,
		Tenant:  victim.id,
		Kind:    AlertTenantsEvicted,
		Detail:  "tenant table full; evicted least-recently-used tenant",
		Tenants: g.lru.Len(),
	})
}

// registerSeries records a new series fingerprint, enforcing the per-tenant
// series quota and cardinality-anomaly detection. Callers must hold g.mu.
func (s *tenantState) registerSeries(g *Guard, fp uint64, now time.Time) error {
	if _, exists := s.series[fp]; exists {
		return nil
	}
	if len(s.series) >= g.cfg.MaxSeriesPerTenant {
		g.alertLocked(s, Alert{
			Time:   now,
			Tenant: s.id,
			Kind:   AlertSeriesQuota,
			Detail: fmt.Sprintf("cap=%d reached", g.cfg.MaxSeriesPerTenant),
			Series: len(s.series),
		})
		return ErrSeriesQuota
	}

	s.series[fp] = struct{}{}
	s.noteAnomaly(g, now)
	return nil
}

// noteAnomaly prunes the sliding window of series-creation timestamps and
// raises an alert when creation velocity exceeds the configured threshold.
func (s *tenantState) noteAnomaly(g *Guard, now time.Time) {
	if g.cfg.AnomalyWindow <= 0 || g.cfg.AnomalySeriesThreshold <= 0 {
		return
	}
	cutoff := now.Add(-g.cfg.AnomalyWindow)
	kept := s.createdAt[:0]
	for _, t := range s.createdAt {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	s.createdAt = kept

	if len(kept) > g.cfg.AnomalySeriesThreshold {
		g.alertLocked(s, Alert{
			Time:   now,
			Tenant: s.id,
			Kind:   AlertCardinalityAnomaly,
			Detail: fmt.Sprintf(
				"%d new series within %s (threshold=%d)",
				len(kept), g.cfg.AnomalyWindow, g.cfg.AnomalySeriesThreshold),
			Series: len(s.series),
		})
	}
}

// alertLocked pushes an alert unless the tenant recently raised the same kind.
// Callers must hold g.mu.
func (g *Guard) alertLocked(s *tenantState, a Alert) {
	if last, ok := s.lastAlert[a.Kind]; ok && a.Time.Sub(last) < g.cfg.AlertCooldown {
		return
	}
	s.lastAlert[a.Kind] = a.Time
	g.alerts.push(a)
}

// DrainAlerts returns and clears buffered alerts in chronological order. It
// never blocks and at most Config.AlertRingSize alerts are returned.
func (g *Guard) DrainAlerts() []Alert {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := g.alerts.snapshot()
	g.alerts = newAlertRing(g.cfg.AlertRingSize)
	return out
}

// TenantCount reports how many tenants currently have tracked state.
func (g *Guard) TenantCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.tenants)
}

// SeriesCount reports the number of distinct series currently tracked for a
// tenant. Unknown tenants report zero.
func (g *Guard) SeriesCount(tenant string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if s, ok := g.tenants[tenant]; ok {
		return len(s.series)
	}
	return 0
}
