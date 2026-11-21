package memberstate

import (
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func pfx(s string) netip.Prefix {
	return netip.MustParsePrefix(s)
}

func addr(s string) netip.Addr {
	return netip.MustParseAddr(s)
}

// disjointAndMinimal asserts the cover consists of mutually non-overlapping
// prefixes and that no two entries are sibling-mergeable.
func disjointAndMinimal(t *testing.T, cover []netip.Prefix) {
	t.Helper()
	for i, a := range cover {
		for j := i + 1; j < len(cover); j++ {
			if a.Overlaps(cover[j]) {
				t.Fatalf("overlapping cover entries %s vs %s in %v", a, cover[j], cover)
			}
		}
	}
	seen := map[netip.Prefix]bool{}
	for _, p := range cover {
		if seen[p] {
			t.Fatalf("duplicate prefix %s in %v", p, cover)
		}
		seen[p] = true
		parentBits := p.Bits() - 1
		if parentBits < 0 {
			continue
		}
		parent := netip.PrefixFrom(p.Masked().Addr(), parentBits)
		siblings := 0
		for _, q := range cover {
			if q.Bits() == p.Bits() && parent.Contains(q.Addr()) {
				siblings++
			}
		}
		if siblings == 2 {
			t.Fatalf("cover not minimal: %s mergeable inside %v", parent, cover)
		}
	}
}

// fixture builds the small source universe with three members exercising
// include, exclude and a short lease, in both address families.
func fixture(t *testing.T) (*Group, *atomic.Int64, netip.Prefix, netip.Prefix) {
	t.Helper()
	var clock atomic.Int64
	clock.Store(1000)
	g := NewGroup(WithClock(func() time.Time {
		return time.Unix(clock.Load(), 0)
	}))

	v4Universe := pfx("10.0.0.0/29") // .0 .. .7
	v6Universe := pfx("2001:db8::/125")

	mustUpdate := func(m Membership) {
		t.Helper()
		if err := g.Update(m); err != nil {
			t.Fatalf("update %s/%s: %v", m.Tenant, m.Port, err)
		}
	}

	// A: include .0-.3 only.
	mustUpdate(Membership{
		Tenant:  "tenant-a",
		Port:    "port-a1",
		Mode:    Include,
		Sources: []netip.Prefix{pfx("10.0.0.0/30"), pfx("2001:db8::/126")},
		Version: 1,
	})
	// B: exclude .4 (and its v6 twin); its isolated port must not receive
	// traffic from the excluded source, while everything else is permitted.
	mustUpdate(Membership{
		Tenant:  "tenant-b",
		Port:    "port-b2",
		Mode:    Exclude,
		Sources: []netip.Prefix{pfx("10.0.0.4/32"), pfx("2001:db8::4/128")},
		Version: 7,
	})
	// C: include .5, short lease.
	mustUpdate(Membership{
		Tenant:        "tenant-c",
		Port:          "port-c3",
		Mode:          Include,
		Sources:       []netip.Prefix{pfx("10.0.0.5/32"), pfx("2001:db8::5/128")},
		Version:       3,
		LeaseDeadline: time.Unix(2000, 0),
	})

	return g, &clock, v4Universe, v6Universe
}

func expectedKeys(ev []Evidence) []MemberKey {
	keys := make([]MemberKey, 0, len(ev))
	for _, e := range ev {
		keys = append(keys, MemberKey{Tenant: e.Tenant, Port: e.Port})
	}
	return keys
}

func hasMember(ev []Evidence, key MemberKey) bool {
	for _, k := range expectedKeys(ev) {
		if k == key {
			return true
		}
	}
	return false
}

func TestMixedIncludeExclude(t *testing.T) {
	g, _, v4Universe, v6Universe := fixture(t)

	decisions, err := g.Decisions(v4Universe)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]MemberKey{
		"10.0.0.0": {{Tenant: "tenant-a", Port: "port-a1"}, {Tenant: "tenant-b", Port: "port-b2"}},
		"10.0.0.1": {{Tenant: "tenant-a", Port: "port-a1"}, {Tenant: "tenant-b", Port: "port-b2"}},
		"10.0.0.2": {{Tenant: "tenant-a", Port: "port-a1"}, {Tenant: "tenant-b", Port: "port-b2"}},
		"10.0.0.3": {{Tenant: "tenant-a", Port: "port-a1"}, {Tenant: "tenant-b", Port: "port-b2"}},
		"10.0.0.4": nil, // B excludes it and A never includes it: must be denied
		"10.0.0.5": {{Tenant: "tenant-b", Port: "port-b2"}, {Tenant: "tenant-c", Port: "port-c3"}},
		"10.0.0.6": {{Tenant: "tenant-b", Port: "port-b2"}},
		"10.0.0.7": {{Tenant: "tenant-b", Port: "port-b2"}},
	}
	if len(decisions) != 8 {
		t.Fatalf("universe expansion wrong: %d", len(decisions))
	}
	for _, d := range decisions {
		got := expectedKeys(d.Evidence)
		exp := want[d.Source.String()]
		if len(got) != len(exp) {
			t.Fatalf("%s allowed=%v evidence=%v, want members %v", d.Source, d.Allowed, got, exp)
		}
		for i := range exp {
			if got[i] != exp[i] {
				t.Fatalf("%s evidence mismatch: got %v want %v", d.Source, got, exp)
			}
		}
		for _, e := range d.Evidence {
			if !e.Rule.Contains(d.Source) {
				t.Fatalf("evidence rule %s does not contain source %s", e.Rule, d.Source)
			}
		}
	}

	// The excluded source must never leak, including at prefix-cover level.
	cover4, err := g.AllowedWithin(v4Universe)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cover4 {
		if p.Contains(addr("10.0.0.4")) {
			t.Fatalf("excluded source leaked through cover %v", cover4)
		}
	}
	disjointAndMinimal(t, cover4)

	cover6, err := g.AllowedWithin(v6Universe)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range cover6 {
		if p.Contains(addr("2001:db8::4")) {
			t.Fatalf("excluded v6 source leaked through cover %v", cover6)
		}
	}
	disjointAndMinimal(t, cover6)

	// Exclude members permit everything outside the universe too: the global
	// cover must stay minimal (sibling prefixes merge back together).
	disjointAndMinimal(t, g.Allowed())
}

func TestAtomicModeSwitchAndLeaveIsolation(t *testing.T) {
	g, _, _, _ := fixture(t)

	keyB := MemberKey{Tenant: "tenant-b", Port: "port-b2"}

	// Atomic switch: B flips from exclude to a narrow include. Readers racing
	// the switch must always observe a valid disjoint cover.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					disjointAndMinimal(t, g.Allowed())
				}
			}
		}()
	}
	if err := g.Update(Membership{
		Tenant:  "tenant-b",
		Port:    "port-b2",
		Mode:    Include,
		Sources: []netip.Prefix{pfx("10.0.0.6/32")},
		Version: 8,
	}); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()

	if ev := g.EvidenceFor(addr("10.0.0.7")); hasMember(ev, keyB) {
		t.Fatalf("mode switch not effective: %v", ev)
	}
	if ev := g.EvidenceFor(addr("10.0.0.6")); !hasMember(ev, keyB) {
		t.Fatalf("new include missing: %v", ev)
	}

	// Stale version is fenced.
	err := g.Update(Membership{Tenant: "tenant-b", Port: "port-b2", Mode: Exclude, Version: 8})
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("want ErrStaleVersion, got %v", err)
	}

	// Leave revokes only B's contribution; A and C are untouched.
	if err := g.Leave(keyB, 8); err != nil {
		t.Fatal(err)
	}
	if ev := g.EvidenceFor(addr("10.0.0.6")); len(ev) != 0 {
		t.Fatalf("B still contributes after leave: %v", ev)
	}
	if ev := g.EvidenceFor(addr("10.0.0.1")); !hasMember(ev, MemberKey{Tenant: "tenant-a", Port: "port-a1"}) {
		t.Fatalf("A harmed by B leave: %v", ev)
	}
	if ev := g.EvidenceFor(addr("10.0.0.5")); !hasMember(ev, MemberKey{Tenant: "tenant-c", Port: "port-c3"}) {
		t.Fatalf("C harmed by B leave: %v", ev)
	}

	// Late re-publication at the removed version is rejected.
	err = g.Update(Membership{Tenant: "tenant-b", Port: "port-b2", Mode: Exclude, Version: 8})
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("late join after leave not fenced: %v", err)
	}
	// A higher-version rejoin is legitimate.
	if err := g.Update(Membership{
		Tenant:  "tenant-b",
		Port:    "port-b2",
		Mode:    Include,
		Sources: []netip.Prefix{pfx("10.0.0.7/32")},
		Version: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if ev := g.EvidenceFor(addr("10.0.0.7")); !hasMember(ev, keyB) {
		t.Fatalf("versioned rejoin failed: %v", ev)
	}
}

func TestPortReuseAcrossTenants(t *testing.T) {
	g, _, _, _ := fixture(t)
	tenantB := MemberKey{Tenant: "tenant-b", Port: "port-b2"}

	// Narrow B to a specific include so its exclude-complement cannot cover
	// the foreign tenant's source.
	if err := g.Update(Membership{
		Tenant:  "tenant-b",
		Port:    "port-b2",
		Mode:    Include,
		Sources: []netip.Prefix{pfx("10.0.0.6/32")},
		Version: 8,
	}); err != nil {
		t.Fatal(err)
	}

	// Same port identifier, different trusted tenant: distinct member.
	other := Membership{
		Tenant:  "tenant-z",
		Port:    "port-b2",
		Mode:    Include,
		Sources: []netip.Prefix{pfx("192.0.2.1/32")},
		Version: 1,
	}
	if err := g.Update(other); err != nil {
		t.Fatal(err)
	}
	tenantZ := MemberKey{Tenant: "tenant-z", Port: "port-b2"}
	if ev := g.EvidenceFor(addr("192.0.2.1")); !hasMember(ev, tenantZ) || hasMember(ev, tenantB) {
		t.Fatalf("tenant binding leaked across shared port id: %v", ev)
	}

	// Leaving tenant Z must not disturb tenant B's binding on the same port.
	if err := g.Leave(tenantZ, 1); err != nil {
		t.Fatal(err)
	}
	if ev := g.EvidenceFor(addr("10.0.0.6")); !hasMember(ev, tenantB) {
		t.Fatalf("tenant B lost after tenant Z leave: %v", ev)
	}
	if ev := g.EvidenceFor(addr("192.0.2.1")); hasMember(ev, tenantZ) {
		t.Fatalf("tenant Z still contributing after leave: %v", ev)
	}
	// B's own include on the reused port id must still work.
	if ev := g.EvidenceFor(addr("10.0.0.6")); !hasMember(ev, tenantB) {
		t.Fatalf("tenant B lost its include on shared port: %v", ev)
	}
}

func TestLeaseExpiryAndLateRenewal(t *testing.T) {
	g, clock, v4Universe, v6Universe := fixture(t)
	keyC := MemberKey{Tenant: "tenant-c", Port: "port-c3"}

	// In-time renewal extends the lease.
	if err := g.Renew(keyC, 3, time.Unix(3000, 0)); err != nil {
		t.Fatal(err)
	}
	clock.Store(2500)
	if ev := g.EvidenceFor(addr("10.0.0.5")); !hasMember(ev, keyC) {
		t.Fatalf("C expired despite renewal: %v", ev)
	}

	// Renewal at the wrong incarnation is a late message and must fail.
	err := g.Renew(keyC, 2, time.Unix(9000, 0))
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("late renewal version not fenced: %v", err)
	}

	// Let the lease lapse; Expire revokes only C.
	clock.Store(3000)
	expired := g.Expire()
	if len(expired) != 1 || expired[0] != keyC {
		t.Fatalf("expire targets = %v", expired)
	}
	if ev := g.EvidenceFor(addr("10.0.0.5")); hasMember(ev, keyC) {
		t.Fatalf("C still contributing after expiry: %v", ev)
	}
	// A and B remain live; B's exclude still denies .4.
	if ev := g.EvidenceFor(addr("10.0.0.1")); len(ev) == 0 {
		t.Fatalf("other members harmed by expiry")
	}
	if ev := g.EvidenceFor(addr("10.0.0.4")); len(ev) != 0 {
		t.Fatalf("excluded source leaked after expiry: %v", ev)
	}

	// Late renewal of the dead incarnation cannot resurrect the contribution.
	err = g.Renew(keyC, 3, time.Unix(9000, 0))
	if !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("post-expiry renewal not fenced: %v", err)
	}

	// A higher-version rejoin with a fresh lease is the only valid path back.
	if err := g.Update(Membership{
		Tenant:        "tenant-c",
		Port:          "port-c3",
		Mode:          Include,
		Sources:       []netip.Prefix{pfx("10.0.0.5/32")},
		Version:       4,
		LeaseDeadline: time.Unix(4000, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if ev := g.EvidenceFor(addr("10.0.0.5")); !hasMember(ev, keyC) {
		t.Fatalf("valid rejoin failed: %v", ev)
	}

	// Publishing with an already-expired deadline is rejected outright.
	err = g.Update(Membership{
		Tenant:        "tenant-c",
		Port:          "port-c3",
		Mode:          Include,
		Version:       5,
		LeaseDeadline: time.Unix(10, 0),
	})
	if !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired publication accepted: %v", err)
	}

	// Both universe queries still satisfy the disjoint/minimal invariant.
	c4, err := g.AllowedWithin(v4Universe)
	if err != nil {
		t.Fatal(err)
	}
	disjointAndMinimal(t, c4)
	c6, err := g.AllowedWithin(v6Universe)
	if err != nil {
		t.Fatal(err)
	}
	disjointAndMinimal(t, c6)
}

func TestPrefixComplementNeverEnumerates(t *testing.T) {
	// An exclude of one /32 must yield a small prefix-level complement rather
	// than 2^32-1 entries.
	s4 := newPrefixSet(fam4)
	s4.insert(pfx("10.0.0.4/32"))
	comp4 := complementOf(s4).emit()
	disjointAndMinimal(t, comp4)
	if len(comp4) > 32 {
		t.Fatalf("v4 complement exploded to %d prefixes", len(comp4))
	}
	for _, p := range comp4 {
		if p.Contains(addr("10.0.0.4")) {
			t.Fatalf("complement contains excluded address: %v", comp4)
		}
	}

	s6 := newPrefixSet(fam6)
	s6.insert(pfx("2001:db8::4/128"))
	comp6 := complementOf(s6).emit()
	disjointAndMinimal(t, comp6)
	if len(comp6) > 128 {
		t.Fatalf("v6 complement exploded to %d prefixes", len(comp6))
	}

	// Empty exclude means the whole family is permitted: single default route.
	empty4 := complementOf(newPrefixSet(fam4)).emit()
	if len(empty4) != 1 || empty4[0] != pfx("0.0.0.0/0") {
		t.Fatalf("empty v4 exclude = %v", empty4)
	}
	empty6 := complementOf(newPrefixSet(fam6)).emit()
	if len(empty6) != 1 || empty6[0] != pfx("::/0") {
		t.Fatalf("empty v6 exclude = %v", empty6)
	}

	// Sibling prefixes union into the covering prefix.
	u := newPrefixSet(fam4)
	u.insert(pfx("10.0.0.0/25"))
	u.insert(pfx("10.0.0.128/25"))
	got := u.emit()
	if len(got) != 1 || got[0] != pfx("10.0.0.0/24") {
		t.Fatalf("sibling merge = %v", got)
	}
}

func TestEmptyIncludeAndInvalidInput(t *testing.T) {
	g := NewGroup()

	// Empty include: silent member, permits nothing at all.
	if err := g.Update(Membership{
		Tenant:  "t",
		Port:    "p",
		Mode:    Include,
		Version: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if cover := g.Allowed(); len(cover) != 0 {
		t.Fatalf("empty include permits sources: %v", cover)
	}
	if ev := g.EvidenceFor(addr("10.0.0.1")); ev != nil {
		t.Fatalf("empty include produced evidence: %v", ev)
	}

	bad := []Membership{
		{Tenant: "t", Mode: Include, Version: 1},             // missing port
		{Tenant: "t", Port: "p", Mode: Include},              // zero version
		{Tenant: "t", Port: "p", Mode: Mode(99), Version: 1}, // bad mode
	}
	for _, m := range bad {
		if err := g.Update(m); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("bad membership %+v accepted: %v", m, err)
		}
	}
	if _, err := g.AllowedWithin(pfx("::ffff:10.0.0.1/128")); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("4-in-6 scope accepted: %v", err)
	}
}

func TestLeaveWithoutMembersIsFenced(t *testing.T) {
	g := NewGroup()
	key := MemberKey{Tenant: "t", Port: "p"}
	if err := g.Leave(key, 1); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("unknown leave = %v", err)
	}
	if err := g.Renew(key, 1, time.Time{}); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("unknown renew = %v", err)
	}
}
