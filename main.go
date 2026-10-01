package main

import (
	"fmt"
	"net/netip"
)

func mustPrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		panic(err)
	}
	return p
}

func demoRules() []Rule {
	return []Rule{
		{ID: 1, Priority: 10, Prefix: mustPrefix("10.0.0.0/8"), Protocol: 6, PortLo: 80, PortHi: 80, Action: Allow},
		{ID: 2, Priority: 20, Prefix: mustPrefix("10.1.0.0/16"), Protocol: 6, PortLo: 0, PortHi: 65535, Action: Deny},
		{ID: 3, Priority: 30, Prefix: mustPrefix("10.0.0.0/8"), Protocol: 6, PortLo: 80, PortHi: 80, Action: Deny},
		{ID: 4, Priority: 40, Prefix: mustPrefix("192.168.0.0/16"), Protocol: 17, PortLo: 53, PortHi: 53, Action: Allow},
		{ID: 5, Priority: 50, Prefix: mustPrefix("192.168.0.0/16"), Protocol: 17, PortLo: 53, PortHi: 53, Action: Deny},
		{ID: 6, Priority: 60, Prefix: mustPrefix("2001:db8::/32"), Protocol: 6, PortLo: 443, PortHi: 443, Action: Allow},
		{ID: 7, Priority: 70, Prefix: mustPrefix("10.9.0.0/16"), Protocol: 6, PortLo: 100, PortHi: 200, Action: Allow},
		{ID: 8, Priority: 80, Prefix: mustPrefix("10.9.0.0/16"), Protocol: 6, PortLo: 201, PortHi: 300, Action: Allow},
		{ID: 9, Priority: 90, Prefix: mustPrefix("10.9.0.0/16"), Protocol: 6, PortLo: 100, PortHi: 300, Action: Deny},
	}
}

func main() {
	rules := demoRules()
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			panic(err)
		}
	}

	fmt.Println("== ruleset (evaluation order: priority asc) ==")
	for _, r := range sortedRules(rules) {
		fmt.Println(" ", r)
	}

	rep := Analyze(rules)

	fmt.Println("\n== unreachable rules (fully shadowed by earlier rules) ==")
	if len(rep.Unreachable) == 0 {
		fmt.Println("  none")
	}
	for _, u := range rep.Unreachable {
		fmt.Printf("  rule #%d is unreachable; counterexample %s\n", u.RuleID, u.Witness)
	}

	fmt.Println("\n== pairwise overlaps / conflicts ==")
	if len(rep.Findings) == 0 {
		fmt.Println("  none")
	}
	for _, f := range rep.Findings {
		fmt.Println(" ", f)
	}

	// Feed the synthetic counterexample packets through the
	// discrete-event model. All times are integer ticks; no real
	// network traffic is generated.
	fmt.Println("\n== discrete-event run (integer ticks, synthetic packets only) ==")
	sim := NewSim(NewClassifier(rules), &Link{Interval: 2, Delay: 3, Capacity: 2})
	tick := 0
	for _, u := range rep.Unreachable {
		sim.Inject(tick, u.Witness)
		tick++
	}
	for _, f := range rep.Findings {
		sim.Inject(tick, f.Witness)
		tick++
	}
	// One packet matching no rule at all -> default deny.
	sim.Inject(tick, Packet{ID: 900, Dst: netip.MustParseAddr("203.0.113.7"), Proto: 6, DstPort: 22})
	// A burst of allowed packets to exercise the queue capacity.
	for i := 0; i < 4; i++ {
		sim.Inject(tick+1, Packet{ID: 1000 + i, Dst: netip.MustParseAddr("10.0.0.9"), Proto: 6, DstPort: 80})
	}
	for _, line := range sim.Run(64) {
		fmt.Println(" ", line)
	}
	fmt.Printf("\ndelivered=%d dropped=%d\n", sim.Delivered, sim.Dropped)
}
