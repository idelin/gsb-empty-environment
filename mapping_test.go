package netsec

import (
	"errors"
	"testing"
)

func TestMappingGuardRails(t *testing.T) {
	r := newRig(t)
	r.assignFrames(t, tenantA, 0, 1)

	// Mapping against an unassigned physical frame is refused.
	if err := r.d1.Map(iova(0), phys(99), PermRead, tenantA); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("map unassigned frame: want ErrOwnerMismatch, got %v", err)
	}

	r.mapPage(t, r.d1, 0, 0, PermRead, tenantA)

	// Duplicate IOVA mapping is a conflict.
	if err := r.d1.Map(iova(0), phys(1), PermRead, tenantA); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate iova map: want ErrConflict, got %v", err)
	}
	// Empty permission set and unaligned addresses are bad requests.
	if err := r.d1.Map(iova(1), phys(1), 0, tenantA); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("zero perm map: want ErrBadRequest, got %v", err)
	}
	if err := r.d1.Map(IOVA(3), phys(1), PermRead, tenantA); !errors.Is(err, ErrUnaligned) {
		t.Fatalf("unaligned iova map: want ErrUnaligned, got %v", err)
	}
	if err := r.d1.Map(iova(2), PhysAddr(3), PermRead, tenantA); !errors.Is(err, ErrUnaligned) {
		t.Fatalf("unaligned phys map: want ErrUnaligned, got %v", err)
	}

	// Remapping a missing IOVA is an unmapped error; remapping for another
	// tenant is an ownership error even when the target frame is assigned.
	if err := r.d1.Remap(iova(7), phys(1), PermRead, tenantA); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("remap missing iova: want ErrUnmapped, got %v", err)
	}
	r.assignFrames(t, tenantB, 2)
	if err := r.d1.Remap(iova(0), phys(2), PermRead, tenantB); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("cross-tenant remap: want ErrOwnerMismatch, got %v", err)
	}

	// Unmapping a missing frame and double device registration are rejected.
	if err := r.d1.Unmap(iova(8)); !errors.Is(err, ErrUnmapped) {
		t.Fatalf("unmap missing: want ErrUnmapped, got %v", err)
	}
	if _, err := r.io.NewDomain(dev1); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate domain: want ErrConflict, got %v", err)
	}

	// A successful remap changes the generation and is observable.
	g := r.d1.Generation()
	if err := r.d1.Remap(iova(0), phys(1), PermWrite, tenantA); err != nil {
		t.Fatalf("legitimate remap: %v", err)
	}
	if r.d1.Generation() != g+1 {
		t.Fatalf("generation after remap = %d, want %d", r.d1.Generation(), g+1)
	}

	// Nil callback is a bad request.
	if _, err := r.io.Transfer(dev1, []Segment{{IOVA: iova(0), Len: 1}}, false, nil); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("nil callback: want ErrBadRequest, got %v", err)
	}

	// Callback failure aborts and wraps ErrCallback while releasing the lease.
	cbErr := errors.New("boom")
	_, err := r.io.Transfer(dev1, []Segment{{IOVA: iova(0), Len: 1}}, true, func(MemOp) error {
		return cbErr
	})
	if !errors.Is(err, ErrCallback) || !errors.Is(err, cbErr) {
		t.Fatalf("callback error wrap: got %v", err)
	}
	if r.d1.ActiveLeases() != 0 {
		t.Fatalf("lease leaked after callback failure")
	}
}
