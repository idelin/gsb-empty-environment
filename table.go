package rib

import (
	"fmt"
	"net/netip"
	"sync"
	"sync/atomic"
)

// OpKind selects insert or withdraw.
type OpKind int

const (
	OpAdd OpKind = iota
	OpDel
)

// Op is one route insert/withdraw within a batch.
type Op struct {
	Kind   OpKind
	Prefix netip.Prefix // must be canonical (see ParsePrefixStrict)
	Route  Route
}

// Add is a convenience constructor for an insert op.
func Add(p netip.Prefix, r Route) Op { return Op{Kind: OpAdd, Prefix: p, Route: r} }

// Del is a convenience constructor for a withdraw op.
func Del(p netip.Prefix, r Route) Op { return Op{Kind: OpDel, Prefix: p, Route: r} }

// snapshot is an immutable, versioned pair of v4/v6 tries.
type snapshot struct {
	version uint64
	v4, v6  *node
	refs    atomic.Int64 // active readers (for reclamation accounting)
}

// Table is a concurrent route table. Writers serialize on a mutex and
// publish a whole new snapshot with one atomic store; readers are lock-free
// and always see a single consistent version, never a mixed tree.
type Table struct {
	mu  sync.Mutex
	cur atomic.Pointer[snapshot]
	seq atomic.Uint64
}

func NewTable() *Table {
	t := &Table{}
	t.cur.Store(&snapshot{})
	return t
}

// Apply applies ops as one atomic batch and returns the new version.
// Prefixes must already be canonical; a non-canonical op rejects the whole
// batch and the table stays at its previous version.
func (t *Table) Apply(ops ...Op) (uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	old := t.cur.Load()
	v4, v6 := old.v4, old.v6
	for i, op := range ops {
		p := op.Prefix
		if !p.IsValid() || p.Addr().Zone() != "" {
			return old.version, &OpError{Index: i, Prefix: op.Prefix, Why: "invalid prefix"}
		}
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return old.version, &OpError{Index: i, Prefix: op.Prefix, Why: "mapped IPv4 with bits < 96"}
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		if p != p.Masked() {
			return old.version, &OpError{Index: i, Prefix: op.Prefix, Why: "host bits set"}
		}
		key, klen := keyOf(p)
		is4 := p.Addr().Is4()
		switch op.Kind {
		case OpAdd:
			r := op.Route
			r.seq = t.seq.Add(1)
			if is4 {
				v4 = ins(v4, key, klen, r, 0)
			} else {
				v6 = ins(v6, key, klen, r, 0)
			}
		case OpDel:
			if is4 {
				v4, _ = del(v4, key, klen, op.Route, 0)
			} else {
				v6, _ = del(v6, key, klen, op.Route, 0)
			}
		}
	}
	s := &snapshot{version: old.version + 1, v4: v4, v6: v6}
	t.cur.Store(s)
	return s.version, nil
}

// OpError reports a rejected batch op.
type OpError struct {
	Index  int
	Prefix netip.Prefix
	Why    string
}

func (e *OpError) Error() string {
	return fmt.Sprintf("op %d (%s): %s", e.Index, e.Prefix, e.Why)
}

// Version returns the currently published version.
func (t *Table) Version() uint64 { return t.cur.Load().version }

// View pins one immutable snapshot for lock-free reads.
type View struct {
	snap *snapshot
}

// Acquire returns a view of the current snapshot. Call Release when done.
func (t *Table) Acquire() *View {
	s := t.cur.Load()
	s.refs.Add(1)
	return &View{snap: s}
}

// Release drops the view's reference to its snapshot.
func (v *View) Release() { v.snap.refs.Add(-1) }

// Version of the pinned snapshot.
func (v *View) Version() uint64 { return v.snap.version }

// Lookup returns the longest-prefix match: the matched prefix and its best
// route. More specific prefixes override less specific ones (including the
// default route); withdrawing a route exposes the next-best route or a
// shorter prefix automatically.
func (v *View) Lookup(a netip.Addr) (netip.Prefix, Route, bool) {
	p, r, ok, _ := v.lookup(a)
	return p, r, ok
}

// LookupBits is Lookup plus the number of bit comparisons performed, which
// is bounded by the address width (32 or 128).
func (v *View) LookupBits(a netip.Addr) (netip.Prefix, Route, bool, int) {
	return v.lookup(a)
}

func (v *View) lookup(a netip.Addr) (netip.Prefix, Route, bool, int) {
	a = a.Unmap()
	key, klen := addrKey(a)
	var counter int
	var n *node
	is4 := a.Is4()
	if is4 {
		n = lookup(v.snap.v4, key, klen, &counter)
	} else {
		n = lookup(v.snap.v6, key, klen, &counter)
	}
	if n == nil {
		return netip.Prefix{}, Route{}, false, counter
	}
	return n.prefix(is4), n.routes[0], true, counter
}

// Routes returns all routes at the longest-prefix match for a, best first.
func (v *View) Routes(a netip.Addr) (netip.Prefix, []Route, bool) {
	a = a.Unmap()
	key, klen := addrKey(a)
	var n *node
	if a.Is4() {
		n = lookup(v.snap.v4, key, klen, nil)
	} else {
		n = lookup(v.snap.v6, key, klen, nil)
	}
	if n == nil {
		return netip.Prefix{}, nil, false
	}
	return n.prefix(a.Is4()), n.routes, true
}

// Walk enumerates every prefix and its routes, v4 then v6, each in
// lexicographic order.
func (v *View) Walk(fn func(netip.Prefix, []Route)) {
	walk(v.snap.v4, true, fn)
	walk(v.snap.v6, false, fn)
}

// Prefixes returns all prefixes in the snapshot.
func (v *View) Prefixes() []netip.Prefix {
	var out []netip.Prefix
	v.Walk(func(p netip.Prefix, _ []Route) { out = append(out, p) })
	return out
}
