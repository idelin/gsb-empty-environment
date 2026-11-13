package netsec

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// Config configures a Ledger.
//
// The callback fields accept a few common shapes for compatibility:
// ImportPolicy and VRPValid may be func(Route) bool, func(Route) error,
// func(RouteView) bool, or func(Route, uint64) bool.  A nil callback allows
// all routes.  Selector may be a Selector or func([]RouteView) (RouteView,
// bool).  Clock may be a Clock, *VirtualClock, or func() time.Time.
type Config struct {
	// RestartTime is the maximum time a stale route may remain forwardable.
	// RestartLimit, GracefulRestartTime and StaleTime are accepted aliases.
	RestartTime         time.Duration
	RestartLimit        time.Duration
	GracefulRestartTime time.Duration
	StaleTime           time.Duration
	RestartWindow       time.Duration
	MaxRestartTime      time.Duration
	RestartTimeout      time.Duration

	Clock         any
	TimeSource    any
	Selector      any
	Select        any
	RouteSelector any

	ImportPolicy any
	Policy       any
	Import       any
	PolicyCheck  any
	VRPValid     any
	VRP          any
	ValidateVRP  any
	VRPCheck     any

	PolicyVersion PolicyVersion
	VRPVersion    VRPVersion

	OnEvent any
}

type peerKey struct {
	neighborAS ASN
	routerID   RouterID
}

type sessionKey struct {
	peerKey
	generation uint64
}

type routeKey struct {
	peerKey
	prefix  Prefix
	routeID RouteID
}

type staleKey struct {
	peerKey
	prefix     Prefix
	generation uint64
	routeID    RouteID
}

type routeEntry struct {
	route         Route
	peer          Peer
	generation    uint64
	policyVersion PolicyVersion
	vrpVersion    VRPVersion
	installedAt   time.Time
}

type staleEntry struct {
	route                 Route
	peer                  Peer
	generation            uint64
	originalPolicyVersion PolicyVersion
	originalVRPVersion    VRPVersion
	validatedPolicy       PolicyVersion
	validatedVRP          VRPVersion
	staleSince            time.Time
	deadline              time.Time
}

func (e *routeEntry) view() RouteView {
	return RouteView{
		ID:                         e.route.ID,
		RouteID:                    e.route.ID,
		Prefix:                     e.route.Prefix,
		AttrsDigest:                e.route.AttrsDigest,
		Attrs:                      e.route.AttrsDigest,
		Attributes:                 e.route.AttrsDigest,
		Peer:                       e.peer,
		NeighborAS:                 e.peer.NeighborAS,
		RouterID:                   e.peer.RouterID,
		TransportGeneration:        e.generation,
		SessionGeneration:          e.generation,
		State:                      StateFresh,
		Forwardable:                true,
		Exportable:                 true,
		FlowSpecEligible:           true,
		PolicyVersion:              e.policyVersion,
		VRPVersion:                 e.vrpVersion,
		OriginalPolicyVersion:      e.policyVersion,
		OriginalVRPVersion:         e.vrpVersion,
		LastValidatedPolicyVersion: e.policyVersion,
		LastValidatedVRPVersion:    e.vrpVersion,
		InstalledAt:                e.installedAt,
	}
}

func (e *staleEntry) view() RouteView {
	return RouteView{
		ID:                         e.route.ID,
		RouteID:                    e.route.ID,
		Prefix:                     e.route.Prefix,
		AttrsDigest:                e.route.AttrsDigest,
		Peer:                       e.peer,
		NeighborAS:                 e.peer.NeighborAS,
		RouterID:                   e.peer.RouterID,
		TransportGeneration:        e.generation,
		SessionGeneration:          e.generation,
		State:                      StateStale,
		Stale:                      true,
		Forwardable:                true,
		Exportable:                 false,
		FlowSpecEligible:           false,
		StaleSince:                 e.staleSince,
		Deadline:                   e.deadline,
		ExpiresAt:                  e.deadline,
		PolicyVersion:              e.originalPolicyVersion,
		VRPVersion:                 e.originalVRPVersion,
		OriginalPolicyVersion:      e.originalPolicyVersion,
		OriginalVRPVersion:         e.originalVRPVersion,
		LastValidatedPolicyVersion: e.validatedPolicy,
		LastValidatedVRPVersion:    e.validatedVRP,
	}
}

func (e *staleEntry) public() StaleRoute {
	return StaleRoute{
		RouteID:                    e.route.ID,
		ID:                         e.route.ID,
		Prefix:                     e.route.Prefix,
		AttrsDigest:                e.route.AttrsDigest,
		Attrs:                      e.route.AttrsDigest,
		Attributes:                 e.route.AttrsDigest,
		Peer:                       e.peer,
		NeighborAS:                 e.peer.NeighborAS,
		RouterID:                   e.peer.RouterID,
		TransportGeneration:        e.generation,
		SessionGeneration:          e.generation,
		PolicyVersion:              e.originalPolicyVersion,
		VRPVersion:                 e.originalVRPVersion,
		OriginalPolicyVersion:      e.originalPolicyVersion,
		OriginalVRPVersion:         e.originalVRPVersion,
		OriginalPolicy:             e.originalPolicyVersion,
		OriginalVRP:                e.originalVRPVersion,
		LastValidatedPolicyVersion: e.validatedPolicy,
		LastValidatedVRPVersion:    e.validatedVRP,
		StaleSince:                 e.staleSince,
		Deadline:                   e.deadline,
		StaleUntil:                 e.deadline,
		ExpiresAt:                  e.deadline,
		State:                      StateStale,
		Forwardable:                true,
		Exportable:                 false,
		FlowSpecEligible:           false,
	}
}

// Ledger is the graceful-restart state machine.  It is safe for concurrent
// use.  Callbacks should be pure and must not synchronously call methods on the
// same Ledger.
type Ledger struct {
	mu sync.Mutex

	clock       Clock
	restartTime time.Duration
	selector    Selector
	policyCheck func(Route, PolicyVersion) bool
	vrpCheck    func(Route, VRPVersion) bool
	onEvent     func(Event)

	policyVersion PolicyVersion
	vrpVersion    VRPVersion

	sessions   map[peerKey]*Session
	lastGen    map[peerKey]uint64
	hasLastGen map[peerKey]bool
	routes     map[routeKey]*routeEntry
	stale      map[staleKey]*staleEntry
	history    []Withdrawal
}

// NewLedger builds a ledger.  It accepts Config, *Config, time.Duration,
// Clock, *VirtualClock and raw callbacks, in any order.
func NewLedger(args ...any) *Ledger {
	cfg := Config{}
	for _, arg := range args {
		switch v := arg.(type) {
		case Config:
			mergeConfig(&cfg, v)
		case *Config:
			if v != nil {
				mergeConfig(&cfg, *v)
			}
		case time.Duration:
			if cfg.RestartTime == 0 {
				cfg.RestartTime = v
			}
		case Clock, *VirtualClock, func() time.Time:
			if cfg.Clock == nil {
				cfg.Clock = v
			}
		case Selector:
			if cfg.Selector == nil {
				cfg.Selector = v
			}
		}
	}

	restart := firstNonZeroDuration(
		cfg.RestartTime,
		cfg.RestartLimit,
		cfg.GracefulRestartTime,
		cfg.StaleTime,
		cfg.RestartWindow,
		cfg.MaxRestartTime,
		cfg.RestartTimeout,
	)
	if restart < 0 {
		restart = 0
	}

	l := &Ledger{
		clock:         clockFrom(firstNonNil(cfg.Clock, cfg.TimeSource)),
		restartTime:   restart,
		policyCheck:   adaptRouteCheck(firstNonNil(cfg.ImportPolicy, cfg.Policy, cfg.Import, cfg.PolicyCheck)),
		vrpCheck:      adaptRouteCheck(firstNonNil(cfg.VRPValid, cfg.VRP, cfg.ValidateVRP, cfg.VRPCheck)),
		onEvent:       adaptEventSink(cfg.OnEvent),
		sessions:      make(map[peerKey]*Session),
		lastGen:       make(map[peerKey]uint64),
		hasLastGen:    make(map[peerKey]bool),
		routes:        make(map[routeKey]*routeEntry),
		stale:         make(map[staleKey]*staleEntry),
		policyVersion: cfg.PolicyVersion,
		vrpVersion:    cfg.VRPVersion,
	}
	l.selector = adaptSelector(firstNonNil(cfg.Selector, cfg.Select, cfg.RouteSelector))
	return l
}

// New is an alias for NewLedger.
func New(args ...any) *Ledger { return NewLedger(args...) }

func mergeConfig(dst *Config, src Config) {
	if dst.RestartTime == 0 {
		dst.RestartTime = src.RestartTime
	}
	if dst.RestartLimit == 0 {
		dst.RestartLimit = src.RestartLimit
	}
	if dst.GracefulRestartTime == 0 {
		dst.GracefulRestartTime = src.GracefulRestartTime
	}
	if dst.StaleTime == 0 {
		dst.StaleTime = src.StaleTime
	}
	if dst.RestartWindow == 0 {
		dst.RestartWindow = src.RestartWindow
	}
	if dst.MaxRestartTime == 0 {
		dst.MaxRestartTime = src.MaxRestartTime
	}
	if dst.RestartTimeout == 0 {
		dst.RestartTimeout = src.RestartTimeout
	}
	if dst.Clock == nil {
		dst.Clock = src.Clock
	}
	if dst.TimeSource == nil {
		dst.TimeSource = src.TimeSource
	}
	if dst.Selector == nil {
		dst.Selector = src.Selector
	}
	if dst.Select == nil {
		dst.Select = src.Select
	}
	if dst.RouteSelector == nil {
		dst.RouteSelector = src.RouteSelector
	}
	if dst.ImportPolicy == nil {
		dst.ImportPolicy = src.ImportPolicy
	}
	if dst.Policy == nil {
		dst.Policy = src.Policy
	}
	if dst.Import == nil {
		dst.Import = src.Import
	}
	if dst.PolicyCheck == nil {
		dst.PolicyCheck = src.PolicyCheck
	}
	if dst.VRPValid == nil {
		dst.VRPValid = src.VRPValid
	}
	if dst.VRP == nil {
		dst.VRP = src.VRP
	}
	if dst.ValidateVRP == nil {
		dst.ValidateVRP = src.ValidateVRP
	}
	if dst.VRPCheck == nil {
		dst.VRPCheck = src.VRPCheck
	}
	if dst.PolicyVersion == 0 {
		dst.PolicyVersion = src.PolicyVersion
	}
	if dst.VRPVersion == 0 {
		dst.VRPVersion = src.VRPVersion
	}
	if dst.OnEvent == nil {
		dst.OnEvent = src.OnEvent
	}
}

func firstNonZeroDuration(values ...time.Duration) time.Duration {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

func firstNonNil(values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func adaptRouteCheck(fn any) func(Route, uint64) bool {
	if isNilCallback(fn) {
		return func(Route, uint64) bool { return true }
	}
	switch f := fn.(type) {
	case nil:
		return func(Route, uint64) bool { return true }
	case func(Route) bool:
		return func(r Route, _ uint64) bool { return f(r) }
	case func(*Route) bool:
		return func(r Route, _ uint64) bool { return f(&r) }
	case func(Route, uint64) bool:
		return f
	case func(RouteView) bool:
		return func(r Route, _ uint64) bool { return f(routeAsView(r)) }
	case func(string) bool:
		return func(r Route, _ uint64) bool { return f(r.Prefix) }
	case func(Route) error:
		return func(r Route, _ uint64) bool { return f(r) == nil }
	case func(*Route) error:
		return func(r Route, _ uint64) bool { return f(&r) == nil }
	case func(Route, uint64) error:
		return func(r Route, v uint64) bool { return f(r, v) == nil }
	default:
		return func(Route, uint64) bool { return true }
	}
}

func adaptSelector(fn any) Selector {
	if isNilCallback(fn) {
		return defaultSelector
	}
	switch f := fn.(type) {
	case nil:
		return defaultSelector
	case Selector:
		return f
	case func(RouteUse, []RouteView) (RouteView, bool):
		return Selector(f)
	case func([]RouteView) (RouteView, bool):
		return func(_ RouteUse, candidates []RouteView) (RouteView, bool) {
			return f(candidates)
		}
	case func([]RouteView) RouteView:
		return func(_ RouteUse, candidates []RouteView) (RouteView, bool) {
			if len(candidates) == 0 {
				return RouteView{}, false
			}
			v := f(candidates)
			for _, c := range candidates {
				if sameRouteView(c, v) {
					return c, true
				}
			}
			return RouteView{}, false
		}
	case func([]RouteView) *RouteView:
		return func(_ RouteUse, candidates []RouteView) (RouteView, bool) {
			v := f(candidates)
			if v == nil {
				return RouteView{}, false
			}
			for _, c := range candidates {
				if sameRouteView(c, *v) {
					return c, true
				}
			}
			return RouteView{}, false
		}
	case func(string, []RouteView) (RouteView, bool):
		return func(_ RouteUse, candidates []RouteView) (RouteView, bool) {
			if len(candidates) == 0 {
				return RouteView{}, false
			}
			return f(candidates[0].Prefix, candidates)
		}
	case func([]RouteView) int:
		return func(_ RouteUse, candidates []RouteView) (RouteView, bool) {
			i := f(candidates)
			if i < 0 || i >= len(candidates) {
				return RouteView{}, false
			}
			return candidates[i], true
		}
	default:
		return defaultSelector
	}
}

func adaptEventSink(fn any) func(Event) {
	if isNilCallback(fn) {
		return nil
	}
	switch f := fn.(type) {
	case nil:
		return nil
	case func(Event):
		return f
	case func(EventType, Event):
		return func(e Event) { f(e.Type, e) }
	case func(string, Event):
		return func(e Event) { f(string(e.Type), e) }
	case func(any):
		return func(e Event) { f(e) }
	default:
		return nil
	}
}

func isNilCallback(fn any) bool {
	if fn == nil {
		return true
	}
	v := reflect.ValueOf(fn)
	return v.Kind() == reflect.Func && v.IsNil()
}

func defaultSelector(_ RouteUse, candidates []RouteView) (RouteView, bool) {
	if len(candidates) == 0 {
		return RouteView{}, false
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		if betterCandidate(c, best) {
			best = c
		}
	}
	return best, true
}

func betterCandidate(a, b RouteView) bool {
	// A fresh route always wins over a stale one.  This prevents stale paths
	// from being preferred merely because their old attributes look better.
	if a.State != b.State {
		return a.State == StateFresh
	}
	if a.Peer.NeighborAS != b.Peer.NeighborAS {
		return a.Peer.NeighborAS < b.Peer.NeighborAS
	}
	if a.Peer.RouterID != b.Peer.RouterID {
		return a.Peer.RouterID < b.Peer.RouterID
	}
	return a.RouteID < b.RouteID
}

func routeAsView(r Route) RouteView {
	return RouteView{
		ID:          r.ID,
		RouteID:     r.ID,
		Prefix:      r.Prefix,
		AttrsDigest: r.AttrsDigest,
		State:       StateFresh,
	}
}

// SetSelector replaces the route-selection callback.
func (l *Ledger) SetSelector(selector any) {
	l.mu.Lock()
	l.selector = adaptSelector(selector)
	l.mu.Unlock()
}

// SetRestartTime changes the maximum stale lifetime.  Existing stale routes
// keep their original deadline.
func (l *Ledger) SetRestartTime(d time.Duration) {
	if d < 0 {
		d = 0
	}
	l.mu.Lock()
	l.restartTime = d
	l.mu.Unlock()
}

// RestartTime returns the configured maximum stale lifetime.
func (l *Ledger) RestartTime() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.restartTime
}

// Now returns the ledger's current virtual time.
func (l *Ledger) Now() time.Time {
	return l.clock.Now()
}

// Advance advances a virtual clock (when present), then expires all stale
// routes whose deadline has been reached.
func (l *Ledger) Advance(d time.Duration) {
	if c, ok := l.clock.(advancingClock); ok {
		c.Advance(d)
	}
	l.mu.Lock()
	l.pruneLocked(l.clock.Now())
	l.mu.Unlock()
}

// AdvanceTo moves a virtual clock to t (when supported) and prunes due stale
// routes.
func (l *Ledger) AdvanceTo(t time.Time) {
	if c, ok := l.clock.(*VirtualClock); ok {
		c.Set(t)
	}
	l.mu.Lock()
	l.pruneLocked(l.clock.Now())
	l.mu.Unlock()
}

// Tick expires all due stale routes without changing the clock.
func (l *Ledger) Tick() {
	l.mu.Lock()
	l.pruneLocked(l.clock.Now())
	l.mu.Unlock()
}

func (l *Ledger) nowLocked() time.Time { return l.clock.Now() }

func (l *Ledger) checkLocked(r Route) (policyOK, vrpOK bool) {
	policyOK = l.policyCheck(r, l.policyVersion)
	vrpOK = l.vrpCheck(r, l.vrpVersion)
	return
}

func (l *Ledger) invalidReasonLocked(r Route) (WithdrawReason, bool) {
	policyOK, vrpOK := l.checkLocked(r)
	switch {
	case !policyOK:
		return WithdrawPolicy, false
	case !vrpOK:
		return WithdrawVRP, false
	default:
		return "", true
	}
}

func (l *Ledger) pruneLocked(now time.Time) []RouteID {
	l.expireLocked(now)
	return l.revalidateStaleLocked(now)
}

func (l *Ledger) expireLocked(now time.Time) []RouteID {
	keys := make([]staleKey, 0, len(l.stale))
	for k := range l.stale {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return staleKeyLess(keys[i], keys[j]) })

	var expired []RouteID
	for _, k := range keys {
		e := l.stale[k]
		if now.Before(e.deadline) {
			continue
		}
		l.removeStaleLocked(k, WithdrawDeadline, now, true)
		expired = append(expired, e.route.ID)
	}
	return expired
}

func (l *Ledger) revalidateStaleLocked(now time.Time) []RouteID {
	keys := make([]staleKey, 0, len(l.stale))
	for k := range l.stale {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return staleKeyLess(keys[i], keys[j]) })

	var removed []RouteID
	for _, k := range keys {
		e := l.stale[k]
		reason, ok := l.invalidReasonLocked(e.route)
		if ok {
			e.validatedPolicy = l.policyVersion
			e.validatedVRP = l.vrpVersion
			continue
		}
		l.removeStaleLocked(k, reason, now, false)
		removed = append(removed, e.route.ID)
	}
	return removed
}

func (l *Ledger) removeStaleLocked(k staleKey, reason WithdrawReason, now time.Time, expired bool) {
	e, ok := l.stale[k]
	if !ok {
		return
	}
	delete(l.stale, k)
	l.history = append(l.history, Withdrawal{
		RouteID:       e.route.ID,
		Prefix:        e.route.Prefix,
		AttrsDigest:   e.route.AttrsDigest,
		Peer:          e.peer,
		Generation:    e.generation,
		Reason:        reason,
		At:            now,
		PolicyVersion: l.policyVersion,
		VRPVersion:    l.vrpVersion,
		WasStale:      true,
		Deadline:      e.deadline,
	})
	evt := Event{
		Type:          EventWithdrawn,
		At:            now,
		RouteID:       e.route.ID,
		Prefix:        e.route.Prefix,
		AttrsDigest:   e.route.AttrsDigest,
		Peer:          e.peer,
		Generation:    e.generation,
		Reason:        reason,
		Deadline:      e.deadline,
		PolicyVersion: l.policyVersion,
		VRPVersion:    l.vrpVersion,
	}
	if expired {
		evt.Type = EventExpired
	}
	l.emitLocked(evt)
}

func (l *Ledger) removeFreshLocked(k routeKey, reason WithdrawReason, now time.Time) {
	e, ok := l.routes[k]
	if !ok {
		return
	}
	delete(l.routes, k)
	l.history = append(l.history, Withdrawal{
		RouteID:       e.route.ID,
		Prefix:        e.route.Prefix,
		AttrsDigest:   e.route.AttrsDigest,
		Peer:          e.peer,
		Generation:    e.generation,
		Reason:        reason,
		At:            now,
		PolicyVersion: e.policyVersion,
		VRPVersion:    e.vrpVersion,
	})
	l.emitLocked(Event{
		Type:          EventWithdrawn,
		At:            now,
		RouteID:       e.route.ID,
		Prefix:        e.route.Prefix,
		AttrsDigest:   e.route.AttrsDigest,
		Peer:          e.peer,
		Generation:    e.generation,
		Reason:        reason,
		PolicyVersion: e.policyVersion,
		VRPVersion:    e.vrpVersion,
	})
}

func (l *Ledger) emitLocked(e Event) {
	if l.onEvent != nil {
		l.onEvent(e)
	}
}

func staleKeyLess(a, b staleKey) bool {
	if a.neighborAS != b.neighborAS {
		return a.neighborAS < b.neighborAS
	}
	if a.routerID != b.routerID {
		return a.routerID < b.routerID
	}
	if a.prefix != b.prefix {
		return a.prefix < b.prefix
	}
	if a.generation != b.generation {
		return a.generation < b.generation
	}
	return a.routeID < b.routeID
}

// PolicyVersion returns the current import policy revision.
func (l *Ledger) PolicyVersion() PolicyVersion {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.policyVersion
}

// VRPVersion returns the current VRP revision.
func (l *Ledger) VRPVersion() VRPVersion {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.vrpVersion
}

// UpdatePolicy installs a new import-policy revision and immediately
// revalidates every stale route.  A nil validator keeps the previous one.
func (l *Ledger) UpdatePolicy(version PolicyVersion, validator ...any) []RouteID {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowLocked()
	l.policyVersion = version
	for _, s := range l.sessions {
		s.identity.PolicyVersion = version
	}
	if len(validator) > 0 && validator[0] != nil {
		l.policyCheck = adaptRouteCheck(validator[0])
	}
	l.emitLocked(Event{Type: EventPolicyUpdate, At: now, PolicyVersion: l.policyVersion, VRPVersion: l.vrpVersion})
	l.expireLocked(now)
	return l.revalidateStaleLocked(now)
}

// SetPolicyVersion is UpdatePolicy with the existing validator.
func (l *Ledger) SetPolicyVersion(version PolicyVersion) []RouteID {
	return l.UpdatePolicy(version)
}

// PolicyUpdated is an alias for UpdatePolicy.
func (l *Ledger) PolicyUpdated(version PolicyVersion, validator ...any) []RouteID {
	return l.UpdatePolicy(version, validator...)
}

// UpdateVRP installs a new VRP revision and immediately revalidates every
// stale route.  A nil validator keeps the previous one.
func (l *Ledger) UpdateVRP(version VRPVersion, validator ...any) []RouteID {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowLocked()
	l.vrpVersion = version
	for _, s := range l.sessions {
		s.identity.VRPVersion = version
	}
	if len(validator) > 0 && validator[0] != nil {
		l.vrpCheck = adaptRouteCheck(validator[0])
	}
	l.emitLocked(Event{Type: EventVRPUpdate, At: now, PolicyVersion: l.policyVersion, VRPVersion: l.vrpVersion})
	l.expireLocked(now)
	return l.revalidateStaleLocked(now)
}

// SetVRPVersion is UpdateVRP with the existing validator.
func (l *Ledger) SetVRPVersion(version VRPVersion) []RouteID {
	return l.UpdateVRP(version)
}

// VRPUpdated is an alias for UpdateVRP.
func (l *Ledger) VRPUpdated(version VRPVersion, validator ...any) []RouteID {
	return l.UpdateVRP(version, validator...)
}

func (l *Ledger) addFreshLocked(r Route, peer Peer, generation uint64, now time.Time) {
	key := routeKey{peerKey: peer.key(), prefix: r.Prefix, routeID: r.ID}
	l.routes[key] = &routeEntry{
		route:         r,
		peer:          peer,
		generation:    generation,
		policyVersion: l.policyVersion,
		vrpVersion:    l.vrpVersion,
		installedAt:   now,
	}
}

func (l *Ledger) findRefreshTargetLocked(peer Peer, generation uint64, r Route, beforeEOR bool) (staleKey, *staleEntry, bool) {
	if !beforeEOR {
		return staleKey{}, nil, false
	}
	keys := make([]staleKey, 0)
	for k := range l.stale {
		if k.peerKey != peer.key() || k.generation >= generation {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return staleKey{}, nil, false
	}
	sort.Slice(keys, func(i, j int) bool { return staleKeyLess(keys[i], keys[j]) })

	if r.ID != "" {
		for _, k := range keys {
			if k.routeID == r.ID && k.prefix == r.Prefix {
				return k, l.stale[k], true
			}
		}
	}
	var prefixMatches []staleKey
	for _, k := range keys {
		e := l.stale[k]
		if e.route.Prefix != r.Prefix {
			continue
		}
		if r.AttrsDigest != "" && e.route.AttrsDigest == r.AttrsDigest {
			return k, e, true
		}
		prefixMatches = append(prefixMatches, k)
	}
	if len(prefixMatches) == 1 {
		k := prefixMatches[0]
		return k, l.stale[k], true
	}
	return staleKey{}, nil, false
}

func (l *Ledger) sessionForPeerLocked(peer Peer) *Session {
	return l.sessions[peer.key()]
}

func (l *Ledger) setSessionLocked(peer Peer, s *Session) {
	l.sessions[peer.key()] = s
	if s.identity.generation() > l.lastGen[peer.key()] {
		l.lastGen[peer.key()] = s.identity.generation()
	}
	l.hasLastGen[peer.key()] = true
}

func (l *Ledger) clearSessionLocked(peer Peer, s *Session) {
	if cur, ok := l.sessions[peer.key()]; ok && cur == s {
		delete(l.sessions, peer.key())
	}
}

// Sessions returns a snapshot of active sessions.
func (l *Ledger) Sessions() []SessionIdentity {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]SessionIdentity, 0, len(l.sessions))
	for _, s := range l.sessions {
		out = append(out, s.identity)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NeighborAS != out[j].NeighborAS {
			return out[i].NeighborAS < out[j].NeighborAS
		}
		if out[i].RouterID != out[j].RouterID {
			return out[i].RouterID < out[j].RouterID
		}
		return out[i].generation() < out[j].generation()
	})
	return out
}

func (l *Ledger) staleViewsLocked() []StaleRoute {
	out := make([]StaleRoute, 0, len(l.stale))
	for _, e := range l.stale {
		out = append(out, e.public())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NeighborAS != out[j].NeighborAS {
			return out[i].NeighborAS < out[j].NeighborAS
		}
		if out[i].RouterID != out[j].RouterID {
			return out[i].RouterID < out[j].RouterID
		}
		if out[i].SessionGeneration != out[j].SessionGeneration {
			return out[i].SessionGeneration < out[j].SessionGeneration
		}
		return out[i].RouteID < out[j].RouteID
	})
	return out
}

// StaleRoutes returns all currently active stale records after applying
// deadline expiry and current policy/VRP revalidation.
func (l *Ledger) StaleRoutes() []StaleRoute {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	return l.staleViewsLocked()
}

// Stale is an alias for StaleRoutes.
func (l *Ledger) Stale() []StaleRoute { return l.StaleRoutes() }

// StaleCount returns the number of live stale records after expiry and
// revalidation.
func (l *Ledger) StaleCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	return len(l.stale)
}

// IsStale reports whether route ID currently names a stale record.
func (l *Ledger) IsStale(id RouteID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	return l.hasStaleIDLocked(id)
}

// ForwardingTable returns fresh routes plus stale routes that are still
// forwardable.  The latter carry StateStale and a deadline.
func (l *Ledger) ForwardingTable() []RouteView {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowLocked()
	l.pruneLocked(now)
	out := make([]RouteView, 0, len(l.routes)+len(l.stale))
	for _, e := range l.routes {
		out = append(out, e.view())
	}
	for _, e := range l.stale {
		out = append(out, e.view())
	}
	sortViews(out)
	return out
}

// Forwarding is an alias for ForwardingTable.
func (l *Ledger) Forwarding() []RouteView { return l.ForwardingTable() }

// FreshRoutes returns only non-stale routes.
func (l *Ledger) FreshRoutes() []RouteView {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	out := make([]RouteView, 0, len(l.routes))
	for _, e := range l.routes {
		out = append(out, e.view())
	}
	sortViews(out)
	return out
}

// Routes is an alias for ForwardingTable.
func (l *Ledger) Routes() []RouteView { return l.ForwardingTable() }

func sortViews(out []RouteView) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Peer.NeighborAS != out[j].Peer.NeighborAS {
			return out[i].Peer.NeighborAS < out[j].Peer.NeighborAS
		}
		if out[i].Peer.RouterID != out[j].Peer.RouterID {
			return out[i].Peer.RouterID < out[j].Peer.RouterID
		}
		if out[i].RouteID != out[j].RouteID {
			return out[i].RouteID < out[j].RouteID
		}
		return out[i].State == StateFresh && out[j].State == StateStale
	})
}

// Route looks up a route by ID, preferring a fresh route.
func (l *Ledger) Route(id RouteID) (RouteView, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	if v, ok := l.freshViewByIDLocked(id); ok {
		return v, true
	}
	return l.staleViewByIDLocked(id)
}

// RouteFor looks up a route for a specific peer.
func (l *Ledger) RouteFor(peer Peer, id RouteID) (RouteView, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	for k, e := range l.routes {
		if k.peerKey == peer.key() && k.routeID == id {
			return e.view(), true
		}
	}
	for k, e := range l.stale {
		if k.peerKey == peer.key() && k.routeID == id {
			return e.view(), true
		}
	}
	return RouteView{}, false
}

func (l *Ledger) freshViewByIDLocked(id RouteID) (RouteView, bool) {
	var found *routeEntry
	for k, e := range l.routes {
		if k.routeID != id {
			continue
		}
		if found == nil || routeEntryLess(e, found) {
			found = e
		}
	}
	if found == nil {
		return RouteView{}, false
	}
	return found.view(), true
}

func (l *Ledger) staleViewByIDLocked(id RouteID) (RouteView, bool) {
	var found *staleEntry
	for k, e := range l.stale {
		if k.routeID != id {
			continue
		}
		if found == nil || staleEntryLess(e, found) {
			found = e
		}
	}
	if found == nil {
		return RouteView{}, false
	}
	return found.view(), true
}

func routeEntryLess(a, b *routeEntry) bool {
	if a.peer.NeighborAS != b.peer.NeighborAS {
		return a.peer.NeighborAS < b.peer.NeighborAS
	}
	if a.peer.RouterID != b.peer.RouterID {
		return a.peer.RouterID < b.peer.RouterID
	}
	return a.route.ID < b.route.ID
}

func staleEntryLess(a, b *staleEntry) bool {
	if a.peer.NeighborAS != b.peer.NeighborAS {
		return a.peer.NeighborAS < b.peer.NeighborAS
	}
	if a.peer.RouterID != b.peer.RouterID {
		return a.peer.RouterID < b.peer.RouterID
	}
	if a.generation != b.generation {
		return a.generation < b.generation
	}
	return a.route.ID < b.route.ID
}

// CanForward reports whether a route is currently eligible for local
// forwarding.  Stale routes are eligible only before their deadline and only
// while the current policy and VRP still accept them.
func (l *Ledger) CanForward(id RouteID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	if _, ok := l.freshViewByIDLocked(id); ok {
		return true
	}
	_, ok := l.staleViewByIDLocked(id)
	return ok
}

// CanExport reports whether a route may be used as a basis for export to
// another neighbor.  Stale routes are never exportable.
func (l *Ledger) CanExport(id RouteID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	if l.hasStaleIDLocked(id) {
		return false
	}
	_, ok := l.freshViewByIDLocked(id)
	return ok
}

// CanFlowSpec reports whether a route may participate in FlowSpec validation.
// Stale routes are never eligible.
func (l *Ledger) CanFlowSpec(id RouteID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	if l.hasStaleIDLocked(id) {
		return false
	}
	_, ok := l.freshViewByIDLocked(id)
	return ok
}

// CanForwardFor, CanExportFor and CanFlowSpecFor are peer-qualified variants
// for hosts that allow the same route ID on multiple sessions.
func (l *Ledger) CanForwardFor(peer Peer, id RouteID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	for k := range l.routes {
		if k.peerKey == peer.key() && k.routeID == id {
			return true
		}
	}
	for k := range l.stale {
		if k.peerKey == peer.key() && k.routeID == id {
			return true
		}
	}
	return false
}

func (l *Ledger) CanExportFor(peer Peer, id RouteID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	for k := range l.stale {
		if k.peerKey == peer.key() && k.routeID == id {
			return false
		}
	}
	for k := range l.routes {
		if k.peerKey == peer.key() && k.routeID == id {
			return true
		}
	}
	return false
}

func (l *Ledger) CanFlowSpecFor(peer Peer, id RouteID) bool {
	return l.CanExportFor(peer, id)
}

// IsForwarding, IsExportable and IsFlowSpecEligible are compatibility aliases.
func (l *Ledger) IsForwarding(id RouteID) bool       { return l.CanForward(id) }
func (l *Ledger) IsExportable(id RouteID) bool       { return l.CanExport(id) }
func (l *Ledger) IsFlowSpecEligible(id RouteID) bool { return l.CanFlowSpec(id) }

// ValidateFlowSpec validates that id refers to a fresh route, not a stale one.
func (l *Ledger) ValidateFlowSpec(id RouteID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	if l.hasStaleIDLocked(id) {
		return fmt.Errorf("%w: %s", ErrStaleRouteForFlowSpec, id)
	}
	if _, ok := l.freshViewByIDLocked(id); ok {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRouteNotFound, id)
}

// ValidateExport validates that id may be exported to another neighbor.
func (l *Ledger) ValidateExport(id RouteID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	if l.hasStaleIDLocked(id) {
		return fmt.Errorf("%w: %s", ErrStaleRouteExport, id)
	}
	if _, ok := l.freshViewByIDLocked(id); ok {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrRouteNotFound, id)
}

func (l *Ledger) hasStaleIDLocked(id RouteID) bool {
	for k := range l.stale {
		if k.routeID == id {
			return true
		}
	}
	return false
}

// Candidates returns routes for prefix that are eligible for use, sorted
// deterministically (fresh before stale).
func (l *Ledger) Candidates(prefix Prefix, use RouteUse) []RouteView {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	return l.candidatesLocked(strings.TrimSpace(prefix), use)
}

func (l *Ledger) candidatesLocked(prefix Prefix, use RouteUse) []RouteView {
	out := make([]RouteView, 0)
	for _, e := range l.routes {
		if prefix != "" && e.route.Prefix != prefix {
			continue
		}
		out = append(out, e.view())
	}
	if use == UseForward {
		for _, e := range l.stale {
			if prefix != "" && e.route.Prefix != prefix {
				continue
			}
			out = append(out, e.view())
		}
	}
	sortViews(out)
	return out
}

// ExportCandidates returns fresh routes eligible for export.  The optional
// argument may be a target Peer, ASN, or prefix; stale routes are always
// excluded regardless of the filter.
func (l *Ledger) ExportCandidates(filter ...any) []RouteView {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())

	var peer *Peer
	var peerAS *ASN
	var prefix Prefix
	for _, f := range filter {
		switch v := f.(type) {
		case Peer:
			p := v
			peer = &p
		case *Peer:
			peer = v
		case ASN:
			as := v
			peerAS = &as
		case string:
			prefix = strings.TrimSpace(v)
		}
	}

	out := make([]RouteView, 0, len(l.routes))
	for _, e := range l.routes {
		if peer != nil && !peerMatchesFilter(e.peer, *peer) {
			continue
		}
		if peerAS != nil && e.peer.NeighborAS != *peerAS {
			continue
		}
		if prefix != "" && e.route.Prefix != prefix {
			continue
		}
		out = append(out, e.view())
	}
	sortViews(out)
	return out
}

func peerMatchesFilter(routePeer, filter Peer) bool {
	if filter.NeighborAS != 0 && routePeer.NeighborAS != filter.NeighborAS {
		return false
	}
	if filter.RouterID != "" && routePeer.RouterID != filter.RouterID {
		return false
	}
	return true
}

// FlowSpecCandidates returns fresh routes that may participate in FlowSpec
// validation.  Stale routes are excluded even if they are still forwardable.
func (l *Ledger) FlowSpecCandidates(prefix ...Prefix) []RouteView {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.nowLocked())
	var want Prefix
	if len(prefix) > 0 {
		want = strings.TrimSpace(prefix[0])
	}
	out := make([]RouteView, 0, len(l.routes))
	for _, e := range l.routes {
		if want != "" && e.route.Prefix != want {
			continue
		}
		out = append(out, e.view())
	}
	sortViews(out)
	return out
}

// SelectBest runs the host's route-selection callback over the eligible
// candidates for prefix and use.  Stale routes are only included for
// UseForward.  A selector result that is not one of the eligible candidates is
// rejected defensively.
func (l *Ledger) SelectBest(prefix Prefix, use ...RouteUse) (RouteView, bool) {
	u := UseForward
	if len(use) > 0 {
		u = use[0]
	}

	l.mu.Lock()
	l.pruneLocked(l.nowLocked())
	candidates := l.candidatesLocked(strings.TrimSpace(prefix), u)
	selector := l.selector
	l.mu.Unlock()

	if len(candidates) == 0 || selector == nil {
		return RouteView{}, false
	}
	chosen, ok := selector(u, append([]RouteView(nil), candidates...))
	if !ok {
		return RouteView{}, false
	}
	for _, c := range candidates {
		if sameRouteView(c, chosen) {
			return c, true
		}
	}
	return RouteView{}, false
}

// SelectRoute is an alias for SelectBest.
func (l *Ledger) SelectRoute(prefix Prefix, use ...RouteUse) (RouteView, bool) {
	return l.SelectBest(prefix, use...)
}

func sameRouteView(a, b RouteView) bool {
	return a.RouteID == b.RouteID &&
		a.Prefix == b.Prefix &&
		a.Peer == b.Peer &&
		a.TransportGeneration == b.TransportGeneration &&
		a.State == b.State
}

// Withdrawals returns the immutable audit history in chronological order.
func (l *Ledger) Withdrawals() []Withdrawal {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Withdrawal, len(l.history))
	copy(out, l.history)
	return out
}

// History is an alias for Withdrawals.
func (l *Ledger) History() []Withdrawal { return l.Withdrawals() }
