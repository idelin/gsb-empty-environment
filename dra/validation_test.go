package dra

import (
	"errors"
	"sync"
	"testing"
)

func exactReq(name, class string, count int64) DeviceRequest {
	return DeviceRequest{
		Name: name, ClassName: class,
		AllocationMode: AllocationModeExactCount, Count: count,
	}
}

func TestInvalidSpecs(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100")}, 80),
	))

	cases := []struct {
		name  string
		claim *ResourceClaim
		want  error
	}{
		{
			"no requests",
			&ResourceClaim{Namespace: "default", Name: "empty"},
			ErrInvalidSpec,
		},
		{
			"duplicate request names",
			&ResourceClaim{
				Namespace: "default", Name: "dup",
				Requests: []DeviceRequest{exactReq("r", "gpu", 1), exactReq("r", "gpu", 1)},
			},
			ErrInvalidSpec,
		},
		{
			"zero exact count",
			&ResourceClaim{
				Namespace: "default", Name: "zero",
				Requests: []DeviceRequest{{Name: "r", ClassName: "gpu", AllocationMode: AllocationModeExactCount}},
			},
			ErrInvalidSpec,
		},
		{
			"unknown allocation mode",
			&ResourceClaim{
				Namespace: "default", Name: "mode",
				Requests: []DeviceRequest{{Name: "r", ClassName: "gpu", AllocationMode: "Weird", Count: 1}},
			},
			ErrInvalidSpec,
		},
		{
			"unknown class",
			&ResourceClaim{
				Namespace: "default", Name: "class",
				Requests: []DeviceRequest{exactReq("r", "tpu", 1)},
			},
			ErrNotFound,
		},
		{
			"match attribute unknown request",
			&ResourceClaim{
				Namespace: "default", Name: "constraint",
				Requests: []DeviceRequest{exactReq("r", "gpu", 1)},
				Constraints: []ClaimConstraint{{
					MatchAttribute: &MatchAttributeConstraint{Attribute: "x", Requests: []string{"ghost"}},
				}},
			},
			ErrInvalidSpec,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := sched.Reserve(testNode, tc.claim, podConsumer("u", "pod"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestAllModePicksOnePool(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool-a",
		gpuDevice("a0", map[string]AttributeValue{"model": StringValue("a100")}, 40),
	))
	sched.ApplySlice(fullSlice("nv", "pool-b",
		gpuDevice("b0", map[string]AttributeValue{"model": StringValue("a100")}, 40),
		gpuDevice("b1", map[string]AttributeValue{"model": StringValue("a100")}, 40),
	))

	claim := &ResourceClaim{
		Namespace: "default", Name: "all-pool",
		Requests: []DeviceRequest{{
			Name: "all", ClassName: "gpu", AllocationMode: AllocationModeAll,
		}},
	}
	if err := sched.Reserve(testNode, claim, podConsumer("u1", "pod1")); err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	stored, _ := sched.GetClaim("default", "all-pool")
	devices := allocationKeys(stored.Allocation)["all"]
	poolSeen := map[string]struct{}{}
	for _, key := range devices {
		poolSeen[key] = struct{}{}
	}
	if len(devices) != 1 {
		t.Fatalf("All mode must select devices from a single pool, got %v", devices)
	}
	if devices[0] != "nv/pool-a/a0" {
		t.Fatalf("smallest-named matching pool should be tried first, got %v", devices)
	}
}

func TestRemoveSliceReleasesDevices(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	slice := fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100")}, 80),
	)
	sched.ApplySlice(slice)

	claim := oneDeviceClaim("default", "temp", false)
	if err := sched.Reserve(testNode, claim, podConsumer("u1", "pod1")); err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	if err := sched.Unreserve(claim, podConsumer("u1", "pod1")); err != nil {
		t.Fatalf("Unreserve failed: %v", err)
	}

	sched.RemoveSlice(slice)
	statuses := sched.PoolStatuses(testNode)
	if len(statuses) != 1 || statuses[0].DeviceCount != 0 || statuses[0].Complete {
		t.Fatalf("removed slice should leave an empty incomplete pool, got %+v", statuses)
	}
	if ok, _ := sched.Filter(testNode, oneDeviceClaim("default", "after", false)); ok {
		t.Fatal("no devices remain after slice removal")
	}

	// Removing an unknown slice is a safe no-op.
	sched.RemoveSlice(fullSlice("nv", "ghost"))
}

func TestUnknownNodeAndGetters(t *testing.T) {
	sched := NewScheduler()
	if statuses := sched.PoolStatuses("missing"); statuses != nil {
		t.Fatalf("nil statuses expected, got %v", statuses)
	}
	if err := sched.EnsurePoolsComplete("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	claim := oneDeviceClaim("default", "nowhere", false)
	if err := sched.Reserve("missing-node", claim, podConsumer("u", "p")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	val := Int64Value(7)
	if val.Kind() != KindInt64 || val.Int64() != 7 {
		t.Fatal("Int64Value getter mismatch")
	}
	if BoolValue(true).Bool() != true || StringValue("x").String() != "x" {
		t.Fatal("attribute getters mismatch")
	}
}

func TestConcurrentReservations(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	devices := make([]Device, 0, 32)
	for i := 0; i < 32; i++ {
		devices = append(devices, gpuDevice(
			"gpu"+itoa(i),
			map[string]AttributeValue{"model": StringValue("a100")},
			40,
		))
	}
	sched.ApplySlice(fullSlice("nv", "pool", devices...))

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claim := oneDeviceClaim("default", "claim-"+itoa(i), false)
			_ = sched.Reserve(testNode, claim, podConsumer("u-"+itoa(i), "pod-"+itoa(i)))
		}(i)
	}
	wg.Wait()

	// Every one of the 32 distinct devices should be occupied exactly once.
	seen := map[string]struct{}{}
	for i := 0; i < 32; i++ {
		stored, ok := sched.GetClaim("default", "claim-"+itoa(i))
		if !ok || stored.Allocation == nil {
			t.Fatalf("claim %d not allocated", i)
		}
		key := stored.Allocation.Results[0].Devices[0].Key()
		if _, dup := seen[key]; dup {
			t.Fatalf("device %s allocated twice", key)
		}
		seen[key] = struct{}{}
	}
	if len(seen) != 32 {
		t.Fatalf("expected 32 unique allocations, got %d", len(seen))
	}

	// No free device remains.
	extra := oneDeviceClaim("default", "extra", false)
	if ok, _ := sched.Filter(testNode, extra); ok {
		t.Fatal("all devices must be occupied")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
