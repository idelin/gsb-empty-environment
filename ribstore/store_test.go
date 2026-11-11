package ribstore

import (
	"net/netip"
	"reflect"
	"testing"
	"time"
)

func pfx(s string) netip.Prefix { return netip.MustParsePrefix(s) }

func recv(t *testing.T, sub *Subscription) Delta {
	t.Helper()
	select {
	case d, ok := <-sub.C:
		if !ok {
			t.Fatal("subscription channel closed unexpectedly")
		}
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delta")
		return Delta{}
	}
}

func expectNoDelta(t *testing.T, sub *Subscription) {
	t.Helper()
	select {
	case d := <-sub.C:
		t.Fatalf("unexpected delta: %+v", d)
	case <-time.After(50 * time.Millisecond):
	}
}

// replica is a subscriber-side table fed by the delta stream.
type replica struct {
	table map[netip.Prefix]RouteView
}

func newReplica() *replica { return &replica{table: make(map[netip.Prefix]RouteView)} }

func (r *replica) apply(d Delta) {
	if d.FullSync {
		r.table = make(map[netip.Prefix]RouteView)
	}
	for _, p := range d.Withdrawn {
		delete(r.table, p)
	}
	for _, v := range d.Announced {
		r.table[v.Prefix] = v
	}
}

func (r *replica) views() []RouteView {
	out := make([]RouteView, 0, len(r.table))
	for _, v := range r.table {
		out = append(out, v)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if lessView(out[j], out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestValidationStates(t *testing.T) {
	s := New()
	s.AddROA(ROA{Prefix: pfx("10.0.0.0/16"), MaxLength: 24, ASN: 65001})
	s.AddRoute("n1", pfx("10.0.1.0/24"), 65001)  // valid
	s.AddRoute("n1", pfx("10.0.2.0/25"), 65001)  // too specific
	s.AddRoute("n1", pfx("10.0.3.0/24"), 65002)  // wrong origin
	s.AddRoute("n1", pfx("192.0.2.0/24"), 65001) // no ROA

	cases := map[string]ValidationState{
		"10.0.1.0/24":  Valid,
		"10.0.2.0/25":  InvalidMaxLength,
		"10.0.3.0/24":  InvalidOriginAS,
		"192.0.2.0/24": NotFound,
	}
	for p, want := range cases {
		v, ok := s.Lookup(pfx(p))
		if !ok {
			t.Fatalf("Lookup(%s) not found", p)
		}
		if v.Validation != want {
			t.Errorf("Lookup(%s).Validation = %v, want %v", p, v.Validation, want)
		}
	}
}

func TestPerNeighborBestAndWithdraw(t *testing.T) {
	s := New()
	p := pfx("192.0.2.0/24")
	sub := s.Subscribe(16)
	recv(t, sub) // initial empty snapshot

	s.AddRoute("n2", p, 65002)
	s.AddRoute("n1", p, 65001) // n1 < n2, becomes best

	d := recv(t, sub)
	if got := d.Announced[0].Neighbor; got != "n2" {
		t.Fatalf("best after first announce = %q, want n2", got)
	}
	d = recv(t, sub)
	if got := d.Announced[0].Neighbor; got != "n1" {
		t.Fatalf("best after second announce = %q, want n1", got)
	}

	// Withdrawing the best falls back to the remaining neighbor.
	s.WithdrawRoute("n1", p)
	d = recv(t, sub)
	if len(d.Announced) != 1 || d.Announced[0].Neighbor != "n2" {
		t.Fatalf("fallback delta = %+v, want announce from n2", d)
	}

	// Withdrawing the last route withdraws the prefix.
	s.WithdrawRoute("n2", p)
	d = recv(t, sub)
	if len(d.Withdrawn) != 1 || d.Withdrawn[0] != p || len(d.Announced) != 0 {
		t.Fatalf("final delta = %+v, want withdrawal of %s", d, p)
	}
	if _, ok := s.Lookup(p); ok {
		t.Fatal("prefix still present after all withdrawals")
	}
}

func TestWithdrawNeighborIsAtomic(t *testing.T) {
	s := New()
	s.AddRoute("n1", pfx("192.0.2.0/24"), 65001)
	s.AddRoute("n1", pfx("198.51.100.0/24"), 65001)
	s.AddRoute("n2", pfx("198.51.100.0/24"), 65002)
	v := s.Version()

	sub := s.Subscribe(16)
	recv(t, sub)

	s.WithdrawNeighbor("n1")
	if got := s.Version(); got != v+1 {
		t.Fatalf("neighbor withdraw bumped version by %d, want 1", got-v)
	}
	d := recv(t, sub)
	if len(d.Withdrawn) != 1 || d.Withdrawn[0] != pfx("192.0.2.0/24") {
		t.Errorf("withdrawn = %v, want [192.0.2.0/24]", d.Withdrawn)
	}
	if len(d.Announced) != 1 || d.Announced[0].Neighbor != "n2" {
		t.Errorf("announced = %+v, want fallback to n2", d.Announced)
	}
}

func TestROAUpdateRevalidatesOnlyCoveredPrefixes(t *testing.T) {
	s := New()
	s.AddRoute("n1", pfx("10.0.1.0/24"), 65001)
	s.AddRoute("n1", pfx("10.0.2.0/24"), 65001)
	s.AddRoute("n1", pfx("192.0.2.0/24"), 65001)
	before := s.Stats().Revalidations

	sub := s.Subscribe(16)
	recv(t, sub)

	s.AddROA(ROA{Prefix: pfx("10.0.0.0/16"), MaxLength: 24, ASN: 65001})

	if got := s.Stats().Revalidations - before; got != 2 {
		t.Fatalf("revalidations = %d, want 2 (only covered prefixes)", got)
	}
	d := recv(t, sub)
	if len(d.Withdrawn) != 0 {
		t.Fatalf("unexpected withdrawals: %v", d.Withdrawn)
	}
	var got []netip.Prefix
	for _, v := range d.Announced {
		if v.Validation != Valid {
			t.Errorf("%s validation = %v, want Valid", v.Prefix, v.Validation)
		}
		got = append(got, v.Prefix)
	}
	want := []netip.Prefix{pfx("10.0.1.0/24"), pfx("10.0.2.0/24")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("revalidated prefixes = %v, want %v", got, want)
	}
	// The unrelated prefix must not be re-announced.
	expectNoDelta(t, sub)
}

func TestROARemovalRestoresNotFound(t *testing.T) {
	s := New()
	roa := ROA{Prefix: pfx("10.0.0.0/16"), MaxLength: 24, ASN: 65001}
	s.AddRoute("n1", pfx("10.0.1.0/24"), 65001)
	s.AddROA(roa)
	if v, _ := s.Lookup(pfx("10.0.1.0/24")); v.Validation != Valid {
		t.Fatalf("validation = %v, want Valid", v.Validation)
	}
	s.RemoveROA(roa)
	if v, _ := s.Lookup(pfx("10.0.1.0/24")); v.Validation != NotFound {
		t.Fatalf("validation after ROA removal = %v, want NotFound", v.Validation)
	}
}

func TestLiveStreamIsAtomicAndContiguous(t *testing.T) {
	s := New()
	s.AddRoute("n1", pfx("10.0.1.0/24"), 65001)

	sub := s.Subscribe(16)
	r := newReplica()
	d := recv(t, sub)
	if !d.FullSync {
		t.Fatal("first delta must be a full sync")
	}
	r.apply(d)
	prev := d.Version

	s.AddROA(ROA{Prefix: pfx("10.0.0.0/16"), MaxLength: 24, ASN: 65001})
	s.AddRoute("n1", pfx("10.0.2.0/24"), 65002)
	s.WithdrawRoute("n1", pfx("10.0.1.0/24"))

	for i := 0; i < 3; i++ {
		d = recv(t, sub)
		if d.FullSync {
			t.Fatal("live delta must be incremental")
		}
		if d.Version != prev+1 {
			t.Fatalf("version jumped %d -> %d, stream not contiguous", prev, d.Version)
		}
		prev = d.Version
		r.apply(d)
	}
	if !reflect.DeepEqual(r.views(), s.Snapshot()) {
		t.Fatalf("replica %v != snapshot %v", r.views(), s.Snapshot())
	}
}

func TestReconnectReplayMergesWithoutDuplicates(t *testing.T) {
	s := New()
	p1, p2, p3 := pfx("10.0.1.0/24"), pfx("10.0.2.0/24"), pfx("10.0.3.0/24")
	s.AddRoute("n1", p1, 65001)
	s.AddRoute("n1", p2, 65001)
	mark := s.Version()

	// Churn that nets out for the disconnected subscriber:
	// p1 withdrawn then re-announced identically; p2 re-announced by a
	// lower-priority neighbor (best unchanged); p3 newly announced.
	s.WithdrawRoute("n1", p1)
	s.AddRoute("n1", p1, 65001)
	s.AddRoute("n2", p2, 65001)
	s.AddRoute("n1", p3, 65001)

	sub := s.SubscribeFrom(mark, 16)
	d := recv(t, sub)
	if d.FullSync {
		t.Fatal("expected merged incremental replay, got full sync")
	}
	if d.Version != s.Version() {
		t.Fatalf("replay version = %d, want %d", d.Version, s.Version())
	}
	if len(d.Announced) != 1 || d.Announced[0].Prefix != p3 {
		t.Fatalf("merged replay = %+v, want only the new p3 route", d)
	}
	if len(d.Withdrawn) != 0 {
		t.Fatalf("unexpected withdrawals in replay: %v", d.Withdrawn)
	}
	expectNoDelta(t, sub)
}

func TestReplayIncludesNetWithdrawal(t *testing.T) {
	s := New()
	p1 := pfx("10.0.1.0/24")
	s.AddRoute("n1", p1, 65001)
	mark := s.Version()
	s.WithdrawRoute("n1", p1)

	sub := s.SubscribeFrom(mark, 16)
	d := recv(t, sub)
	if len(d.Withdrawn) != 1 || d.Withdrawn[0] != p1 || len(d.Announced) != 0 {
		t.Fatalf("replay = %+v, want withdrawal of %s", d, p1)
	}
}

func TestFullTableSwitchAfterCompaction(t *testing.T) {
	s := New()
	s.AddRoute("n1", pfx("10.0.1.0/24"), 65001)
	s.AddRoute("n1", pfx("10.0.2.0/24"), 65001)
	staleMark := s.Version()
	s.WithdrawRoute("n1", pfx("10.0.1.0/24"))
	s.AddRoute("n1", pfx("10.0.3.0/24"), 65002)
	s.Compact(1)

	// A subscriber whose last version fell out of the journal gets a full
	// sync that replaces its table, so stale routes disappear instead of
	// being replayed.
	sub := s.SubscribeFrom(staleMark, 16)
	d := recv(t, sub)
	if !d.FullSync {
		t.Fatalf("expected full sync after compaction, got %+v", d)
	}
	r := newReplica()
	r.table[pfx("10.0.1.0/24")] = RouteView{Prefix: pfx("10.0.1.0/24"), Neighbor: "n1", OriginASN: 65001}
	r.apply(d)
	if !reflect.DeepEqual(r.views(), s.Snapshot()) {
		t.Fatalf("replica %v != snapshot %v", r.views(), s.Snapshot())
	}
	if _, ok := r.table[pfx("10.0.1.0/24")]; ok {
		t.Fatal("stale route survived the full-table switch")
	}

	// Live deltas keep flowing after the switch.
	s.AddRoute("n1", pfx("10.0.4.0/24"), 65001)
	d = recv(t, sub)
	if d.FullSync || len(d.Announced) != 1 {
		t.Fatalf("post-switch delta = %+v, want incremental announce", d)
	}
}

func TestSubscribeFromCurrentVersionSendsNothing(t *testing.T) {
	s := New()
	s.AddRoute("n1", pfx("10.0.1.0/24"), 65001)
	sub := s.SubscribeFrom(s.Version(), 16)
	expectNoDelta(t, sub)
	s.AddRoute("n1", pfx("10.0.2.0/24"), 65001)
	d := recv(t, sub)
	if len(d.Announced) != 1 || d.Announced[0].Prefix != pfx("10.0.2.0/24") {
		t.Fatalf("live delta = %+v", d)
	}
}

func TestClosedSubscriptionStopsReceiving(t *testing.T) {
	s := New()
	sub := s.Subscribe(16)
	recv(t, sub)
	sub.Close()
	s.AddRoute("n1", pfx("10.0.1.0/24"), 65001)
	if _, ok := <-sub.C; ok {
		t.Fatal("expected closed channel")
	}
	// Store remains usable.
	if got := s.Stats().Prefixes; got != 1 {
		t.Fatalf("prefixes = %d, want 1", got)
	}
}

func TestIPv6RoutesAndROAs(t *testing.T) {
	s := New()
	s.AddROA(ROA{Prefix: pfx("2001:db8::/32"), MaxLength: 48, ASN: 65001})
	s.AddRoute("n1", pfx("2001:db8:1::/48"), 65001)
	s.AddRoute("n1", pfx("2001:db8:2::/64"), 65001)
	if v, _ := s.Lookup(pfx("2001:db8:1::/48")); v.Validation != Valid {
		t.Errorf("v6 /48 validation = %v, want Valid", v.Validation)
	}
	if v, _ := s.Lookup(pfx("2001:db8:2::/64")); v.Validation != InvalidMaxLength {
		t.Errorf("v6 /64 validation = %v, want InvalidMaxLength", v.Validation)
	}
}
