package sim

import (
	"net/netip"
	"testing"

	"rib"
)

func mustPrefix(s string) netip.Prefix {
	p, err := rib.ParsePrefixStrict(s, rib.RejectHostBits)
	if err != nil {
		panic(err)
	}
	return p
}

func mustAddr(s string) netip.Addr { return netip.MustParseAddr(s) }

type delivery struct {
	id   int
	tick uint64
	hops int
}

// buildLine creates src --link--> dst routers; dst terminates dstNet hosts.
func buildLine(t *testing.T, interval, delay uint64, prefix string) (*Sim, *Router, *Router, *[]delivery) {
	t.Helper()
	s := New()
	rt1, rt2 := rib.NewTable(), rib.NewTable()
	r1 := NewRouter("R1", rt1)
	r2 := NewRouter("R2", rt2)
	var got []delivery
	dstNet := mustAddr("203.0.113.7")
	r2.Local[dstNet] = true
	r2.Deliver = func(s *Sim, p *Packet) {
		got = append(got, delivery{id: p.ID, tick: s.Now(), hops: p.Hops})
	}
	nh := mustAddr("10.0.0.2")
	l := &Link{Name: "R1-R2", Interval: interval, Delay: delay}
	r1.Connect(nh, l, func(p *Packet) { r2.Receive(s, p) })
	if _, err := rt1.Apply(rib.Add(mustPrefix(prefix), rib.Route{Priority: 1, NextHops: []netip.Addr{nh}})); err != nil {
		t.Fatal(err)
	}
	return s, r1, r2, &got
}

// TestLinkTiming: 3 packets injected at tick 0 on a link with Interval=2,
// Delay=5 depart at 2,4,6 and arrive at 7,9,11, in FIFO order.
func TestLinkTiming(t *testing.T) {
	s, r1, _, got := buildLine(t, 2, 5, "203.0.113.0/24")
	for i := 1; i <= 3; i++ {
		p := &Packet{ID: i, Src: mustAddr("192.0.2.1"), Dst: mustAddr("203.0.113.7"), Born: 0}
		r1.Receive(s, p)
	}
	s.Run(1000)
	want := []delivery{{1, 7, 2}, {2, 9, 2}, {3, 11, 2}}
	if len(*got) != len(want) {
		t.Fatalf("got %v", *got)
	}
	for i, d := range *got {
		if d != want[i] {
			t.Fatalf("delivery %d = %+v, want %+v", i, d, want[i])
		}
	}
}

// TestRerouteOnWithdraw: traffic follows the best route; withdrawing the
// best route mid-simulation restores the next-best (same prefix, lower
// priority), and withdrawing that falls back to a less specific prefix.
func TestRerouteOnWithdraw(t *testing.T) {
	s := New()
	tab := rib.NewTable()
	r1 := NewRouter("R1", tab)
	r2 := NewRouter("R2", rib.NewTable())
	r3 := NewRouter("R3", rib.NewTable())
	dst := mustAddr("203.0.113.7")
	var via []string
	r2.Local[dst] = true
	r2.Deliver = func(s *Sim, p *Packet) { via = append(via, "R2@"+itoa(s.Now())) }
	r3.Local[dst] = true
	r3.Deliver = func(s *Sim, p *Packet) { via = append(via, "R3@"+itoa(s.Now())) }

	nh2, nh3 := mustAddr("10.0.0.2"), mustAddr("10.0.0.3")
	l2 := &Link{Name: "R1-R2", Interval: 1, Delay: 5}
	l3 := &Link{Name: "R1-R3", Interval: 1, Delay: 5}
	r1.Connect(nh2, l2, func(p *Packet) { r2.Receive(s, p) })
	r1.Connect(nh3, l3, func(p *Packet) { r3.Receive(s, p) })

	p24 := mustPrefix("203.0.113.0/24")
	p16 := mustPrefix("203.0.0.0/16")
	hi := rib.Route{Priority: 100, NextHops: []netip.Addr{nh2}}
	lo := rib.Route{Priority: 10, NextHops: []netip.Addr{nh3}}
	fallback := rib.Route{Priority: 1, NextHops: []netip.Addr{nh3}}
	if _, err := tab.Apply(
		rib.Add(p24, hi), rib.Add(p24, lo), rib.Add(p16, fallback),
	); err != nil {
		t.Fatal(err)
	}

	inject := func(id int) {
		r1.Receive(s, &Packet{ID: id, Src: mustAddr("192.0.2.1"), Dst: dst, Born: s.Now()})
	}
	s.At(0, func() { inject(1) }) // -> R2 (best priority)
	s.At(10, func() {
		if _, err := tab.Apply(rib.Del(p24, hi)); err != nil {
			t.Error(err)
		}
	})
	s.At(20, func() { inject(2) }) // -> R3 (next-best same-prefix route)
	s.At(30, func() {
		if _, err := tab.Apply(rib.Del(p24, lo)); err != nil {
			t.Error(err)
		}
	})
	s.At(40, func() { inject(3) }) // -> R3 via less-specific /16
	s.Run(1000)

	want := []string{"R2@6", "R3@26", "R3@46"}
	if len(via) != len(want) {
		t.Fatalf("via = %v, want %v", via, want)
	}
	for i := range want {
		if via[i] != want[i] {
			t.Fatalf("via = %v, want %v", via, want)
		}
	}
}

// TestDropNoRoute: packets with no matching route are dropped, not sent.
func TestDropNoRoute(t *testing.T) {
	s := New()
	r := NewRouter("R1", rib.NewTable())
	var drops int
	r.OnDrop = func(s *Sim, p *Packet) { drops++ }
	r.Receive(s, &Packet{ID: 1, Src: mustAddr("192.0.2.1"), Dst: mustAddr("203.0.113.7")})
	s.Run(100)
	if drops != 1 {
		t.Fatalf("drops = %d, want 1", drops)
	}
}

// TestECMPGroup: both next hops of a group are reachable; flow hash picks
// deterministically.
func TestECMPGroup(t *testing.T) {
	s := New()
	tab := rib.NewTable()
	r1 := NewRouter("R1", tab)
	r2 := NewRouter("R2", rib.NewTable())
	r3 := NewRouter("R3", rib.NewTable())
	dst := mustAddr("203.0.113.7")
	r2.Local[dst] = true
	r3.Local[dst] = true
	var c2, c3 int
	r2.Deliver = func(s *Sim, p *Packet) { c2++ }
	r3.Deliver = func(s *Sim, p *Packet) { c3++ }
	nh2, nh3 := mustAddr("10.0.0.2"), mustAddr("10.0.0.3")
	l2 := &Link{Name: "R1-R2", Interval: 1, Delay: 1}
	l3 := &Link{Name: "R1-R3", Interval: 1, Delay: 1}
	r1.Connect(nh2, l2, func(p *Packet) { r2.Receive(s, p) })
	r1.Connect(nh3, l3, func(p *Packet) { r3.Receive(s, p) })
	if _, err := tab.Apply(rib.Add(mustPrefix("203.0.113.0/24"),
		rib.Route{Priority: 1, NextHops: []netip.Addr{nh2, nh3}})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		src := mustAddr("192.0.2.1").Next()
		for j := 0; j < i; j++ {
			src = src.Next()
		}
		r1.Receive(s, &Packet{ID: i, Src: src, Dst: dst, Born: s.Now()})
	}
	s.Run(1000)
	if c2+c3 != 20 {
		t.Fatalf("delivered %d, want 20", c2+c3)
	}
	if c2 == 0 || c3 == 0 {
		t.Fatalf("ECMP did not spread: R2=%d R3=%d", c2, c3)
	}
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
