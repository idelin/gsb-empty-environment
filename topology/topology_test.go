package topology

import "testing"

func ready(ip, zone, node string) Endpoint {
	return Endpoint{
		IP:         ip,
		Zone:       zone,
		Node:       node,
		Weight:     1,
		Conditions: EndpointConditions{Ready: true, Serving: true},
	}
}

func terminating(ip, zone, node string) Endpoint {
	return Endpoint{
		IP:   ip,
		Zone: zone,
		Node: node,
		Conditions: EndpointConditions{
			Ready:       false,
			Serving:     true,
			Terminating: true,
		},
	}
}

func failedEndpoint(ip, zone, node string) Endpoint {
	return Endpoint{
		IP:         ip,
		Zone:       zone,
		Node:       node,
		Conditions: EndpointConditions{Ready: false, Serving: false},
	}
}

func hintEnabledCfg() ServiceConfig {
	return ServiceConfig{
		TrafficDistribution:   TrafficDistributionPreferClose,
		InternalTrafficPolicy: InternalTrafficPolicyCluster,
	}
}

func backendIPs(table *RouteTable) []string {
	ips := make([]string, 0, len(table.Backends))
	for _, backend := range table.Backends {
		ips = append(ips, backend.Endpoint.IP)
	}
	return ips
}

func assertEqualStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("at %d: got %v want %v", i, got, want)
		}
	}
}

// TestSafeguardCPUImbalance covers acceptance case 1: skewed zone CPU plus
// endpoints concentrated in one zone makes the minimum quota infeasible, so
// every hint is cleared and the precise overload reason is reported.
func TestSafeguardCPUImbalance(t *testing.T) {
	zones := []ZoneCapacity{
		{Zone: "zone-a", CPU: 90},
		{Zone: "zone-b", CPU: 10},
	}
	slices := []EndpointSlice{{
		Name: "slice-1",
		Endpoints: []Endpoint{
			ready("10.0.0.1", "zone-a", "n-a-1"),
			ready("10.0.0.2", "zone-a", "n-a-2"),
		},
	}}

	allocation := AllocateHints(hintEnabledCfg(), zones, slices)
	if allocation.Active {
		t.Fatalf("expected safeguard fallback, got active hints: %#v", allocation.Hints)
	}
	if allocation.Reason != ReasonOverload {
		t.Fatalf("reason = %q, want %q (message: %s)", allocation.Reason, ReasonOverload, allocation.Message)
	}
	if allocation.Hints != nil {
		t.Fatalf("hints must be cleared on safeguard, got %#v", allocation.Hints)
	}
	if allocation.Message == "" {
		t.Fatal("degradation message must not be empty")
	}

	// Data plane must fall back to global routing when hints are inactive.
	table := CompileNodeRoutes(hintEnabledCfg(), "n-b-1", "zone-b", allocation, slices)
	if table.HintsApplied {
		t.Fatal("global fallback must not report hints applied")
	}
	assertEqualStrings(t, backendIPs(table), []string{"10.0.0.1", "10.0.0.2"})
}

func TestSafeguardReasons(t *testing.T) {
	slices := []EndpointSlice{{
		Name:      "slice-1",
		Endpoints: []Endpoint{ready("10.0.0.1", "zone-a", "n-a-1")},
	}}

	t.Run("hints disabled", func(t *testing.T) {
		cfg := ServiceConfig{TrafficDistribution: TrafficDistributionCluster}
		allocation := AllocateHints(cfg,
			[]ZoneCapacity{{Zone: "a", CPU: 1}, {Zone: "b", CPU: 1}}, slices)
		if allocation.Active || allocation.Reason != ReasonHintsDisabled {
			t.Fatalf("got active=%v reason=%q", allocation.Active, allocation.Reason)
		}
	})

	t.Run("single zone", func(t *testing.T) {
		allocation := AllocateHints(hintEnabledCfg(),
			[]ZoneCapacity{{Zone: "a", CPU: 1}}, slices)
		if allocation.Active || allocation.Reason != ReasonInsufficientZones {
			t.Fatalf("got active=%v reason=%q", allocation.Active, allocation.Reason)
		}
	})

	t.Run("zero cpu zone", func(t *testing.T) {
		zones := []ZoneCapacity{{Zone: "a", CPU: 1}, {Zone: "b", CPU: 0}}
		allocation := AllocateHints(hintEnabledCfg(), zones, slices)
		if allocation.Active || allocation.Reason != ReasonZeroCPUZone {
			t.Fatalf("got active=%v reason=%q", allocation.Active, allocation.Reason)
		}
	})

	t.Run("fewer endpoints than zones", func(t *testing.T) {
		zones := []ZoneCapacity{
			{Zone: "a", CPU: 1},
			{Zone: "b", CPU: 1},
			{Zone: "c", CPU: 1},
		}
		allocation := AllocateHints(hintEnabledCfg(), zones, slices)
		if allocation.Active || allocation.Reason != ReasonEndpointZoneCount {
			t.Fatalf("got active=%v reason=%q", allocation.Active, allocation.Reason)
		}
	})

	t.Run("invalid threshold", func(t *testing.T) {
		cfg := hintEnabledCfg()
		cfg.OverloadThreshold = 0.9
		allocation := AllocateHints(cfg,
			[]ZoneCapacity{{Zone: "a", CPU: 1}, {Zone: "b", CPU: 1}}, slices)
		if allocation.Active || allocation.Reason != ReasonInvalidConfig {
			t.Fatalf("got active=%v reason=%q", allocation.Active, allocation.Reason)
		}
	})
}

// TestCrossZoneMinimumOverloadFill covers acceptance case 2: the small zone
// lacks local endpoints, so the allocator steals surplus zone-a endpoints and
// deterministically fills the gap without overloading either zone.
func TestCrossZoneMinimumOverloadFill(t *testing.T) {
	zones := []ZoneCapacity{
		{Zone: "zone-a", CPU: 50},
		{Zone: "zone-b", CPU: 50},
	}
	slices := []EndpointSlice{{
		Name: "slice-1",
		Endpoints: []Endpoint{
			ready("10.0.0.1", "zone-a", "n-a-1"),
			ready("10.0.0.2", "zone-a", "n-a-2"),
			ready("10.0.0.3", "zone-a", "n-a-3"),
			ready("10.0.0.4", "zone-a", "n-a-4"),
			ready("10.0.0.5", "zone-a", "n-a-5"),
			ready("10.0.0.6", "zone-b", "n-b-1"),
		},
	}}

	allocation := AllocateHints(hintEnabledCfg(), zones, slices)
	if !allocation.Active {
		t.Fatalf("expected active hints, fallback: %s (%s)", allocation.Reason, allocation.Message)
	}

	counts := map[string]int{}
	crossZone := ""
	for ip, hint := range allocation.Hints {
		if len(hint.ForZones) != 1 {
			t.Fatalf("endpoint %s hint %v must contain exactly one zone", ip, hint.ForZones)
		}
		zone := hint.ForZones[0]
		counts[zone]++
		if endpointZone(allocation, ip) != zone {
			crossZone = ip
		}
	}
	if counts["zone-a"] != 3 || counts["zone-b"] != 3 {
		t.Fatalf("zone allocation = %v, want 3/3", counts)
	}
	if crossZone == "" {
		t.Fatal("expected at least one cross-zone fill into zone-b")
	}

	// Node in zone-b must only see the three endpoints hinted for zone-b,
	// including the cross-zone fill.
	tableB := CompileNodeRoutes(hintEnabledCfg(), "n-b-1", "zone-b", allocation, slices)
	if !tableB.HintsApplied || len(tableB.Backends) != 3 {
		t.Fatalf("zone-b table = %#v", tableB)
	}

	tableA := CompileNodeRoutes(hintEnabledCfg(), "n-a-1", "zone-a", allocation, slices)
	if !tableA.HintsApplied || len(tableA.Backends) != 3 {
		t.Fatalf("zone-a table = %#v", tableA)
	}
}

func endpointZone(allocation *Allocation, ip string) string {
	for _, ep := range allocation.ReadyEndpoints {
		if ep.IP == ip {
			return ep.Zone
		}
	}
	return ""
}

// TestLocalPolicyOverride covers acceptance case 3: Local ignores hints and
// global fallback, and only ever selects same-node endpoints.
func TestLocalPolicyOverride(t *testing.T) {
	zones := []ZoneCapacity{
		{Zone: "zone-a", CPU: 50},
		{Zone: "zone-b", CPU: 50},
	}
	slices := []EndpointSlice{{
		Name: "slice-1",
		Endpoints: []Endpoint{
			ready("10.0.0.1", "zone-a", "n-a-1"),
			ready("10.0.0.2", "zone-a", "n-a-2"),
			ready("10.0.0.3", "zone-b", "n-b-1"),
			ready("10.0.0.4", "zone-b", "n-b-1"),
		},
	}}
	allocation := AllocateHints(hintEnabledCfg(), zones, slices)
	if !allocation.Active {
		t.Fatalf("allocation fallback: %s", allocation.Message)
	}

	cfg := hintEnabledCfg()
	cfg.InternalTrafficPolicy = InternalTrafficPolicyLocal

	table := CompileNodeRoutes(cfg, "n-b-1", "zone-b", allocation, slices)
	if !table.Local || table.HintsApplied {
		t.Fatalf("local table flags = %#v", table)
	}
	assertEqualStrings(t, backendIPs(table), []string{"10.0.0.3", "10.0.0.4"})

	// Node with no local endpoints gets an empty table, never remote traffic.
	empty := CompileNodeRoutes(cfg, "n-a-9", "zone-a", allocation, slices)
	if len(empty.Backends) != 0 {
		t.Fatalf("unexpected backends: %v", backendIPs(empty))
	}

	// Local terminating fallback: no ready endpoint on the node.
	localFallback := []EndpointSlice{{
		Name: "slice-9",
		Endpoints: []Endpoint{
			terminating("10.0.9.1", "zone-a", "n-a-9"),
			ready("10.0.0.1", "zone-a", "n-a-1"),
		},
	}}
	fallback := CompileNodeRoutes(cfg, "n-a-9", "zone-a", allocation, localFallback)
	if !fallback.FallbackTerminating || len(fallback.Backends) != 1 {
		t.Fatalf("local terminating fallback = %#v", fallback)
	}
	if backendIPs(fallback)[0] != "10.0.9.1" || !fallback.Backends[0].Terminating {
		t.Fatalf("wrong fallback backend: %#v", fallback.Backends)
	}
}

// TestTerminatingFallback covers acceptance case 4: with no ready endpoint in
// the effective routing set, serving+terminating endpoints take over while
// failed endpoints stay excluded; zone hints still apply when available.
func TestTerminatingFallback(t *testing.T) {
	slices := []EndpointSlice{{
		Name: "slice-1",
		Endpoints: []Endpoint{
			terminating("10.0.0.1", "zone-a", "n-a-1"),
			terminating("10.0.0.2", "zone-b", "n-b-1"),
			failedEndpoint("10.0.0.3", "zone-a", "n-a-2"),
		},
	}}

	// No ready endpoints means the allocator itself must degrade.
	allocation := AllocateHints(hintEnabledCfg(),
		[]ZoneCapacity{{Zone: "zone-a", CPU: 1}, {Zone: "zone-b", CPU: 1}},
		slices,
	)
	if allocation.Active || allocation.Reason != ReasonEndpointZoneCount {
		t.Fatalf("got active=%v reason=%q", allocation.Active, allocation.Reason)
	}

	table := CompileNodeRoutes(hintEnabledCfg(), "n-a-1", "zone-a", allocation, slices)
	if !table.FallbackTerminating || table.HintsApplied {
		t.Fatalf("global terminating fallback flags = %#v", table)
	}
	assertEqualStrings(t, backendIPs(table), []string{"10.0.0.1", "10.0.0.2"})

	// With a still-valid hint set (e.g. cached before the ready endpoints
	// terminated), the terminating fallback must remain zone-local.
	hinted := &Allocation{
		Active:    true,
		Threshold: DefaultOverloadThreshold,
		Hints: map[string]HintsForZones{
			"10.0.0.1": {ForZones: []string{"zone-a"}},
			"10.0.0.2": {ForZones: []string{"zone-b"}},
		},
	}
	hintedTable := CompileNodeRoutes(hintEnabledCfg(), "n-a-1", "zone-a", hinted, slices)
	if !hintedTable.FallbackTerminating || !hintedTable.HintsApplied {
		t.Fatalf("hinted terminating fallback flags = %#v", hintedTable)
	}
	assertEqualStrings(t, backendIPs(hintedTable), []string{"10.0.0.1"})
}

// TestMergeSlicesDedup covers acceptance case 5: overlapping EndpointSlices
// are merged by IP with AND-ed conditions, max weight and stable identity.
func TestMergeSlicesDedup(t *testing.T) {
	slices := []EndpointSlice{
		{
			Name: "slice-z",
			Endpoints: []Endpoint{
				{
					IP:         "10.0.0.1",
					Zone:       "zone-a",
					Node:       "n-a-1",
					Weight:     5,
					Conditions: EndpointConditions{Ready: true, Serving: true, Terminating: true},
				},
			},
		},
		{
			Name: "slice-a",
			Endpoints: []Endpoint{
				{
					IP:         "10.0.0.1",
					Zone:       "zone-a",
					Node:       "n-a-1",
					Weight:     2,
					Conditions: EndpointConditions{Ready: false, Serving: true, Terminating: true},
				},
				{
					IP:         "10.0.0.2",
					Zone:       "zone-b",
					Node:       "n-b-1",
					Conditions: EndpointConditions{Ready: true, Serving: true},
				},
				{
					IP:         "10.0.0.5",
					Zone:       "zone-a",
					Node:       "n-a-2",
					Weight:     1,
					Conditions: EndpointConditions{Ready: true, Serving: true},
				},
			},
		},
	}

	merged := MergeSlices(slices)
	if len(merged) != 3 {
		t.Fatalf("merged length = %d, want 3: %#v", len(merged), merged)
	}

	first := merged[0]
	if first.IP != "10.0.0.1" {
		t.Fatalf("merged output must be IP-sorted, got %s", first.IP)
	}
	if first.Conditions.Ready {
		t.Fatal("ready flags must be AND-ed across duplicate slices")
	}
	if !first.Conditions.Serving || !first.Conditions.Terminating {
		t.Fatalf("serving/terminating merge wrong: %#v", first.Conditions)
	}
	if first.Weight != 5 {
		t.Fatalf("weight = %d, want max 5", first.Weight)
	}

	// A duplicate that becomes not-ready must exclude the IP from hint
	// allocation but keep it eligible as a terminating fallback.
	zones := []ZoneCapacity{{Zone: "zone-a", CPU: 50}, {Zone: "zone-b", CPU: 50}}
	allocation := AllocateHints(hintEnabledCfg(), zones, slices)
	if !allocation.Active {
		t.Fatalf("allocation fallback: %s", allocation.Message)
	}
	if _, ok := allocation.Hints["10.0.0.1"]; ok {
		t.Fatal("merged not-ready endpoint must not receive a hint")
	}
	if _, ok := allocation.Hints["10.0.0.5"]; !ok {
		t.Fatal("unique ready endpoint must receive a hint")
	}

	// While ready endpoints exist the merged terminating endpoint stays out of
	// the data path; the terminating fallback itself is covered separately.
	table := CompileNodeRoutes(hintEnabledCfg(), "n-a-1", "zone-a", nil, slices)
	if table.FallbackTerminating {
		t.Fatalf("terminating fallback must not activate with ready backends: %#v", table)
	}
	assertEqualStrings(t, backendIPs(table), []string{"10.0.0.2", "10.0.0.5"})
}

// TestHappyPathAndWeights exercises the normal hinted path: equal zones split
// endpoints evenly, weights are preserved and unset weights become 1.
func TestHappyPathAndWeights(t *testing.T) {
	zones := []ZoneCapacity{{Zone: "zone-a", CPU: 1}, {Zone: "zone-b", CPU: 1}}
	slices := []EndpointSlice{{
		Name: "slice-1",
		Endpoints: []Endpoint{
			ready("10.0.0.1", "zone-a", "n-a-1"),
			ready("10.0.0.2", "zone-a", "n-a-2"),
			ready("10.0.0.3", "zone-b", "n-b-1"),
			ready("10.0.0.4", "zone-b", "n-b-2"),
		},
	}}
	slices[0].Endpoints[0].Weight = 7

	allocation := AllocateHints(hintEnabledCfg(), zones, slices)
	if !allocation.Active || allocation.Threshold != DefaultOverloadThreshold {
		t.Fatalf("allocation = %#v", allocation)
	}

	table := CompileNodeRoutes(hintEnabledCfg(), "n-a-1", "zone-a", allocation, slices)
	if len(table.Backends) != 2 {
		t.Fatalf("backends = %#v", table.Backends)
	}
	weights := map[string]int{}
	for _, backend := range table.Backends {
		weights[backend.Endpoint.IP] = backend.Weight
	}
	if weights["10.0.0.1"] != 7 || weights["10.0.0.2"] != 1 {
		t.Fatalf("weight normalization wrong: %v", weights)
	}

	// Empty hint set for a zone (e.g. nil allocation) falls back globally.
	global := CompileNodeRoutes(hintEnabledCfg(), "n-a-1", "zone-a", nil, slices)
	if global.HintsApplied || len(global.Backends) != 4 {
		t.Fatalf("nil allocation must produce global routing: %#v", global)
	}
}

// TestAggregateZoneCapacity checks node-level CPU inputs roll up correctly.
func TestAggregateZoneCapacity(t *testing.T) {
	nodes := []NodeCapacity{
		{Node: "n-a-1", Zone: "zone-a", CPUCapacity: 4},
		{Node: "n-a-2", Zone: "zone-a", CPUCapacity: 6},
		{Node: "n-b-1", Zone: "zone-b", CPUCapacity: 5},
		{Node: "n-x", Zone: "zone-x", CPUCapacity: -1},
	}
	zones := AggregateZoneCapacity(nodes)
	if len(zones) != 2 {
		t.Fatalf("zones = %#v", zones)
	}
	if zones[0].Zone != "zone-a" || zones[0].CPU != 10 || zones[1].CPU != 5 {
		t.Fatalf("aggregation wrong: %#v", zones)
	}
}

// TestDeterministicAllocation ensures shuffled slice order yields identical
// hint assignments, which is required for stable control-plane convergence.
func TestDeterministicAllocation(t *testing.T) {
	zones := []ZoneCapacity{{Zone: "zone-a", CPU: 50}, {Zone: "zone-b", CPU: 50}}
	base := []Endpoint{
		ready("10.0.0.1", "zone-a", "n-a-1"),
		ready("10.0.0.2", "zone-a", "n-a-2"),
		ready("10.0.0.3", "zone-a", "n-a-3"),
		ready("10.0.0.4", "zone-b", "n-b-1"),
	}

	first := AllocateHints(hintEnabledCfg(), zones,
		[]EndpointSlice{{Name: "s", Endpoints: base}})

	shuffled := []Endpoint{base[2], base[0], base[3], base[1]}
	second := AllocateHints(hintEnabledCfg(), zones,
		[]EndpointSlice{{Name: "s", Endpoints: shuffled}})

	if !first.Active || !second.Active {
		t.Fatal("both allocations should be active")
	}
	for ip, hint := range first.Hints {
		if second.Hints[ip].ForZones[0] != hint.ForZones[0] {
			t.Fatalf("nondeterministic hint for %s: %v vs %v",
				ip, hint.ForZones, second.Hints[ip].ForZones)
		}
	}
}
