package dra

import "testing"

func TestSelectorMatching(t *testing.T) {
	dev := Device{
		Name: "gpu0",
		Attributes: map[string]AttributeValue{
			"model":   StringValue("a100"),
			"enabled": BoolValue(true),
			"cores":   Int64Value(108),
			"driver":  VersionValue("1.2.3"),
		},
		Capacities: map[string]int64{"memory": 80, "bandwidth": 900},
	}

	cases := []struct {
		name string
		sel  Selector
		want bool
	}{
		{"string equal", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "model", Op: OpEqual, Value: StringValue("a100")}}}, true},
		{"string not equal", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "model", Op: OpNotEqual, Value: StringValue("h100")}}}, true},
		{"string in", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "model", Op: OpIn, Value: StringValue("a100,h100")}}}, true},
		{"string not in", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "model", Op: OpNotIn, Value: StringValue("h100,b200")}}}, true},
		{"bool equal", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "enabled", Op: OpEqual, Value: BoolValue(true)}}}, true},
		{"int range lower", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "cores", Op: OpGreaterEqual, Value: Int64Value(64)}}}, true},
		{"int range upper reject", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "cores", Op: OpLessThan, Value: Int64Value(108)}}}, false},
		{"missing attribute", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "numa", Op: OpEqual, Value: Int64Value(0)}}}, false},
		{"semver compatible", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "driver", Op: OpSemverCompatible, Value: VersionValue("1.2.0")}}}, true},
		{"semver incompatible major", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "driver", Op: OpSemverCompatible, Value: VersionValue("2.0.0")}}}, false},
		{"capacity lower bound ok", Selector{CapacityRequirements: []CapacityRequirement{
			{Name: "memory", Op: OpCapacityGte, Value: 80}}}, true},
		{"capacity lower bound reject", Selector{CapacityRequirements: []CapacityRequirement{
			{Name: "memory", Op: OpCapacityGte, Value: 81}}}, false},
		{"missing capacity", Selector{CapacityRequirements: []CapacityRequirement{
			{Name: "vram", Op: OpCapacityGte, Value: 1}}}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := deviceMatches(dev, []Selector{tc.sel})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestClassAndRequestSelectorsCombine(t *testing.T) {
	dev := Device{
		Name: "fpga0",
		Attributes: map[string]AttributeValue{
			"vendor": StringValue("xilinx"),
			"region": StringValue("us-west"),
		},
	}
	sched := NewScheduler()
	sched.AddClass(DeviceClass{
		Name: "fpga",
		Selectors: []Selector{{
			AttributeRequirements: []AttributeRequirement{
				{Attribute: "vendor", Op: OpEqual, Value: StringValue("xilinx")},
			},
		}},
	})
	sched.ApplySlice(fullSlice("xilinx", "fpga-pool", dev))

	claim := &ResourceClaim{
		Namespace: "default", Name: "fpga-claim",
		Requests: []DeviceRequest{{
			Name: "part", ClassName: "fpga",
			AllocationMode: AllocationModeExactCount, Count: 1,
			Selectors: []Selector{{
				AttributeRequirements: []AttributeRequirement{
					{Attribute: "region", Op: OpEqual, Value: StringValue("us-east")},
				},
			}},
		}},
	}
	if ok, _ := sched.Filter(testNode, claim); ok {
		t.Fatal("class selector AND request selector must both match")
	}

	claim.Requests[0].Selectors[0].AttributeRequirements[0].Value = StringValue("us-west")
	if ok, err := sched.Filter(testNode, claim); err != nil || !ok {
		t.Fatalf("combined selectors should match, ok=%v err=%v", ok, err)
	}
}

func TestSemverPrecedence(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-1", "1.0.0-2", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
		{"1.0.1", "1.0.0", 1},
	}
	for _, tc := range cases {
		av, err := parseSemver(tc.a)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.a, err)
		}
		bv, err := parseSemver(tc.b)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.b, err)
		}
		if got := compareSemver(av, bv); got != tc.want {
			t.Fatalf("compare(%s,%s)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSemverCompatibleRanges(t *testing.T) {
	cases := []struct {
		device, requirement string
		want                bool
	}{
		{"1.2.3", "1.2.0", true},
		{"2.0.0", "1.2.0", false},
		{"0.2.5", "0.2.0", true},
		{"0.3.0", "0.2.0", false},
		{"0.0.3", "0.0.3", true},
		{"0.0.4", "0.0.3", false},
		{"1.5.0-rc1", "1.4.0", true},
	}
	for _, tc := range cases {
		dev, _ := parseSemver(tc.device)
		req, _ := parseSemver(tc.requirement)
		if got := semverCompatible(dev, req); got != tc.want {
			t.Fatalf("compatible(%s,%s)=%v want %v", tc.device, tc.requirement, got, tc.want)
		}
	}
}

func TestSelectorErrorBranches(t *testing.T) {
	dev := Device{
		Name:       "gpu0",
		Attributes: map[string]AttributeValue{"s": StringValue("x"), "n": Int64Value(1)},
		Capacities: map[string]int64{"c": 10},
	}
	cases := []struct {
		name string
		sel  Selector
	}{
		{"in on ints", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "n", Op: OpIn, Value: StringValue("1")}}}},
		{"order on strings", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "s", Op: OpLessThan, Value: StringValue("y")}}}},
		{"order type mismatch", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "n", Op: OpLessThan, Value: StringValue("y")}}}},
		{"semver on non-version", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "s", Op: OpSemverCompatible, Value: VersionValue("1.0.0")}}}},
		{"equal type mismatch", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "n", Op: OpEqual, Value: StringValue("1")}}}},
		{"unknown attribute op", Selector{AttributeRequirements: []AttributeRequirement{
			{Attribute: "s", Op: "Bogus", Value: StringValue("x")}}}},
		{"unknown capacity op", Selector{CapacityRequirements: []CapacityRequirement{
			{Name: "c", Op: "Bogus", Value: 1}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := deviceMatches(dev, []Selector{tc.sel}); err == nil {
				t.Fatal("expected error for malformed selector")
			}
		})
	}

	// Capacity operators other than GtEq still evaluate correctly.
	for _, op := range []MatchOp{OpEqual, OpNotEqual, OpLessThan, OpLessEqual, OpGreaterThan, OpGreaterEqual} {
		if _, err := deviceMatches(dev, []Selector{{
			CapacityRequirements: []CapacityRequirement{{Name: "c", Op: op, Value: 10}},
		}}); err != nil {
			t.Fatalf("capacity op %s errored: %v", op, err)
		}
	}

	// Malformed version strings surface through semver equality.
	bad := Device{Attributes: map[string]AttributeValue{"v": VersionValue("not-a-version")}}
	if _, err := deviceMatches(bad, []Selector{{
		AttributeRequirements: []AttributeRequirement{
			{Attribute: "v", Op: OpEqual, Value: VersionValue("1.0.0")},
		},
	}}); err == nil {
		t.Fatal("malformed version must error")
	}
}
