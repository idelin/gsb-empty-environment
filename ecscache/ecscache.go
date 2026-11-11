// Package ecscache implements an EDNS Client Subnet (ECS, RFC 7871) aware
// answer cache for a DNS resolver front end.
//
// A cache entry cannot be shared between client addresses merely because the
// queried name and type match. Reuse is jointly determined by the address
// family, query name, query type, the client-subnet prefix actually sent
// upstream, and the scope prefix returned by the upstream:
//
//   - The upstream response scope may only cover a prefix that contains the
//     client address the request was made for.
//   - The scope prefix length must never exceed the source prefix length that
//     was sent. An answer claiming a finer scope than the upstream was told
//     about is rejected rather than cached.
//   - A cached answer is reused only for addresses whose bits inside the
//     response scope match the stored scope.
//
// Per-tenant configuration truncates the client address before it is sent
// upstream. Tightening a tenant's source prefix limits the scope of every
// lookup and drops finer entries, so older, more precise answers can never
// leak to a later, more privacy-preserving policy.
package ecscache

import (
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Family identifies the address family of a client subnet.
type Family uint8

const (
	FamilyUnspecified Family = 0
	FamilyIPv4        Family = 1
	FamilyIPv6        Family = 2
)

var (
	// ErrInvalidAddress is returned when a client address is unspecified or
	// cannot be mapped to a supported family.
	ErrInvalidAddress = errors.New("ecscache: invalid client address")
	// ErrUnknownTenant is returned when a lookup or store references a tenant
	// that has no configuration installed.
	ErrUnknownTenant = errors.New("ecscache: unknown tenant")
	// ErrScopeLongerThanSource is returned when a response scope prefix is
	// longer than the source prefix that was sent upstream.
	ErrScopeLongerThanSource = errors.New("ecscache: response scope is longer than sent source prefix")
	// ErrScopeExcludesAddress is returned when the response scope prefix does
	// not contain the client address.
	ErrScopeExcludesAddress = errors.New("ecscache: response scope does not contain client address")
	// ErrScopeFamilyMismatch is returned when the scope address belongs to a
	// different family than the client address.
	ErrScopeFamilyMismatch = errors.New("ecscache: response scope family differs from client family")
	// ErrNonPositiveTTL is returned when an entry is stored with a non-positive
	// time to live.
	ErrNonPositiveTTL = errors.New("ecscache: ttl must be positive")
)

// Config is the per-tenant truncation policy. Each value is the number of
// leading address bits retained when constructing the ECS source prefix sent
// upstream; the remaining bits are zeroed. Valid ranges are 0..32 for IPv4 and
// 0..128 for IPv6.
type Config struct {
	IPv4SourceBits uint8
	IPv6SourceBits uint8
}

// Prefix is the client subnet information that travels with a request.
type Prefix struct {
	Family Family
	// Bits is the prefix length. Addr carries the masked prefix; any bits
	// beyond Bits are zero.
	Bits uint8
	Addr netip.Addr
}

// key identifies a stored answer. Every field participates, so answers for
// different families, names, types, sent source prefixes, or response scopes
// never collide.
type key struct {
	tenant     string
	family     Family
	name       string
	qtype      uint16
	sourceBits uint8
	scopeBits  uint8
	scopeAddr  [16]byte
}

type entry struct {
	value   []byte
	expires time.Time
}

// Cache is a concurrency-safe ECS-aware answer cache.
type Cache struct {
	now   func() time.Time
	mu    sync.RWMutex
	cfgs  map[string]Config
	store map[key]entry
}

// New creates an empty cache. Initial tenant policies must be installed with
// SetConfig before lookups are possible.
func New() *Cache {
	return &Cache{
		now:   time.Now,
		cfgs:  make(map[string]Config),
		store: make(map[key]entry),
	}
}

// newWithClock creates a cache with an injectable clock for tests.
func newWithClock(now func() time.Time) *Cache {
	c := New()
	c.now = now
	return c
}

// SetConfig installs or replaces the truncation policy for a tenant.
//
// When the policy is tightened (fewer source bits for either family) entries
// whose response scope is finer than the new limit are deleted. They could no
// longer be returned, and keeping them would let stale, precise answers
// resurface if the policy were later widened.
func (c *Cache) SetConfig(tenant string, cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	old, had := c.cfgs[tenant]
	c.cfgs[tenant] = cfg
	if !had {
		return nil
	}

	for k := range c.store {
		if k.tenant != tenant {
			continue
		}
		limit := sourceBitsFor(old, k.family)
		if sourceBitsFor(cfg, k.family) < limit && k.scopeBits > sourceBitsFor(cfg, k.family) {
			delete(c.store, k)
		}
	}
	return nil
}

// Lookup returns a cached answer for the client's request.
//
// sent is the exact source prefix that the caller must send upstream on a
// miss: the client address masked with the tenant's configured truncation
// length. On a hit, value is the cached answer; on a miss value is nil.
func (c *Cache) Lookup(tenant string, client netip.Addr, name string, qtype uint16) (value []byte, sent Prefix, err error) {
	if !client.IsValid() {
		return nil, Prefix{}, ErrInvalidAddress
	}
	fam := familyOf(client)
	canonical := canonicalName(name)

	c.mu.RLock()
	defer c.mu.RUnlock()

	cfg, ok := c.cfgs[tenant]
	if !ok {
		return nil, Prefix{}, ErrUnknownTenant
	}

	client = normalizeAddr(client)
	sourceBits := sourceBitsFor(cfg, fam)
	sentAddr := maskAddr(client, sourceBits)
	sent = Prefix{Family: fam, Bits: sourceBits, Addr: sentAddr}

	now := c.now()
	var best key
	bestFound := false
	for k, e := range c.store {
		if !e.expires.After(now) {
			continue
		}
		if k.tenant != tenant || k.family != fam || k.name != canonical || k.qtype != qtype {
			continue
		}
		// The response scope cannot be finer than the precision the tenant
		// is currently allowed to send.
		if k.scopeBits > sourceBits {
			continue
		}
		scope := netip.PrefixFrom(addrFromBytes(fam, k.scopeAddr), int(k.scopeBits))
		if !scope.Contains(client) {
			continue
		}
		if !bestFound || k.scopeBits > best.scopeBits ||
			(k.scopeBits == best.scopeBits && k.sourceBits > best.sourceBits) {
			best = k
			bestFound = true
		}
	}
	if !bestFound {
		return nil, sent, nil
	}
	return append([]byte(nil), c.store[best].value...), sent, nil
}

// Store caches an upstream answer.
//
// sent is the source prefix actually sent upstream for this request and scope
// is the ECS scope prefix returned by the upstream. The call is rejected if
// the scope is longer than the sent prefix, belongs to another family, or does
// not contain the client address. ttl bounds how long the answer stays usable.
func (c *Cache) Store(tenant string, client netip.Addr, name string, qtype uint16, sent, scope Prefix, value []byte, ttl time.Duration) error {
	if !client.IsValid() {
		return ErrInvalidAddress
	}
	if ttl <= 0 {
		return ErrNonPositiveTTL
	}
	fam := familyOf(client)

	c.mu.Lock()
	defer c.mu.Unlock()

	cfg, ok := c.cfgs[tenant]
	if !ok {
		return ErrUnknownTenant
	}
	sourceBits := sourceBitsFor(cfg, fam)
	client = normalizeAddr(client)

	if sent.Family != fam || scope.Family != fam {
		return ErrScopeFamilyMismatch
	}
	if !sent.Addr.IsValid() || !scope.Addr.IsValid() {
		return ErrInvalidAddress
	}
	expected := maskAddr(client, sourceBits)
	if sent.Bits != sourceBits || sent.Addr != expected {
		return errors.New("ecscache: sent prefix does not match tenant policy")
	}
	if scope.Bits > sent.Bits {
		return ErrScopeLongerThanSource
	}
	if scope.Bits > maxBits(fam) || sent.Bits > maxBits(fam) {
		return ErrInvalidAddress
	}

	scopePrefix := netip.PrefixFrom(scope.Addr, int(scope.Bits))
	if !scopePrefix.Contains(client) {
		return ErrScopeExcludesAddress
	}

	c.sweepLocked(c.now())
	k := key{
		tenant:     tenant,
		family:     fam,
		name:       canonicalName(name),
		qtype:      qtype,
		sourceBits: sent.Bits,
		scopeBits:  scope.Bits,
		scopeAddr:  addrBytes(scopePrefix.Masked().Addr()),
	}
	c.store[k] = entry{
		value:   append([]byte(nil), value...),
		expires: c.now().Add(ttl),
	}
	return nil
}

// Len reports the number of currently stored, unexpired entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepLocked(c.now())
	return len(c.store)
}

// PurgeTenant removes every entry belonging to a tenant.
func (c *Cache) PurgeTenant(tenant string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.store {
		if k.tenant == tenant {
			delete(c.store, k)
		}
	}
	delete(c.cfgs, tenant)
}

func (c *Cache) sweepLocked(now time.Time) {
	for k, e := range c.store {
		if !e.expires.After(now) {
			delete(c.store, k)
		}
	}
}

func validateConfig(cfg Config) error {
	if cfg.IPv4SourceBits > 32 || cfg.IPv6SourceBits > 128 {
		return errors.New("ecscache: source prefix length out of range")
	}
	return nil
}

func sourceBitsFor(cfg Config, fam Family) uint8 {
	if fam == FamilyIPv6 {
		return cfg.IPv6SourceBits
	}
	return cfg.IPv4SourceBits
}

func maxBits(fam Family) uint8 {
	if fam == FamilyIPv6 {
		return 128
	}
	return 32
}

func familyOf(addr netip.Addr) Family {
	if addr.Is4In6() {
		return FamilyIPv4
	}
	if addr.Is6() {
		return FamilyIPv6
	}
	return FamilyIPv4
}

func normalizeAddr(addr netip.Addr) netip.Addr {
	if addr.Is4In6() {
		return addr.Unmap()
	}
	return addr
}

func maskAddr(addr netip.Addr, bits uint8) netip.Addr {
	return netip.PrefixFrom(addr, int(bits)).Masked().Addr()
}

func addrBytes(addr netip.Addr) [16]byte {
	var out [16]byte
	b := addr.As16()
	copy(out[:], b[:])
	return out
}

func addrFromBytes(fam Family, b [16]byte) netip.Addr {
	if fam == FamilyIPv6 {
		return netip.AddrFrom16(b)
	}
	return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
}

func canonicalName(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}
