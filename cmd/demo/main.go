// Demo: versioned RIB with priorities, next-hop groups, atomic batches and
// a discrete-event packet simulation. No real network traffic is generated.
package main

import (
	"fmt"
	"net/netip"

	"rib"
	"rib/sim"
)

func mustPrefix(s string) netip.Prefix {
	p, err := rib.ParsePrefixStrict(s, rib.RejectHostBits)
	if err != nil {
		panic(err)
	}
	return p
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func main() {
	tab := rib.NewTable()

	fmt.Println("== 1. strict parsing ==")
	if _, err := rib.ParsePrefixStrict("10.0.0.1/8", rib.RejectHostBits); err != nil {
		fmt.Println("reject host bits :", err)
	}
	p, _ := rib.ParsePrefixStrict("10.0.0.1/8", rib.MaskHostBits)
	fmt.Println("mask policy      : 10.0.0.1/8 ->", p)

	fmt.Println("\n== 2. atomic batch publish ==")
	v1, _ := tab.Apply(
		rib.Add(mustPrefix("0.0.0.0/0"), rib.Route{Priority: 1, Tag: "default", NextHops: []netip.Addr{addr("192.0.2.1")}}),
		rib.Add(mustPrefix("203.0.113.0/24"), rib.Route{Priority: 10, Tag: "backup", NextHops: []netip.Addr{addr("198.51.100.2")}}),
		rib.Add(mustPrefix("203.0.113.0/24"), rib.Route{Priority: 100, Tag: "primary", NextHops: []netip.Addr{addr("198.51.100.1")}}),
		rib.Add(mustPrefix("2001:db8:e800::/61"), rib.Route{Priority: 50, Tag: "v6-nonbyte", NextHops: []netip.Addr{addr("2001:db8::1")}}),
	)
	fmt.Println("published version", v1)

	lookup := func(a string) {
		v := tab.Acquire()
		pf, r, ok, bits := v.LookupBits(addr(a))
		v.Release()
		if ok {
			fmt.Printf("lookup %-38s -> %-22s tag=%-9s pri=%-3d bits=%d\n", a, pf, r.Tag, r.Priority, bits)
		} else {
			fmt.Printf("lookup %-38s -> no route (bits=%d)\n", a, bits)
		}
	}
	lookup("203.0.113.9")
	lookup("203.0.114.9") // only default matches
	lookup("2001:db8:e800:0007::1")

	fmt.Println("\n== 3. withdraw restores next-best ==")
	v2, _ := tab.Apply(rib.Del(mustPrefix("203.0.113.0/24"), rib.Route{Priority: 100, Tag: "primary", NextHops: []netip.Addr{addr("198.51.100.1")}}))
	fmt.Println("withdrew primary, version", v2)
	lookup("203.0.113.9")

	fmt.Println("\n== 4. old snapshot still readable ==")
	fmt.Println("(see TestSnapshotReclaim for GC verification)")

	fmt.Println("\n== 5. discrete-event simulation (integer ticks) ==")
	s := sim.New()
	rt1 := rib.NewTable()
	r1 := sim.NewRouter("R1", rt1)
	r2 := sim.NewRouter("R2", rib.NewTable())
	dst := addr("203.0.113.7")
	r2.Local[dst] = true
	r2.Deliver = func(s *sim.Sim, p *sim.Packet) {
		fmt.Printf("tick %-3d R2 delivered packet %d (hops=%d, born=%d)\n", s.Now(), p.ID, p.Hops, p.Born)
	}
	nh := addr("198.51.100.1")
	link := &sim.Link{Name: "R1-R2", Interval: 2, Delay: 5}
	r1.Connect(nh, link, func(p *sim.Packet) { r2.Receive(s, p) })
	if _, err := rt1.Apply(rib.Add(mustPrefix("203.0.113.0/24"),
		rib.Route{Priority: 1, NextHops: []netip.Addr{nh}})); err != nil {
		panic(err)
	}
	for i := 1; i <= 3; i++ {
		id := i
		s.At(0, func() {
			fmt.Printf("tick %-3d inject packet %d at R1\n", s.Now(), id)
			r1.Receive(s, &sim.Packet{ID: id, Src: addr("192.0.2.1"), Dst: dst, Born: s.Now()})
		})
	}
	s.Run(1000)
}
