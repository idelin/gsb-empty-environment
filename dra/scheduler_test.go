package dra

import (
	"errors"
	"testing"
)

const testNode = "node-a"

func gpuDevice(name string, attrs map[string]AttributeValue, memory int64) Device {
	return Device{
		Name:       name,
		Attributes: attrs,
		Capacities: map[string]int64{"memory": memory},
	}
}

func fullSlice(driver, pool string, devices ...Device) ResourceSlice {
	return ResourceSlice{
		Name:     driver + "-" + pool + "-0",
		NodeName: testNode,
		Driver:   driver,
		Pool: ResourcePool{
			Name:               pool,
			Generation:         1,
			ResourceSliceCount: 1,
		},
		Devices: devices,
	}
}

func podConsumer(id, name string) Consumer {
	return Consumer{UID: id, Namespace: "default", Name: name}
}

func allocationKeys(alloc *AllocationResult) map[string][]string {
	out := map[string][]string{}
	for _, res := range alloc.Results {
		for _, dev := range res.Devices {
			out[res.Request] = append(out[res.Request], dev.Key())
		}
	}
	return out
}

// TestGreedyTrapBacktracking constructs the classic deadlock: request A can
// take gpu0/gpu1 while only gpu0 satisfies B. A greedy first-fit on A would
// steal gpu0 and fail; the backtracking solver must find A=gpu1, B=gpu0.
func TestGreedyTrapBacktracking(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})

	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100"), "p2p": BoolValue(true)}, 80),
		gpuDevice("gpu1", map[string]AttributeValue{"model": StringValue("a100"), "p2p": BoolValue(false)}, 40),
	))

	claim := &ResourceClaim{
		Namespace: "default",
		Name:      "greedy",
		Requests: []DeviceRequest{
			{
				Name: "a", ClassName: "gpu",
				AllocationMode: AllocationModeExactCount, Count: 1,
			},
			{
				Name: "b", ClassName: "gpu",
				AllocationMode: AllocationModeExactCount, Count: 1,
				Selectors: []Selector{{
					AttributeRequirements: []AttributeRequirement{
						{Attribute: "model", Op: OpEqual, Value: StringValue("a100")},
						{Attribute: "p2p", Op: OpEqual, Value: BoolValue(true)},
					},
					CapacityRequirements: []CapacityRequirement{
						{Name: "memory", Op: OpCapacityGte, Value: 80},
					},
				}},
			},
		},
	}

	ok, err := sched.Filter(testNode, claim)
	if err != nil {
		t.Fatalf("Filter returned error: %v", err)
	}
	if !ok {
		t.Fatal("Filter rejected a satisfiable claim")
	}

	if err := sched.Reserve(testNode, claim, podConsumer("p1", "pod1")); err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	stored, ok := sched.GetClaim("default", "greedy")
	if !ok {
		t.Fatal("claim not stored")
	}
	keys := allocationKeys(stored.Allocation)
	if len(keys["a"]) != 1 || len(keys["b"]) != 1 {
		t.Fatalf("expected one device per request, got %v", keys)
	}
	if keys["a"][0] == keys["b"][0] {
		t.Fatalf("requests share device %s: mutual exclusion violated", keys["a"][0])
	}
	if keys["b"][0] != "nv/pool/gpu0" {
		t.Fatalf("backtracking should leave gpu0 for request B, got %v", keys)
	}
	if keys["a"][0] != "nv/pool/gpu1" {
		t.Fatalf("request A should backtrack onto gpu1, got %v", keys)
	}

	// Both devices are now occupied: another identical claim cannot fit.
	other := &ResourceClaim{
		Namespace: "default", Name: "greedy-2",
		Requests: []DeviceRequest{
			{Name: "x", ClassName: "gpu", AllocationMode: AllocationModeExactCount, Count: 1},
		},
	}
	if ok, _ := sched.Filter(testNode, other); ok {
		t.Fatal("occupied devices must not be re-allocated")
	}
}

// TestMatchAttributeSameFabric requires two requests to land in one NVLink
// fabric domain, including a positive and a negative scenario.
func TestMatchAttributeSameFabric(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"fabric": Int64Value(1)}, 40),
		gpuDevice("gpu1", map[string]AttributeValue{"fabric": Int64Value(1)}, 40),
		gpuDevice("gpu2", map[string]AttributeValue{"fabric": Int64Value(2)}, 40),
	))

	claim := &ResourceClaim{
		Namespace: "default", Name: "fabric",
		Requests: []DeviceRequest{
			{Name: "primary", ClassName: "gpu", AllocationMode: AllocationModeExactCount, Count: 1},
			{Name: "secondary", ClassName: "gpu", AllocationMode: AllocationModeExactCount, Count: 1},
		},
		Constraints: []ClaimConstraint{{
			MatchAttribute: &MatchAttributeConstraint{
				Attribute: "fabric",
				Requests:  []string{"primary", "secondary"},
			},
		}},
	}

	if err := sched.Reserve(testNode, claim, podConsumer("p1", "pod1")); err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	stored, _ := sched.GetClaim("default", "fabric")
	keys := allocationKeys(stored.Allocation)
	fabricOf := map[string]int64{
		"nv/pool/gpu0": 1, "nv/pool/gpu1": 1, "nv/pool/gpu2": 2,
	}
	if fabricOf[keys["primary"][0]] != fabricOf[keys["secondary"][0]] {
		t.Fatalf("MatchAttribute violated: %v", keys)
	}

	// Negative: the first claim consumes both fabric-1 devices, leaving a
	// single fabric-2 device, so a second same-domain pair cannot fit.
	second := &ResourceClaim{
		Namespace: "default", Name: "fabric-2",
		Requests: []DeviceRequest{
			{Name: "primary", ClassName: "gpu", AllocationMode: AllocationModeExactCount, Count: 1},
			{Name: "secondary", ClassName: "gpu", AllocationMode: AllocationModeExactCount, Count: 1},
		},
		Constraints: []ClaimConstraint{{
			MatchAttribute: &MatchAttributeConstraint{
				Attribute: "fabric", Requests: []string{"primary", "secondary"},
			},
		}},
	}
	if ok, _ := sched.Filter(testNode, second); ok {
		t.Fatal("no same-fabric pair remains, Filter must fail")
	}
}

// TestMatchAttributeMissingAttribute fails when a device lacks the attribute.
func TestMatchAttributeMissingAttribute(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"numa": Int64Value(0)}, 40),
		gpuDevice("gpu1", map[string]AttributeValue{}, 40),
	))
	claim := &ResourceClaim{
		Namespace: "default", Name: "numa",
		Requests: []DeviceRequest{
			{Name: "a", ClassName: "gpu", AllocationMode: AllocationModeExactCount, Count: 1},
			{Name: "b", ClassName: "gpu", AllocationMode: AllocationModeExactCount, Count: 1},
		},
		Constraints: []ClaimConstraint{{
			MatchAttribute: &MatchAttributeConstraint{
				Attribute: "numa", Requests: []string{"a", "b"},
			},
		}},
	}
	if ok, err := sched.Filter(testNode, claim); err != nil || ok {
		t.Fatalf("expected no-fit without attribute, got ok=%v err=%v", ok, err)
	}
}

// TestIncompleteGenerationPoolMasked ensures pools missing slices for their
// newest generation are invisible to allocation, and that completing the
// generation exposes them again.
func TestIncompleteGenerationPoolMasked(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})

	sched.ApplySlice(ResourceSlice{
		Name: "nv-pool-0", NodeName: testNode, Driver: "nv",
		Pool: ResourcePool{Name: "pool", Generation: 1, ResourceSliceCount: 2},
		Devices: []Device{
			gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100")}, 80),
		},
	})

	if err := sched.EnsurePoolsComplete(testNode); !errors.Is(err, ErrIncompletePool) {
		t.Fatalf("expected ErrIncompletePool, got %v", err)
	}

	claim := &ResourceClaim{
		Namespace: "default", Name: "incomplete",
		Requests: []DeviceRequest{
			{Name: "a", ClassName: "gpu", AllocationMode: AllocationModeExactCount, Count: 1},
		},
	}
	if ok, _ := sched.Filter(testNode, claim); ok {
		t.Fatal("incomplete pool must be masked from Filter")
	}
	if err := sched.Reserve(testNode, claim, podConsumer("p1", "pod1")); !errors.Is(err, ErrNoDevices) {
		t.Fatalf("expected ErrNoDevices while pool incomplete, got %v", err)
	}

	// Deliver the missing second slice: the pool becomes complete.
	sched.ApplySlice(ResourceSlice{
		Name: "nv-pool-1", NodeName: testNode, Driver: "nv",
		Pool: ResourcePool{Name: "pool", Generation: 1, ResourceSliceCount: 2},
		Devices: []Device{
			gpuDevice("gpu1", map[string]AttributeValue{"model": StringValue("a100")}, 80),
		},
	})
	if err := sched.EnsurePoolsComplete(testNode); err != nil {
		t.Fatalf("pool should be complete: %v", err)
	}
	if err := sched.Reserve(testNode, claim, podConsumer("p1", "pod1")); err != nil {
		t.Fatalf("Reserve after completion failed: %v", err)
	}

	// A newer generation resets the view and drops stale-generation devices.
	sched.ApplySlice(ResourceSlice{
		Name: "nv-pool-0", NodeName: testNode, Driver: "nv",
		Pool: ResourcePool{Name: "pool", Generation: 2, ResourceSliceCount: 1},
		Devices: []Device{
			gpuDevice("gpu9", map[string]AttributeValue{"model": StringValue("h100")}, 80),
		},
	})
	statuses := sched.PoolStatuses(testNode)
	if len(statuses) != 1 || statuses[0].Generation != 2 || !statuses[0].Complete {
		t.Fatalf("expected single complete generation-2 pool, got %+v", statuses)
	}
	if statuses[0].DeviceCount != 1 {
		t.Fatalf("stale generation devices must be dropped, got %d devices", statuses[0].DeviceCount)
	}
}

// TestAllocationModes covers All (whole matching pool) and ExactCount,
// including cross-request/cross-claim exclusion.
func TestAllocationModes(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100")}, 40),
		gpuDevice("gpu1", map[string]AttributeValue{"model": StringValue("a100")}, 40),
		gpuDevice("gpu2", map[string]AttributeValue{"model": StringValue("h100")}, 80),
	))

	allClaim := &ResourceClaim{
		Namespace: "default", Name: "all-mode",
		Requests: []DeviceRequest{{
			Name: "every-a100", ClassName: "gpu", AllocationMode: AllocationModeAll,
			Selectors: []Selector{{
				AttributeRequirements: []AttributeRequirement{
					{Attribute: "model", Op: OpEqual, Value: StringValue("a100")},
				},
			}},
		}},
	}
	if err := sched.Reserve(testNode, allClaim, podConsumer("p1", "pod1")); err != nil {
		t.Fatalf("All-mode Reserve failed: %v", err)
	}
	stored, _ := sched.GetClaim("default", "all-mode")
	got := allocationKeys(stored.Allocation)["every-a100"]
	if len(got) != 2 {
		t.Fatalf("All mode must take both a100 devices, got %v", got)
	}

	// ExactCount can only use the leftover h100.
	exactClaim := &ResourceClaim{
		Namespace: "default", Name: "exact",
		Requests: []DeviceRequest{{
			Name: "one", ClassName: "gpu",
			AllocationMode: AllocationModeExactCount, Count: 1,
		}},
	}
	if err := sched.Reserve(testNode, exactClaim, podConsumer("p2", "pod2")); err != nil {
		t.Fatalf("ExactCount Reserve failed: %v", err)
	}
	stored, _ = sched.GetClaim("default", "exact")
	if allocationKeys(stored.Allocation)["one"][0] != "nv/pool/gpu2" {
		t.Fatalf("only the free h100 should be selectable, got %v", allocationKeys(stored.Allocation))
	}

	tooMany := &ResourceClaim{
		Namespace: "default", Name: "too-many",
		Requests: []DeviceRequest{{
			Name: "two", ClassName: "gpu",
			AllocationMode: AllocationModeExactCount, Count: 2,
		}},
	}
	if ok, _ := sched.Filter(testNode, tooMany); ok {
		t.Fatal("ExactCount exceeding available devices must fail")
	}
}
