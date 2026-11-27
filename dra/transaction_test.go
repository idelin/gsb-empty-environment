package dra

import (
	"errors"
	"testing"
)

func oneDeviceClaim(namespace, name string, shared bool) *ResourceClaim {
	return &ResourceClaim{
		Namespace: namespace,
		Name:      name,
		Shared:    shared,
		Requests: []DeviceRequest{{
			Name:           "gpu",
			ClassName:      "gpu",
			AllocationMode: AllocationModeExactCount,
			Count:          1,
		}},
	}
}

// TestSharedClaimReferenceCounting verifies that multiple consumers share one
// allocation without deducting hardware twice, and that the devices stay
// occupied until the last consumer unreserves.
func TestSharedClaimReferenceCounting(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100")}, 80),
	))

	claim := oneDeviceClaim("default", "shared", true)
	sched.AddClaim(*claim)

	pod1 := podConsumer("u1", "pod1")
	pod2 := podConsumer("u2", "pod2")
	if err := sched.Reserve(testNode, claim, pod1); err != nil {
		t.Fatalf("first Reserve failed: %v", err)
	}
	if err := sched.Reserve(testNode, claim, pod2); err != nil {
		t.Fatalf("second Reserve on shared claim failed: %v", err)
	}

	stored, _ := sched.GetClaim("default", "shared")
	if len(stored.ReservedFor) != 2 {
		t.Fatalf("expected two ReservedFor entries, got %d", len(stored.ReservedFor))
	}

	// Re-reserving the same consumer is idempotent (no duplicate entry).
	if err := sched.Reserve(testNode, claim, pod1); err != nil {
		t.Fatalf("idempotent Reserve failed: %v", err)
	}
	stored, _ = sched.GetClaim("default", "shared")
	if len(stored.ReservedFor) != 2 {
		t.Fatalf("duplicate consumer must not be recorded, got %d", len(stored.ReservedFor))
	}

	// While shared, the single device remains unavailable to other claims.
	competitor := oneDeviceClaim("default", "competitor", false)
	if ok, _ := sched.Filter(testNode, competitor); ok {
		t.Fatal("shared claim still holds the device; competitor must not fit")
	}

	// First consumer leaves: allocation is retained.
	if err := sched.Unreserve(claim, pod1); err != nil {
		t.Fatalf("Unreserve pod1 failed: %v", err)
	}
	stored, _ = sched.GetClaim("default", "shared")
	if stored.Allocation == nil {
		t.Fatal("allocation must survive while one shared consumer remains")
	}
	if len(stored.ReservedFor) != 1 || stored.ReservedFor[0].Key() != pod2.Key() {
		t.Fatalf("only pod2 should remain, got %+v", stored.ReservedFor)
	}
	if ok, _ := sched.Filter(testNode, competitor); ok {
		t.Fatal("device stays occupied until the last consumer leaves")
	}

	// Last consumer leaves: hardware is released and becomes allocatable.
	if err := sched.Unreserve(claim, pod2); err != nil {
		t.Fatalf("Unreserve pod2 failed: %v", err)
	}
	stored, _ = sched.GetClaim("default", "shared")
	if stored.Allocation != nil {
		t.Fatal("allocation must be released when reference count hits zero")
	}
	if err := sched.Reserve(testNode, competitor, podConsumer("u3", "pod3")); err != nil {
		t.Fatalf("released device should be allocatable again: %v", err)
	}
	stored, _ = sched.GetClaim("default", "competitor")
	if allocationKeys(stored.Allocation)["gpu"][0] != "nv/pool/gpu0" {
		t.Fatalf("expected recycled gpu0, got %v", allocationKeys(stored.Allocation))
	}
}

// TestExclusiveUnreserveRollback verifies that an exclusive claim (e.g. one
// produced from a ResourceClaimTemplate) releases all devices on a single
// Unreserve, exactly as a failed pod binding requires.
func TestExclusiveUnreserveRollback(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100")}, 80),
		gpuDevice("gpu1", map[string]AttributeValue{"model": StringValue("a100")}, 80),
	))

	claim := &ResourceClaim{
		Namespace: "default", Name: "exclusive", Shared: false,
		Requests: []DeviceRequest{{
			Name: "pair", ClassName: "gpu",
			AllocationMode: AllocationModeExactCount, Count: 2,
		}},
	}
	consumer := podConsumer("u1", "pod1")
	if err := sched.Reserve(testNode, claim, consumer); err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	// A second consumer cannot take the exclusive claim.
	if err := sched.Reserve(testNode, claim, podConsumer("u2", "pod2")); !errors.Is(err, ErrAlreadyReserved) {
		t.Fatalf("expected ErrAlreadyReserved, got %v", err)
	}

	// All devices are occupied before unreserve.
	replacement := oneDeviceClaim("default", "replacement", false)
	if ok, _ := sched.Filter(testNode, replacement); ok {
		t.Fatal("exclusive claim holds both devices")
	}

	if err := sched.Unreserve(claim, consumer); err != nil {
		t.Fatalf("Unreserve failed: %v", err)
	}
	stored, _ := sched.GetClaim("default", "exclusive")
	if stored.Allocation != nil || len(stored.ReservedFor) != 0 {
		t.Fatalf("Unreserve must drop allocation and consumers, got %+v", stored)
	}

	// Both devices are returned to the free pool at once.
	reclaim := &ResourceClaim{
		Namespace: "default", Name: "reclaim",
		Requests: []DeviceRequest{{
			Name: "pair", ClassName: "gpu",
			AllocationMode: AllocationModeExactCount, Count: 2,
		}},
	}
	if err := sched.Reserve(testNode, reclaim, podConsumer("u3", "pod3")); err != nil {
		t.Fatalf("devices should be fully recycled after Unreserve: %v", err)
	}

	// Unreserving again or an unknown consumer is an error.
	if err := sched.Unreserve(claim, consumer); !errors.Is(err, ErrNotReserved) {
		t.Fatalf("expected ErrNotReserved, got %v", err)
	}
}

// TestDeleteClaimReleasesHardware covers explicit claim deletion.
func TestDeleteClaimReleasesHardware(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100")}, 80),
	))

	claim := oneDeviceClaim("default", "doomed", true)
	if err := sched.Reserve(testNode, claim, podConsumer("u1", "pod1")); err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	if err := sched.DeleteClaim("default", "doomed"); err != nil {
		t.Fatalf("DeleteClaim failed: %v", err)
	}
	if _, ok := sched.GetClaim("default", "doomed"); ok {
		t.Fatal("claim should be gone after delete")
	}
	next := oneDeviceClaim("default", "next", false)
	if err := sched.Reserve(testNode, next, podConsumer("u2", "pod2")); err != nil {
		t.Fatalf("hardware should be free after delete: %v", err)
	}
	if err := sched.DeleteClaim("default", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestReserveNodeMismatch ensures an allocated claim cannot be reserved on
// another node.
func TestReserveNodeMismatch(t *testing.T) {
	sched := NewScheduler()
	sched.AddClass(DeviceClass{Name: "gpu"})
	sched.ApplySlice(fullSlice("nv", "pool",
		gpuDevice("gpu0", map[string]AttributeValue{"model": StringValue("a100")}, 80),
	))
	claim := oneDeviceClaim("default", "pinned", true)
	if err := sched.Reserve(testNode, claim, podConsumer("u1", "pod1")); err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	if err := sched.Reserve("node-b", claim, podConsumer("u2", "pod2")); !errors.Is(err, ErrNodeMismatch) {
		t.Fatalf("expected ErrNodeMismatch, got %v", err)
	}
}
