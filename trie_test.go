package rib

import (
	"math/rand/v2"
	"net/netip"
	"testing"
)

// refTable is an independent, deliberately simple reference: a flat map of
// prefix -> routes, with longest-prefix match done by full scan.
type refTable struct {
	m   map[netip.Prefix][]Route
	seq uint64
}

func newRefTable() *refTable { return &refTable{m: map[netip.Prefix][]Route{}} }

func (r *refTable) add(p netip.Prefix, rt Route) {
	r.seq++
	rt.seq = r.seq
	r.m[p] = insertRoute(r.m[p], rt)
}

func (r *refTable) del(p netip.Prefix, rt Route) {
	rs, ok := r.m[p]
	if !ok {
		return
	}
	rs, _ = removeRoutes(rs, rt)
	if len(rs) == 0 {
		delete(r.m, p)
	} else {
		r.m[p] = rs
	}
}

func (r *refTable) lookup(a netip.Addr) (netip.Prefix, Route, bool) {
	var best netip.Prefix
	found := false
	for p := range r.m {
		if p.Addr().Is4() != a.Is4() {
			continue
		}
		if p.Contains(a) && (!found || p.Bits() > best.Bits()) {
			best, found = p, true
		}
	}
	if !found {
		return netip.Prefix{}, Route{}, false
	}
	return best, r.m[best][0], true
}

func randAddr(rng *rand.Rand, v6 bool) netip.Addr {
	if v6 {
		var b [16]byte
		for i := range b {
			b[i] = byte(rng.Uint())
		}
		return netip.AddrFrom16(b)
	}
	return netip.AddrFrom4([4]byte{byte(rng.Uint()), byte(rng.Uint()), byte(rng.Uint()), byte(rng.Uint())})
}

func randPrefix(rng *rand.Rand, v6 bool) netip.Prefix {
	a := randAddr(rng, v6)
	w := 32
	if v6 {
		w = 128
	}
	return netip.PrefixFrom(a, rng.IntN(w+1)).Masked()
}

func randRoute(rng *rand.Rand, id int) Route {
	n := 1 + rng.IntN(3)
	nhs := make([]netip.Addr, n)
	for i := range nhs {
		nhs[i] = randAddr(rng, false)
	}
	return Route{Priority: rng.IntN(4), NextHops: nhs, Tag: string(rune('a' + id%6))}
}

// TestFuzzVsFullScan applies random batches to both the trie and the
// reference full-scan table, then cross-checks enumeration, random lookups
// and the bit-access bound after every batch.
func TestFuzzVsFullScan(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 7))
	tab := NewTable()
	ref := newRefTable()
	var added []Op

	for batch := 0; batch < 400; batch++ {
		var ops []Op
		n := 1 + rng.IntN(8)
		for i := 0; i < n; i++ {
			v6 := rng.IntN(2) == 0
			if len(added) > 0 && rng.IntN(3) == 0 {
				// withdraw a previously added route
				op := added[rng.IntN(len(added))]
				ops = append(ops, Del(op.Prefix, op.Route))
				ref.del(op.Prefix, op.Route)
			} else {
				p := randPrefix(rng, v6)
				rt := randRoute(rng, batch*16+i)
				ops = append(ops, Add(p, rt))
				ref.add(p, rt)
				added = append(added, Add(p, rt))
			}
		}
		if _, err := tab.Apply(ops...); err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}

		// 1. compression invariant on both tries
		snap := tab.cur.Load()
		if err := checkCompressed(snap.v4, 0); err != nil {
			t.Fatalf("batch %d v4: %v", batch, err)
		}
		if err := checkCompressed(snap.v6, 0); err != nil {
			t.Fatalf("batch %d v6: %v", batch, err)
		}

		// 2. full enumeration matches the reference set
		v := tab.Acquire()
		got := map[netip.Prefix]int{}
		v.Walk(func(p netip.Prefix, rs []Route) { got[p] = len(rs) })
		if len(got) != len(ref.m) {
			t.Fatalf("batch %d: %d prefixes, ref has %d", batch, len(got), len(ref.m))
		}
		for p, n := range got {
			if len(ref.m[p]) != n {
				t.Fatalf("batch %d: prefix %s route count %d, ref %d", batch, p, n, len(ref.m[p]))
			}
		}

		// 3. random lookups match the full-scan reference, bit bound holds
		for i := 0; i < 100; i++ {
			v6 := rng.IntN(2) == 0
			a := randAddr(rng, v6)
			gp, gr, gok, bits := v.LookupBits(a)
			rp, rr, rok := ref.lookup(a)
			if gok != rok {
				t.Fatalf("batch %d: lookup %s ok=%v ref=%v", batch, a, gok, rok)
			}
			if gok && (gp != rp || gr.Priority != rr.Priority || gr.Tag != rr.Tag) {
				t.Fatalf("batch %d: lookup %s = %s/%v, ref %s/%v", batch, a, gp, gr, rp, rr)
			}
			w := 32
			if v6 {
				w = 128
			}
			if bits > w {
				t.Fatalf("batch %d: lookup %s used %d bit accesses > %d", batch, a, bits, w)
			}
		}
		v.Release()
	}
}

// TestBitBoundEmptyAndDeep checks the bit-access upper bound explicitly on a
// deep chain of prefixes.
func TestBitBoundDeepChain(t *testing.T) {
	tab := NewTable()
	var ops []Op
	for i := 1; i <= 128; i++ {
		p := netip.PrefixFrom(netip.MustParseAddr("2001:db8::"), i).Masked()
		ops = append(ops, Add(p, Route{Priority: 1, Tag: "x"}))
	}
	if _, err := tab.Apply(ops...); err != nil {
		t.Fatal(err)
	}
	v := tab.Acquire()
	defer v.Release()
	_, _, ok, bits := v.LookupBits(netip.MustParseAddr("2001:db8::1"))
	if !ok {
		t.Fatal("no match")
	}
	if bits > 128 {
		t.Fatalf("bit accesses %d exceed 128", bits)
	}
}

// TestSpecificOverridesDefaultAndFallback: more specific prefixes beat the
// default route; withdrawing the specific one falls back to shorter ones.
func TestSpecificOverridesDefaultAndFallback(t *testing.T) {
	tab := NewTable()
	must := func(s string) netip.Prefix {
		p, err := ParsePrefixStrict(s, RejectHostBits)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	def := Route{Priority: 1, Tag: "default"}
	mid := Route{Priority: 1, Tag: "mid"}
	leaf := Route{Priority: 1, Tag: "leaf"}
	if _, err := tab.Apply(
		Add(must("0.0.0.0/0"), def),
		Add(must("10.0.0.0/8"), mid),
		Add(must("10.1.0.0/16"), leaf),
	); err != nil {
		t.Fatal(err)
	}
	v := tab.Acquire()
	addr := netip.MustParseAddr("10.1.2.3")
	if _, r, _ := v.Lookup(addr); r.Tag != "leaf" {
		t.Fatalf("want leaf, got %q", r.Tag)
	}
	v.Release()

	// withdraw the /16: fall back to the /8
	if _, err := tab.Apply(Del(must("10.1.0.0/16"), leaf)); err != nil {
		t.Fatal(err)
	}
	v = tab.Acquire()
	if _, r, _ := v.Lookup(addr); r.Tag != "mid" {
		t.Fatalf("want mid, got %q", r.Tag)
	}
	v.Release()

	// withdraw the /8: fall back to default
	if _, err := tab.Apply(Del(must("10.0.0.0/8"), mid)); err != nil {
		t.Fatal(err)
	}
	v = tab.Acquire()
	defer v.Release()
	if _, r, _ := v.Lookup(addr); r.Tag != "default" {
		t.Fatalf("want default, got %q", r.Tag)
	}
}

// TestWithdrawRestoresNextBest: same-prefix priorities; withdrawing the best
// route exposes the next-best one.
func TestWithdrawRestoresNextBest(t *testing.T) {
	tab := NewTable()
	p := netip.MustParsePrefix("192.0.2.0/24")
	hi := Route{Priority: 100, Tag: "primary"}
	lo := Route{Priority: 10, Tag: "backup"}
	if _, err := tab.Apply(Add(p, lo), Add(p, hi)); err != nil {
		t.Fatal(err)
	}
	addr := netip.MustParseAddr("192.0.2.9")
	v := tab.Acquire()
	if _, r, _ := v.Lookup(addr); r.Tag != "primary" {
		t.Fatalf("want primary, got %q", r.Tag)
	}
	v.Release()

	if _, err := tab.Apply(Del(p, hi)); err != nil {
		t.Fatal(err)
	}
	v = tab.Acquire()
	defer v.Release()
	if _, r, _ := v.Lookup(addr); r.Tag != "backup" {
		t.Fatalf("next-best not restored, got %q", r.Tag)
	}
}

// TestNonByteAlignedIPv6 exercises split/merge on non-byte-aligned prefixes.
func TestNonByteAlignedIPv6(t *testing.T) {
	tab := NewTable()
	must := func(s string) netip.Prefix { return netip.MustParsePrefix(s) }
	ops := []Op{
		Add(must("2001:db8::/33"), Route{Priority: 1, Tag: "a"}),
		Add(must("2001:db8:8000::/34"), Route{Priority: 1, Tag: "b"}),
		Add(must("2001:db8:c000::/35"), Route{Priority: 1, Tag: "c"}),
		Add(must("2001:db8:e800::/61"), Route{Priority: 1, Tag: "d"}),
		Add(must("2001:db8:ffff:ffff:ffff:ffff:ffff:fffe/127"), Route{Priority: 1, Tag: "e"}),
	}
	if _, err := tab.Apply(ops...); err != nil {
		t.Fatal(err)
	}
	v := tab.Acquire()
	cases := []struct{ addr, tag string }{
		{"2001:db8:0000::1", "a"},
		{"2001:db8:9000::1", "b"},
		{"2001:db8:d000::1", "c"},
		{"2001:db8:e800:0007::1", "d"},
		{"2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", "e"},
	}
	for _, c := range cases {
		if _, r, ok := v.Lookup(netip.MustParseAddr(c.addr)); !ok || r.Tag != c.tag {
			t.Fatalf("%s: want %q, got %q (ok=%v)", c.addr, c.tag, r.Tag, ok)
		}
	}
	v.Release()
	// withdraw everything; table must compress back to empty
	for _, op := range ops {
		if _, err := tab.Apply(Del(op.Prefix, op.Route)); err != nil {
			t.Fatal(err)
		}
	}
	snap := tab.cur.Load()
	if snap.v6 != nil {
		t.Fatal("v6 trie not fully merged after withdrawals")
	}
}
