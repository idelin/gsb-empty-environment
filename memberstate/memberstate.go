// Package memberstate holds per-multicast-group listener state for a virtual
// switch data plane. It merges per-member include/exclude source filters into
// one exact, disjoint prefix cover without ever expanding prefixes into
// individual addresses, and attributes every allowed source to the members
// that authorize it.
package memberstate

import (
	"errors"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrStaleVersion rejects an update whose version is not strictly greater
	// than the incarnation currently held (or removed) for the member. It is
	// the fence used against reordered or late control messages.
	ErrStaleVersion = errors.New("memberstate: stale member version")
	// ErrLeaseExpired rejects a publication whose lease deadline is in the
	// past; a higher-version rejoin remains valid.
	ErrLeaseExpired = errors.New("memberstate: lease already expired")
	// ErrInvalidInput rejects malformed membership descriptors.
	ErrInvalidInput = errors.New("memberstate: invalid membership")
)

// TenantID identifies the trusted tenant bound to a member.
type TenantID string

// PortID identifies the port instance bound to a member.
type PortID string

// MemberKey uniquely identifies a listener by its trusted tenant and the
// concrete port instance it is bound to.
type MemberKey struct {
	Tenant TenantID
	Port   PortID
}

// Mode is the IGMPv3/MLDv2-style source filter mode of one member.
type Mode int

const (
	// Include permits only the listed source prefixes; an empty include list
	// is a silent listener that permits nothing.
	Include Mode = iota + 1
	// Exclude permits the whole address family except the listed prefixes.
	Exclude
)

func (m Mode) valid() bool {
	return m == Include || m == Exclude
}

// Membership is one control-plane publication for a member. Version is a
// monotonic incarnation counter: every accepted publication must carry a
// strictly greater version than the one currently stored for the member.
// LeaseDeadline is the absolute expiry instant; a zero deadline means the
// publication never expires on its own.
type Membership struct {
	Tenant        TenantID
	Port          PortID
	Mode          Mode
	Sources       []netip.Prefix
	Version       uint64
	LeaseDeadline time.Time
}

func (m Membership) key() MemberKey {
	return MemberKey{Tenant: m.Tenant, Port: m.Port}
}

// Evidence names the member and the matching filter rule that authorized one
// allowed source. For an exclude member, Rule is the complement piece that
// contains the source rather than any excluded prefix.
type Evidence struct {
	Tenant TenantID
	Port   PortID
	Mode   Mode
	Rule   netip.Prefix
}

// SourceDecision is the per-source verdict inside a query scope.
type SourceDecision struct {
	Source   netip.Addr
	Allowed  bool
	Evidence []Evidence
}

type memberState struct {
	membership Membership
	rules      []netip.Prefix // canonical, per-family sorted source list
	permitted4 *prefixSet     // this member's effective permit cover, IPv4
	permitted6 *prefixSet     // this member's effective permit cover, IPv6
	expired    bool
}

type snapshot struct {
	members map[MemberKey]*memberState
}

// Option configures a Group.
type Option func(*Group)

// WithClock injects a clock, mainly for deterministic lease tests.
func WithClock(now func() time.Time) Option {
	return func(g *Group) {
		g.now = now
	}
}

// Group stores the listener membership of one multicast group. Writers are
// serialized by mu; readers take an immutable snapshot through atomic
// pointer, so a mode switch is published atomically and never exposes
// half-applied state to the data plane.
type Group struct {
	mu   sync.Mutex
	snap atomic.Pointer[snapshot]
	now  func() time.Time
}

// NewGroup creates an empty group.
func NewGroup(opts ...Option) *Group {
	g := &Group{now: time.Now}
	for _, opt := range opts {
		opt(g)
	}
	g.snap.Store(&snapshot{members: map[MemberKey]*memberState{}})
	return g
}

func comparePrefix(a, b netip.Prefix) int {
	if c := a.Addr().Unmap().Compare(b.Addr().Unmap()); c != 0 {
		return c
	}
	return a.Bits() - b.Bits()
}

func compareKey(a, b MemberKey) int {
	switch {
	case a.Tenant < b.Tenant:
		return -1
	case a.Tenant > b.Tenant:
		return 1
	case a.Port < b.Port:
		return -1
	case a.Port > b.Port:
		return 1
	default:
		return 0
	}
}

func canonicalRules(in []netip.Prefix) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(in))
	for _, p := range in {
		if !p.Addr().IsValid() || p.Addr().Is4In6() {
			return nil, ErrInvalidInput
		}
		out = append(out, p.Masked())
	}
	slices.SortFunc(out, comparePrefix)
	return slices.Compact(out), nil
}

func splitFamilies(rules []netip.Prefix) (v4, v6 []netip.Prefix) {
	for _, p := range rules {
		if p.Addr().Unmap().Is4() {
			v4 = append(v4, p)
		} else {
			v6 = append(v6, p)
		}
	}
	return v4, v6
}

// buildPermitted turns one member's filter into its effective permit cover.
// Include: the listed prefixes. Exclude: the family-wide complement of the
// listed prefixes. Everything stays at prefix granularity.
func buildPermitted(mode Mode, rules []netip.Prefix) (*prefixSet, *prefixSet, error) {
	r4, r6 := splitFamilies(rules)
	s4 := newPrefixSet(fam4)
	s6 := newPrefixSet(fam6)

	switch mode {
	case Include:
		for _, p := range r4 {
			s4.insert(p)
		}
		for _, p := range r6 {
			s6.insert(p)
		}
	case Exclude:
		ex4 := newPrefixSet(fam4)
		for _, p := range r4 {
			ex4.insert(p)
		}
		s4 = complementOf(ex4)

		ex6 := newPrefixSet(fam6)
		for _, p := range r6 {
			ex6.insert(p)
		}
		s6 = complementOf(ex6)
	default:
		return nil, nil, ErrInvalidInput
	}
	return s4, s6, nil
}

// Update atomically publishes a member's membership (including a mode switch),
// or creates the member on first publication. Only this member's contribution
// is touched.
func (g *Group) Update(m Membership) error {
	if m.Tenant == "" || m.Port == "" || !m.Mode.valid() || m.Version == 0 {
		return ErrInvalidInput
	}
	rules, err := canonicalRules(m.Sources)
	if err != nil {
		return err
	}
	if !m.LeaseDeadline.IsZero() && !m.LeaseDeadline.After(g.now()) {
		return ErrLeaseExpired
	}
	p4, p6, err := buildPermitted(m.Mode, rules)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	prev := g.snap.Load()
	if existing, ok := prev.members[m.key()]; ok {
		if m.Version <= existing.membership.Version {
			return ErrStaleVersion
		}
	}
	next := make(map[MemberKey]*memberState, len(prev.members)+1)
	for k, v := range prev.members {
		next[k] = v
	}
	next[m.key()] = &memberState{
		membership: m,
		rules:      rules,
		permitted4: p4,
		permitted6: p6,
	}
	g.snap.Store(&snapshot{members: next})
	return nil
}

// Renew is a lease refresh for the current incarnation. It is accepted only
// while the member is still present and unexpired; a refresh arriving after
// expiry or carrying an old version is rejected, so late messages can never
// resurrect or extend a contribution.
func (g *Group) Renew(key MemberKey, version uint64, deadline time.Time) error {
	if key.Tenant == "" || key.Port == "" || version == 0 {
		return ErrInvalidInput
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	prev := g.snap.Load()
	existing, ok := prev.members[key]
	if !ok || existing.expired || version != existing.membership.Version {
		return ErrStaleVersion
	}
	if !deadline.IsZero() && !deadline.After(g.now()) {
		return ErrLeaseExpired
	}
	next := make(map[MemberKey]*memberState, len(prev.members))
	for k, v := range prev.members {
		next[k] = v
	}
	renewed := *existing
	renewed.membership.LeaseDeadline = deadline
	next[key] = &renewed
	g.snap.Store(&snapshot{members: next})
	return nil
}

// Leave removes exactly this member's contribution. The removed version is
// retained as a fence: an equal or older publication arriving late cannot
// recreate the member.
func (g *Group) Leave(key MemberKey, version uint64) error {
	if key.Tenant == "" || key.Port == "" || version == 0 {
		return ErrInvalidInput
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	prev := g.snap.Load()
	existing, ok := prev.members[key]
	if !ok || version < existing.membership.Version {
		return ErrStaleVersion
	}
	next := make(map[MemberKey]*memberState, len(prev.members))
	for k, v := range prev.members {
		if k != key {
			next[k] = v
		}
	}
	// Tombstone keeps the version fence without contributing any sources.
	next[key] = &memberState{
		membership: Membership{
			Tenant:  key.Tenant,
			Port:    key.Port,
			Version: version,
		},
		permitted4: newPrefixSet(fam4),
		permitted6: newPrefixSet(fam6),
		expired:    true,
	}
	g.snap.Store(&snapshot{members: next})
	return nil
}

// Expire retires leases whose deadline has passed, revoking only those
// members' contributions. It returns the affected member keys.
func (g *Group) Expire() []MemberKey {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()

	prev := g.snap.Load()
	var expired []MemberKey
	next := make(map[MemberKey]*memberState, len(prev.members))
	for k, v := range prev.members {
		if !v.expired && !v.membership.LeaseDeadline.IsZero() && !v.membership.LeaseDeadline.After(now) {
			retired := *v
			retired.expired = true
			retired.permitted4 = newPrefixSet(fam4)
			retired.permitted6 = newPrefixSet(fam6)
			next[k] = &retired
			expired = append(expired, k)
			continue
		}
		next[k] = v
	}
	if len(expired) == 0 {
		return nil
	}
	slices.SortFunc(expired, compareKey)
	g.snap.Store(&snapshot{members: next})
	return expired
}

func (g *Group) live(snap *snapshot, now time.Time) []*memberState {
	out := make([]*memberState, 0, len(snap.members))
	for _, m := range snap.members {
		if m.expired {
			continue
		}
		if d := m.membership.LeaseDeadline; !d.IsZero() && !d.After(now) {
			continue
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b *memberState) int {
		return compareKey(a.membership.key(), b.membership.key())
	})
	return out
}

func unionSets(live []*memberState, fam family) *prefixSet {
	union := newPrefixSet(fam)
	for _, m := range live {
		var cover []netip.Prefix
		if fam == fam4 {
			cover = m.permitted4.emit()
		} else {
			cover = m.permitted6.emit()
		}
		for _, p := range cover {
			union.insert(p)
		}
	}
	return union
}

// Allowed returns the exact, minimal, mutually disjoint prefix cover of the
// union of every live member's permit set, IPv4 prefixes followed by IPv6.
func (g *Group) Allowed() []netip.Prefix {
	snap := g.snap.Load()
	live := g.live(snap, g.now())
	out := unionSets(live, fam4).emit()
	out = append(out, unionSets(live, fam6).emit()...)
	return out
}

// AllowedWithin intersects the allowed cover with scope (typically a small
// data-plane universe) and returns the exact disjoint pieces inside it.
func (g *Group) AllowedWithin(scope netip.Prefix) ([]netip.Prefix, error) {
	if !scope.Addr().IsValid() || scope.Addr().Is4In6() {
		return nil, ErrInvalidInput
	}
	addr := scope.Addr().Unmap()
	scope = scope.Masked()
	snap := g.snap.Load()
	live := g.live(snap, g.now())

	var set *prefixSet
	if addr.Is4() {
		set = unionSets(live, fam4)
	} else {
		set = unionSets(live, fam6)
	}
	return set.intersect(scope, nil), nil
}

func matchingRule(m *memberState, src netip.Addr) (netip.Prefix, bool) {
	set := m.permitted4
	if src.Is6() {
		set = m.permitted6
	}
	if !set.containsAddr(src) {
		return netip.Prefix{}, false
	}
	cover := set.emit()
	for _, p := range cover {
		if p.Contains(src) {
			return p, true
		}
	}
	return netip.Prefix{}, false
}

// EvidenceFor returns every live member whose permit set covers src, together
// with the specific include prefix or exclude-complement piece that matches.
// A nil result means the source is denied to every member.
func (g *Group) EvidenceFor(src netip.Addr) []Evidence {
	src = src.Unmap()
	snap := g.snap.Load()
	live := g.live(snap, g.now())

	var out []Evidence
	for _, m := range live {
		rule, ok := matchingRule(m, src)
		if !ok {
			continue
		}
		out = append(out, Evidence{
			Tenant: m.membership.Tenant,
			Port:   m.membership.Port,
			Mode:   m.membership.Mode,
			Rule:   rule,
		})
	}
	return out
}

// Decisions evaluates every address in scope against the merged filter.
// Address expansion happens only in this query helper for verification; the
// stored filter itself remains prefix-only.
func (g *Group) Decisions(scope netip.Prefix) ([]SourceDecision, error) {
	if !scope.Addr().IsValid() || scope.Addr().Is4In6() {
		return nil, ErrInvalidInput
	}
	addr := scope.Addr().Unmap()
	scope = scope.Masked()
	var decisions []SourceDecision
	last := lastAddrOf(scope)
	for cur := addr; ; cur = cur.Next() {
		ev := g.EvidenceFor(cur)
		decisions = append(decisions, SourceDecision{
			Source:   cur,
			Allowed:  len(ev) > 0,
			Evidence: ev,
		})
		if cur == last {
			break
		}
	}
	return decisions, nil
}

func lastAddrOf(p netip.Prefix) netip.Addr {
	addr := p.Addr().Unmap()
	ones, bits := p.Bits(), addr.BitLen()
	arr := prefixBits(p)
	for i := ones; i < bits; i++ {
		setBit(&arr, i, 1)
	}
	if bits == 32 {
		var raw [4]byte
		copy(raw[:], arr[:4])
		a4 := netip.AddrFrom4(raw)
		return a4
	}
	a6 := netip.AddrFrom16(arr)
	return a6
}
