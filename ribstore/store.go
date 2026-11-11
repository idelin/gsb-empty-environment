// Package ribstore implements a versioned prefix store for a route
// collector. It keeps per-neighbor announcements and withdrawals, validates
// the resulting RIB against ROAs, and lets subscribers observe atomic RIB/ROA
// views through a strictly versioned delta stream.
package ribstore

import (
	"bytes"
	"net/netip"
	"sort"
	"sync"
)

// ValidationState is the RPKI validation outcome of a route. The two invalid
// cases are kept distinct so operators can trace max-length violations and
// origin-AS mismatches separately.
type ValidationState int

const (
	// NotFound means no ROA covers the prefix.
	NotFound ValidationState = iota
	// Valid means a covering ROA matches both origin ASN and max length.
	Valid
	// InvalidOriginAS means ROAs cover the prefix but none lists the
	// route's origin ASN.
	InvalidOriginAS
	// InvalidMaxLength means a covering ROA lists the origin ASN but the
	// prefix is more specific than its max length.
	InvalidMaxLength
)

func (v ValidationState) String() string {
	switch v {
	case NotFound:
		return "NotFound"
	case Valid:
		return "Valid"
	case InvalidOriginAS:
		return "InvalidOriginAS"
	case InvalidMaxLength:
		return "InvalidMaxLength"
	}
	return "Unknown"
}

// ROA is a single ROA entry: ASN is authorized to originate Prefix (or more
// specifics) up to MaxLength bits.
type ROA struct {
	Prefix    netip.Prefix
	MaxLength int
	ASN       uint32
}

// RouteView is the RIB entry for one prefix as observed by subscribers.
type RouteView struct {
	Prefix     netip.Prefix
	Neighbor   string
	OriginASN  uint32
	Validation ValidationState
}

// Delta is one atomic, versioned update to the RIB view. When FullSync is
// set the receiver must replace its whole table with Announced; otherwise
// the delta merges incrementally into the receiver's table.
type Delta struct {
	Version   uint64
	FullSync  bool
	Announced []RouteView
	Withdrawn []netip.Prefix
}

// Stats exposes store counters. Revalidations counts prefix re-validations
// triggered by ROA updates, so tests and operators can confirm that ROA
// changes only recompute affected prefixes.
type Stats struct {
	Version       uint64
	Prefixes      int
	Revalidations uint64
}

// prefixChange records the before/after view of one prefix in a commit.
type prefixChange struct {
	prefix netip.Prefix
	old    *RouteView
	new    *RouteView
}

// journalEntry is one committed version: the set of prefix changes applied
// atomically under that version.
type journalEntry struct {
	version uint64
	changes []prefixChange
}

// Store is the versioned prefix store. All methods are safe for concurrent
// use; subscribers always observe whole commits, never intermediate states.
type Store struct {
	mu            sync.RWMutex
	root          *pnode
	version       uint64
	base          uint64 // version of the state preceding journal[0]
	journal       []journalEntry
	subs          map[*Subscription]struct{}
	revalidations uint64
}

// New returns an empty store at version 0.
func New() *Store {
	return &Store{
		root: &pnode{},
		subs: make(map[*Subscription]struct{}),
	}
}

// Version returns the current store version.
func (s *Store) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Stats returns current store counters.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Stats{Version: s.version, Revalidations: s.revalidations}
	s.walkRouteNodesFrom(s.root, [16]byte{}, 0, func(*pnode, netip.Prefix) { st.Prefixes++ })
	return st
}

// Lookup returns the current view of an exact prefix.
func (s *Store) Lookup(p netip.Prefix) (RouteView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.nodeFor(p.Masked(), false)
	if n == nil {
		return RouteView{}, false
	}
	v := s.viewOf(n, p.Masked())
	if v == nil {
		return RouteView{}, false
	}
	return *v, true
}

// Snapshot returns the full RIB view at the current version.
func (s *Store) Snapshot() []RouteView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked()
}

func (s *Store) snapshotLocked() []RouteView {
	var out []RouteView
	s.walkRouteNodesFrom(s.root, [16]byte{}, 0, func(n *pnode, p netip.Prefix) {
		if v := s.viewOf(n, p); v != nil {
			out = append(out, *v)
		}
	})
	sort.Slice(out, func(i, j int) bool { return lessView(out[i], out[j]) })
	return out
}

// AddRoute records or replaces an announcement from a neighbor. Identical
// re-announcements are no-ops and do not bump the version.
func (s *Store) AddRoute(neighbor string, p netip.Prefix, originASN uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p = p.Masked()
	n := s.nodeFor(p, true)
	if n.routes == nil {
		n.routes = make(map[string]uint32)
	}
	if cur, ok := n.routes[neighbor]; ok && cur == originASN {
		return
	}
	old := s.viewOf(n, p)
	n.routes[neighbor] = originASN
	s.commitBestChange(n, p, old)
}

// WithdrawRoute removes one neighbor's announcement for a prefix.
func (s *Store) WithdrawRoute(neighbor string, p netip.Prefix) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p = p.Masked()
	n := s.nodeFor(p, false)
	if n == nil {
		return
	}
	if _, ok := n.routes[neighbor]; !ok {
		return
	}
	old := s.viewOf(n, p)
	delete(n.routes, neighbor)
	s.commitBestChange(n, p, old)
}

// WithdrawNeighbor removes every announcement of a neighbor (session down)
// as a single atomic commit.
func (s *Store) WithdrawNeighbor(neighbor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var changes []prefixChange
	s.walkRouteNodesFrom(s.root, [16]byte{}, 0, func(n *pnode, p netip.Prefix) {
		if _, ok := n.routes[neighbor]; !ok {
			return
		}
		old := s.viewOf(n, p)
		delete(n.routes, neighbor)
		if nw := s.viewOf(n, p); !viewsEqual(old, nw) {
			changes = append(changes, prefixChange{prefix: p, old: old, new: nw})
		}
	})
	s.commit(changes)
}

// AddROA inserts a ROA and re-validates only the RIB prefixes it covers.
func (s *Store) AddROA(r ROA) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r = normalizeROA(r)
	n := s.roaNode(r.Prefix, true)
	if _, ok := n.roas[r]; ok {
		return
	}
	changes := s.revalidateCovered(n, r.Prefix, func() { n.roas[r] = struct{}{} })
	s.commit(changes)
}

// RemoveROA deletes a ROA and re-validates only the RIB prefixes it covered.
func (s *Store) RemoveROA(r ROA) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r = normalizeROA(r)
	n := s.roaNode(r.Prefix, false)
	if n == nil {
		return
	}
	if _, ok := n.roas[r]; !ok {
		return
	}
	changes := s.revalidateCovered(n, r.Prefix, func() { delete(n.roas, r) })
	s.commit(changes)
}

func normalizeROA(r ROA) ROA {
	r.Prefix = r.Prefix.Masked()
	if r.MaxLength < r.Prefix.Bits() {
		r.MaxLength = r.Prefix.Bits()
	}
	return r
}

func (s *Store) roaNode(p netip.Prefix, create bool) *pnode {
	n := s.nodeFor(p, create)
	if n != nil && create && n.roas == nil {
		n.roas = make(map[ROA]struct{})
	}
	return n
}

// revalidateCovered snapshots the views of the RIB prefixes covered by
// roaPrefix, applies mutate (the ROA insert/delete), then re-validates only
// those prefixes and returns the ones whose view changed.
func (s *Store) revalidateCovered(n *pnode, roaPrefix netip.Prefix, mutate func()) []prefixChange {
	key, bits := normKey(roaPrefix)
	type affected struct {
		node *pnode
		pfx  netip.Prefix
		old  *RouteView
	}
	var list []affected
	s.walkRouteNodesFrom(n, key, bits, func(rn *pnode, p netip.Prefix) {
		list = append(list, affected{node: rn, pfx: p, old: s.viewOf(rn, p)})
	})
	mutate()
	var changes []prefixChange
	for _, a := range list {
		s.revalidations++
		if nw := s.viewOf(a.node, a.pfx); !viewsEqual(a.old, nw) {
			changes = append(changes, prefixChange{prefix: a.pfx, old: a.old, new: nw})
		}
	}
	return changes
}

// Compact drops all but the newest keep journal entries. Subscribers that
// later request a replay from before the retained base receive a full-table
// sync instead of incremental deltas.
func (s *Store) Compact(keep int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if keep < 0 {
		keep = 0
	}
	if len(s.journal) <= keep {
		return
	}
	s.journal = append([]journalEntry(nil), s.journal[len(s.journal)-keep:]...)
	if len(s.journal) == 0 {
		s.base = s.version
	} else {
		s.base = s.journal[0].version - 1
	}
}

// commitBestChange recomputes the best view of one prefix after its route
// set changed and commits the difference, if any.
func (s *Store) commitBestChange(n *pnode, p netip.Prefix, old *RouteView) {
	nw := s.viewOf(n, p)
	if viewsEqual(old, nw) {
		return
	}
	s.commit([]prefixChange{{prefix: p, old: old, new: nw}})
}

// commit bumps the version, journals the changes, and fans the delta out to
// subscribers. Callers must hold s.mu and pass a non-empty change set.
func (s *Store) commit(changes []prefixChange) {
	if len(changes) == 0 {
		return
	}
	s.version++
	s.journal = append(s.journal, journalEntry{version: s.version, changes: changes})
	d := Delta{Version: s.version}
	for _, c := range changes {
		if c.new != nil {
			d.Announced = append(d.Announced, *c.new)
		} else {
			d.Withdrawn = append(d.Withdrawn, c.prefix)
		}
	}
	sortDelta(&d)
	for sub := range s.subs {
		sub.send(d)
	}
}

// viewOf computes the current best view for the routes at node n. The best
// route is chosen deterministically by lowest neighbor ID.
func (s *Store) viewOf(n *pnode, p netip.Prefix) *RouteView {
	best := ""
	for id := range n.routes {
		if best == "" || id < best {
			best = id
		}
	}
	if best == "" {
		return nil
	}
	asn := n.routes[best]
	return &RouteView{
		Prefix:     p,
		Neighbor:   best,
		OriginASN:  asn,
		Validation: s.validate(p, asn),
	}
}

// validate walks the trie from the root to p, evaluating every ROA that
// covers the prefix.
func (s *Store) validate(p netip.Prefix, asn uint32) ValidationState {
	key, bits := normKey(p)
	plen := p.Bits()
	n := s.root
	covering := false
	asnMatch := false
	for i := 0; i <= bits; i++ {
		for roa := range n.roas {
			covering = true
			if roa.ASN == asn {
				if plen <= roa.MaxLength {
					return Valid
				}
				asnMatch = true
			}
		}
		if i == bits {
			break
		}
		n = n.child[bitAt(key, i)]
		if n == nil {
			break
		}
	}
	switch {
	case !covering:
		return NotFound
	case asnMatch:
		return InvalidMaxLength
	default:
		return InvalidOriginAS
	}
}

func viewsEqual(a, b *RouteView) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func lessPrefix(a, b netip.Prefix) bool {
	ka, ba := normKey(a)
	kb, bb := normKey(b)
	if c := bytes.Compare(ka[:], kb[:]); c != 0 {
		return c < 0
	}
	return ba < bb
}

func lessView(a, b RouteView) bool { return lessPrefix(a.Prefix, b.Prefix) }

func sortDelta(d *Delta) {
	sort.Slice(d.Announced, func(i, j int) bool { return lessView(d.Announced[i], d.Announced[j]) })
	sort.Slice(d.Withdrawn, func(i, j int) bool { return lessPrefix(d.Withdrawn[i], d.Withdrawn[j]) })
}
