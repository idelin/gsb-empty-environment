// Command memberstate-demo walks a small source universe against a mixed
// include/exclude membership and prints the authorizing member evidence for
// every allowed source.
package main

import (
	"fmt"
	"net/netip"

	"prescreen.local/ps-71326fef7d/memberstate"
)

func mustPrefix(s string) netip.Prefix {
	return netip.MustParsePrefix(s)
}

func main() {
	g := memberstate.NewGroup()

	must := func(m memberstate.Membership) {
		if err := g.Update(m); err != nil {
			panic(err)
		}
	}

	must(memberstate.Membership{
		Tenant:  "tenant-a",
		Port:    "port-a1",
		Mode:    memberstate.Include,
		Sources: []netip.Prefix{mustPrefix("10.0.0.0/30"), mustPrefix("2001:db8::/126")},
		Version: 1,
	})
	must(memberstate.Membership{
		Tenant:  "tenant-b",
		Port:    "port-b2",
		Mode:    memberstate.Exclude,
		Sources: []netip.Prefix{mustPrefix("10.0.0.4/32"), mustPrefix("2001:db8::4/128")},
		Version: 7,
	})
	must(memberstate.Membership{
		Tenant:  "tenant-c",
		Port:    "port-c3",
		Mode:    memberstate.Include,
		Sources: []netip.Prefix{mustPrefix("10.0.0.5/32"), mustPrefix("2001:db8::5/128")},
		Version: 3,
	})

	universes := []netip.Prefix{mustPrefix("10.0.0.0/29"), mustPrefix("2001:db8::/125")}
	for _, universe := range universes {
		// g.Allowed() returns the whole-family cover; AllowedWithin projects
		// the exact disjoint pieces onto the data plane's small universe.
		cover, err := g.AllowedWithin(universe)
		if err != nil {
			panic(err)
		}
		fmt.Printf("merged disjoint cover inside %s:\n", universe)
		for _, p := range cover {
			fmt.Printf("  %s\n", p)
		}
	}

	for _, universe := range universes {
		fmt.Printf("\nsource universe %s\n", universe)
		decisions, err := g.Decisions(universe)
		if err != nil {
			panic(err)
		}
		for _, d := range decisions {
			if !d.Allowed {
				fmt.Printf("  DENY  %-18s (no authorizing member; exclusion holds)\n", d.Source)
				continue
			}
			fmt.Printf("  ALLOW %-18s evidence:\n", d.Source)
			for _, e := range d.Evidence {
				mode := "include"
				if e.Mode == memberstate.Exclude {
					mode = "exclude-complement"
				}
				fmt.Printf("         tenant=%-8s port=%-8s via %-18s rule=%s\n",
					e.Tenant, e.Port, mode, e.Rule)
			}
		}
	}
}
