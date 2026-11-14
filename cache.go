package netsec

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type cacheKey struct {
	owner string
	typ   uint16
	class uint16
}

type cacheEntry struct {
	records []RR
	expiry  time.Time
	secure  bool
	source  string // canonical apex of the zone this data was admitted under
}

// Cache is a DNS cache that only accepts records inside the answering
// zone's bailiwick. It is safe for concurrent use.
type Cache struct {
	mu      sync.RWMutex
	now     func() time.Time
	entries map[cacheKey]*cacheEntry
}

// NewCache returns an empty cache using the real clock.
func NewCache() *Cache {
	return &Cache{now: time.Now, entries: make(map[cacheKey]*cacheEntry)}
}

// SetClock replaces the cache's clock. Intended for tests.
func (c *Cache) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// AdmitResult reports how a response was treated.
type AdmitResult struct {
	// Cached records were inside the bailiwick and entered the cache.
	Cached []RR
	// Hints records were outside the bailiwick. The resolver may use
	// them once (e.g. to continue a referral chase) but they are not
	// cached and carry no DNSSEC validation.
	Hints []RR
}

// Admit inspects a response received from an authoritative server for
// authZone (the zone of the server the resolver queried, canonical or
// not; "" means the root) and caches only the records that fall inside
// the computed bailiwick. Everything else is returned as one-time hints.
//
// The bailiwick is derived from the question (name and qtype), the
// answering zone, wildcard expansion (synthesized owners stay inside the
// zone), the CNAME/DNAME chain in the answer section, and any referrals
// in the authority section. Admitting a new delegation also evicts
// previously cached data below it that was admitted under the old,
// wider scope.
func (c *Cache) Admit(resp *Response, authZone string) *AdmitResult {
	zone := Canonical(authZone)
	validated := resp.DNSSECValid

	// Referrals: NS records in the authority section owned strictly
	// below the answering zone narrow the scope of everything else in
	// this response. Collect them first, with their nameserver names
	// for glue verification.
	delegations := make(map[string]map[string]bool) // owner -> ns names
	for _, rr := range resp.Authority {
		crr := canonicalRR(rr)
		if crr.Type == TypeNS && strictlyBelow(zone, crr.Owner) {
			ns := Canonical(crr.RData)
			if delegations[crr.Owner] == nil {
				delegations[crr.Owner] = make(map[string]bool)
			}
			delegations[crr.Owner][ns] = true
		}
	}
	// delegationOf returns the deepest referral at or above owner.
	delegationOf := func(owner string) string {
		best := ""
		for d := range delegations {
			if IsSubdomain(d, owner) && (best == "" || strictlyBelow(best, d)) {
				best = d
			}
		}
		return best
	}

	res := &AdmitResult{}
	seenHint := make(map[RR]bool)
	hint := func(rr RR) {
		if !seenHint[rr] {
			seenHint[rr] = true
			res.Hints = append(res.Hints, rr)
		}
	}
	type admission struct {
		rr     RR
		secure bool
	}
	var admissions []admission
	admit := func(rr RR, secure bool) {
		admissions = append(admissions, admission{rr: rr, secure: secure})
	}

	// Answer section: walk the CNAME/DNAME chain from the qname. Every
	// hop recomputes the scope; a hop that leaves the zone (or dives
	// below a referral) invalidates the rest of the chain for caching.
	scope := zone // "" means: chain left every zone we can vouch for
	chainName := Canonical(resp.Question.Name)
	for _, rr := range resp.Answer {
		crr := canonicalRR(rr)
		if scope != "" && IsSubdomain(scope, crr.Owner) && delegationOf(crr.Owner) == "" {
			admit(crr, validated)
		} else {
			hint(crr)
		}
		if scope == "" {
			continue
		}
		switch crr.Type {
		case TypeCNAME:
			if crr.Owner == chainName {
				chainName = Canonical(crr.RData)
				if !IsSubdomain(scope, chainName) || delegationOf(chainName) != "" {
					scope = ""
				}
			}
		case TypeDNAME:
			if strictlyBelow(crr.Owner, chainName) {
				chainName = rewriteSuffix(chainName, crr.Owner, crr.RData)
				if !IsSubdomain(scope, chainName) || delegationOf(chainName) != "" {
					scope = ""
				}
			}
		}
	}

	// Authority section: the zone's own data (SOA for negative answers,
	// the zone's NS, signatures) is cacheable, as are referrals
	// themselves. NS for unrelated zones and anything below a referral
	// are hints only.
	for _, rr := range resp.Authority {
		crr := canonicalRR(rr)
		if !IsSubdomain(zone, crr.Owner) {
			hint(crr)
			continue
		}
		d := delegationOf(crr.Owner)
		switch {
		case d == "":
			admit(crr, validated)
		case crr.Owner == d && (crr.Type == TypeNS || crr.Type == TypeDS):
			// The delegation itself. DS is signed by the parent zone;
			// the referral NS set is not.
			admit(crr, validated && crr.Type == TypeDS)
		default:
			hint(crr)
		}
	}

	// Additional section: in-zone data (e.g. addresses of MX/SRV
	// targets) is cacheable. Below a referral only glue is allowed:
	// address records for that delegation's own nameservers. Anything
	// else — the classic poisoning attempt — is a one-time hint.
	for _, rr := range resp.Additional {
		crr := canonicalRR(rr)
		if !IsSubdomain(zone, crr.Owner) {
			hint(crr)
			continue
		}
		if d := delegationOf(crr.Owner); d != "" {
			if (crr.Type == TypeA || crr.Type == TypeAAAA) && delegations[d][crr.Owner] {
				admit(crr, false) // glue is not signed
			} else {
				hint(crr)
			}
			continue
		}
		admit(crr, validated)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()

	// A new delegation invalidates the old scope: drop previously
	// cached records below the delegation point that were admitted
	// under an ancestor zone rather than by the delegated zone itself.
	for d := range delegations {
		for key, e := range c.entries {
			if strictlyBelow(d, key.owner) && !IsSubdomain(d, e.source) {
				delete(c.entries, key)
			}
		}
	}

	groups := make(map[cacheKey][]admission)
	var order []cacheKey
	for _, a := range admissions {
		key := cacheKey{owner: a.rr.Owner, typ: a.rr.Type, class: a.rr.Class}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], a)
	}
	seenCached := make(map[RR]bool)
	for _, key := range order {
		group := groups[key]
		ttl := group[0].rr.TTL
		secure := group[0].secure
		for _, a := range group {
			if a.rr.TTL < ttl {
				ttl = a.rr.TTL
			}
			secure = secure && a.secure
			if !seenCached[a.rr] {
				seenCached[a.rr] = true
				res.Cached = append(res.Cached, a.rr)
			}
		}
		e := c.entries[key]
		if e == nil {
			e = &cacheEntry{}
			c.entries[key] = e
		}
		// Duplicate records (same canonical owner/type/class/rdata)
		// collapse into the existing RRset.
		have := make(map[string]bool, len(e.records))
		for _, r := range e.records {
			have[r.RData] = true
		}
		for _, a := range group {
			if !have[a.rr.RData] {
				have[a.rr.RData] = true
				e.records = append(e.records, a.rr)
			}
		}
		sort.Slice(e.records, func(i, j int) bool {
			return e.records[i].RData < e.records[j].RData
		})
		e.expiry = now.Add(time.Duration(ttl) * time.Second)
		e.secure = secure
		e.source = zone
	}
	return res
}

// Lookup returns the cached RRset for (name, typ, class), whether it was
// admitted from a DNSSEC-validated zone, and whether it was present and
// unexpired. Returned TTLs are the remaining seconds.
func (c *Cache) Lookup(name string, typ, class uint16) ([]RR, bool, bool) {
	key := cacheKey{owner: Canonical(name), typ: typ, class: class}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false, false
	}
	now := c.now()
	if !now.Before(e.expiry) {
		delete(c.entries, key)
		return nil, false, false
	}
	remaining := uint32(e.expiry.Sub(now) / time.Second)
	recs := make([]RR, len(e.records))
	for i, r := range e.records {
		r.TTL = remaining
		recs[i] = r
	}
	return recs, e.secure, true
}

// Len reports the number of cached RRsets. Intended for tests and
// diagnostics.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// canonicalRR normalizes an RR for comparison and storage: lower-case
// absolute owner, and lower-case names inside RDATA of name-bearing
// types. Records that differed only in case or wire encoding become
// identical here.
func canonicalRR(rr RR) RR {
	rr.Owner = Canonical(rr.Owner)
	rr.RData = canonicalRData(rr.Type, rr.RData)
	return rr
}

func canonicalRData(typ uint16, rdata string) string {
	switch typ {
	case TypeNS, TypeCNAME, TypePTR, TypeDNAME:
		return Canonical(rdata)
	case TypeMX:
		if f := strings.Fields(rdata); len(f) == 2 {
			return f[0] + " " + Canonical(f[1])
		}
	case TypeSOA:
		if f := strings.Fields(rdata); len(f) == 7 {
			f[0] = Canonical(f[0])
			f[1] = Canonical(f[1])
			return strings.Join(f, " ")
		}
	case TypeSRV:
		if f := strings.Fields(rdata); len(f) == 4 {
			f[3] = Canonical(f[3])
			return strings.Join(f, " ")
		}
	case TypeRRSIG:
		if f := strings.Fields(rdata); len(f) >= 8 {
			f[7] = Canonical(f[7])
			return strings.Join(f, " ")
		}
	}
	return rdata
}
