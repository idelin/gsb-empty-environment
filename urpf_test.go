package netsec_test

import (
	"net/netip"
	"testing"

	netsec "example.org/netsec/q0734"
)

func TestAsymmetricRoute(t *testing.T) {
	table := mustTable(t, []netsec.Route{
		{Prefix: netip.MustParsePrefix("198.51.100.0/24"), Egress: "wan"},
		{Prefix: netip.MustParsePrefix("198.51.0.0/16"), Egress: "backup"},
	}, netsec.Config{})

	source := netip.MustParseAddr("198.51.100.10")

	if got := table.Check(source, "backup", netsec.ModeStrict); got.Allowed {
		t.Fatalf("strict mode allowed an asymmetric source: %+v", got)
	}
	if got := table.Check(source, "backup", netsec.ModeFeasible); !got.Allowed {
		t.Fatalf("feasible mode rejected a matching path: %+v", got)
	}
	if got := table.Check(source, "backup", netsec.ModeLoose); !got.Allowed {
		t.Fatalf("loose mode rejected an existing route: %+v", got)
	}
	if got := table.Check(source, "wan", netsec.ModeStrict); !got.Allowed {
		t.Fatalf("strict mode rejected a symmetric source: %+v", got)
	}
}

func TestDefaultRouteOnly(t *testing.T) {
	routes := []netsec.Route{
		{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Egress: "wan"},
	}
	source := netip.MustParseAddr("203.0.113.9")

	table := mustTable(t, routes, netsec.Config{})
	if got := table.Check(source, "wan", netsec.ModeStrict); !got.Allowed {
		t.Fatalf("strict mode rejected a default route through ingress: %+v", got)
	}
	if got := table.Check(source, "lan", netsec.ModeStrict); got.Allowed {
		t.Fatalf("strict mode allowed a default route through another interface: %+v", got)
	}
	if got := table.Check(source, "wan", netsec.ModeFeasible); !got.Allowed {
		t.Fatalf("feasible mode rejected a matching default route: %+v", got)
	}
	if got := table.Check(source, "lan", netsec.ModeFeasible); got.Allowed {
		t.Fatalf("feasible mode allowed a default route through another interface: %+v", got)
	}
	if got := table.Check(source, "wan", netsec.ModeLoose); got.Allowed {
		t.Fatalf("loose mode accepted a default route without configuration: %+v", got)
	}

	allowDefault := mustTable(t, routes, netsec.Config{AllowDefaultForLoose: true})
	if got := allowDefault.Check(source, "lan", netsec.ModeLoose); !got.Allowed {
		t.Fatalf("loose mode rejected an allowed default route: %+v", got)
	}

	withSpecific := mustTable(t, []netsec.Route{
		{Prefix: netip.MustParsePrefix("0.0.0.0/0"), Egress: "wan"},
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Egress: "inside"},
	}, netsec.Config{})
	if got := withSpecific.Check(netip.MustParseAddr("10.1.2.3"), "outside", netsec.ModeLoose); !got.Allowed {
		t.Fatalf("loose mode let a disabled default route veto a specific route: %+v", got)
	}
}

func TestBlackholeRoute(t *testing.T) {
	table := mustTable(t, []netsec.Route{
		{Prefix: netip.MustParsePrefix("203.0.113.0/24"), Blackhole: true},
	}, netsec.Config{AllowDefaultForLoose: true})
	source := netip.MustParseAddr("203.0.113.7")

	for _, mode := range []netsec.Mode{netsec.ModeStrict, netsec.ModeFeasible, netsec.ModeLoose} {
		t.Run(mode.String(), func(t *testing.T) {
			if got := table.Check(source, "wan", mode); got.Allowed {
				t.Fatalf("%s mode accepted a blackhole route: %+v", mode, got)
			}
		})
	}

	// A blackhole entry is not feasible, but a broader non-blackhole route is
	// still a valid feasible/loose return path.
	mixed := mustTable(t, []netsec.Route{
		{Prefix: netip.MustParsePrefix("203.0.113.0/24"), Blackhole: true},
		{Prefix: netip.MustParsePrefix("203.0.0.0/16"), Egress: "backup"},
	}, netsec.Config{})
	if got := mixed.Check(source, "backup", netsec.ModeStrict); got.Allowed {
		t.Fatalf("strict mode ignored a best blackhole route: %+v", got)
	}
	if got := mixed.Check(source, "backup", netsec.ModeFeasible); !got.Allowed {
		t.Fatalf("feasible mode ignored a non-blackhole path: %+v", got)
	}
	if got := mixed.Check(source, "backup", netsec.ModeLoose); !got.Allowed {
		t.Fatalf("loose mode ignored a non-blackhole route: %+v", got)
	}
}

func TestEqualCostMultipath(t *testing.T) {
	table := mustTable(t, []netsec.Route{
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Egress: "eth0", Metric: 10},
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Egress: "eth1", Metric: 10},
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Egress: "eth2", Metric: 20},
	}, netsec.Config{})
	source := netip.MustParseAddr("192.0.2.55")

	if got := table.Check(source, "eth1", netsec.ModeStrict); !got.Allowed {
		t.Fatalf("strict mode ignored a non-first equal-cost path: %+v", got)
	}
	if got := table.Check(source, "eth0", netsec.ModeStrict); !got.Allowed {
		t.Fatalf("strict mode rejected another equal-cost path: %+v", got)
	}
	if got := table.Check(source, "eth2", netsec.ModeStrict); got.Allowed {
		t.Fatalf("strict mode accepted a higher-metric path: %+v", got)
	}
	if got := table.Check(source, "eth2", netsec.ModeFeasible); !got.Allowed {
		t.Fatalf("feasible mode ignored a matching higher-metric path: %+v", got)
	}
}

func TestIPv6AndSourceValidation(t *testing.T) {
	table := mustTable(t, []netsec.Route{
		{Prefix: netip.MustParsePrefix("2001:db8::/32"), Egress: "core0"},
		{Prefix: netip.MustParsePrefix("192.0.2.0/24"), Egress: "edge0"},
	}, netsec.Config{})

	if got := table.Check(netip.MustParseAddr("2001:db8::42"), "core0", netsec.ModeStrict); !got.Allowed {
		t.Fatalf("strict mode rejected a symmetric IPv6 source: %+v", got)
	}
	if got := table.Check(netip.MustParseAddr("::ffff:192.0.2.10"), "edge0", netsec.ModeStrict); !got.Allowed {
		t.Fatalf("IPv4-mapped source was not normalized: %+v", got)
	}
	if got := table.Check(netip.Addr{}, "edge0", netsec.ModeLoose); got.Allowed {
		t.Fatalf("loose mode accepted an invalid source: %+v", got)
	}
	if got := table.Check(netip.MustParseAddr("0.0.0.0"), "edge0", netsec.ModeLoose); got.Allowed {
		t.Fatalf("loose mode accepted an unspecified source: %+v", got)
	}
	if got := table.Check(netip.MustParseAddr("224.0.0.1"), "edge0", netsec.ModeLoose); got.Allowed {
		t.Fatalf("loose mode accepted a multicast source: %+v", got)
	}
}

func TestNoRouteAndInvalidMode(t *testing.T) {
	table := mustTable(t, []netsec.Route{
		{Prefix: netip.MustParsePrefix("10.0.0.0/8"), Egress: "inside"},
	}, netsec.Config{})
	source := netip.MustParseAddr("192.0.2.1")

	for _, mode := range []netsec.Mode{netsec.ModeStrict, netsec.ModeFeasible, netsec.ModeLoose} {
		if got := table.Check(source, "outside", mode); got.Allowed {
			t.Fatalf("%s mode accepted a source with no route: %+v", mode, got)
		}
	}
	if got := table.Check(netip.MustParseAddr("10.0.0.1"), "inside", netsec.Mode(99)); got.Allowed {
		t.Fatalf("invalid mode accepted a source: %+v", got)
	}
}

func TestNewTableValidation(t *testing.T) {
	if _, err := netsec.NewTable([]netsec.Route{{Egress: "eth0"}}, netsec.Config{}); err == nil {
		t.Fatal("NewTable accepted an invalid prefix")
	}
	if _, err := netsec.NewTable([]netsec.Route{{
		Prefix: netip.MustParsePrefix("192.0.2.0/24"),
	}}, netsec.Config{}); err == nil {
		t.Fatal("NewTable accepted a non-blackhole route without an egress")
	}

	table := mustTable(t, []netsec.Route{{
		Prefix:    netip.MustParsePrefix("192.0.2.0/24"),
		Blackhole: true,
	}}, netsec.Config{})
	if got := table.Check(netip.MustParseAddr("192.0.2.1"), "eth0", netsec.ModeLoose); got.Allowed {
		t.Fatalf("blackhole route without egress was accepted: %+v", got)
	}
}

func mustTable(t *testing.T, routes []netsec.Route, config netsec.Config) *netsec.Table {
	t.Helper()
	table, err := netsec.NewTable(routes, config)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return table
}
