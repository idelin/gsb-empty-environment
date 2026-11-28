package device

import (
	"errors"
	"testing"
)

// TestZoneCapacityBoundaryAndMisalignedWrites covers acceptance item 1: the
// writable region ends at SLBA+ZoneCapacity (< ZoneSize here), regular writes
// must start exactly at the write pointer, and ZoneAppend allocates atomically.
func TestZoneCapacityBoundaryAndMisalignedWrites(t *testing.T) {
	dev, err := New(Config{
		NumZones:     2,
		ZoneSize:     8,
		ZoneCapacity: 5, // capacity strictly below size
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	z0, _ := dev.Zone(0)
	if z0.SLBA != 0 || z0.ZoneSize != 8 || z0.ZoneCapacity != 5 || z0.WritePointer != 0 {
		t.Fatalf("unexpected descriptor: %+v", z0)
	}
	if z0.State != ZoneEmpty {
		t.Fatalf("new zone state = %s, want EMPTY", z0.State)
	}

	// A write starting above the empty-zone WP is rejected before activation.
	if err := dev.Write(1, 1, nil); !errors.Is(err, ErrInvalidZoneWrite) {
		t.Fatalf("misaligned write err = %v, want InvalidZoneWrite", err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneEmpty {
		t.Fatalf("rejected write mutated state to %s", info.State)
	}

	// First legal write implicitly opens the zone and advances WP.
	if err := dev.Write(0, 1, []byte("a")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneImplicitlyOpen || info.WritePointer != 1 {
		t.Fatalf("after first write: %+v", info)
	}

	// Any non-WP LBA is invalid, including backwards and skipped LBAs.
	if err := dev.Write(0, 1, nil); !errors.Is(err, ErrInvalidZoneWrite) {
		t.Fatalf("backwards write err = %v", err)
	}
	if err := dev.Write(3, 1, nil); !errors.Is(err, ErrInvalidZoneWrite) {
		t.Fatalf("gap write err = %v", err)
	}

	// Writes are sequential up to the capacity boundary.
	if err := dev.Write(1, 3, nil); err != nil {
		t.Fatalf("sequential multi-block write: %v", err)
	}
	if info, _ := dev.Zone(0); info.WritePointer != 4 {
		t.Fatalf("wp = %d, want 4", info.WritePointer)
	}

	// One block remains in capacity; a two-block write would cross SLBA+cap
	// (LBA 5) even though ZoneSize still covers LBA 5..7.
	if err := dev.Write(4, 2, nil); !errors.Is(err, ErrInvalidZoneWrite) {
		t.Fatalf("capacity crossing write err = %v, want InvalidZoneWrite", err)
	}
	if err := dev.Write(4, 1, nil); err != nil {
		t.Fatalf("final capacity write: %v", err)
	}
	info, _ := dev.Zone(0)
	if info.State != ZoneFull || info.WritePointer != info.SLBA+info.ZoneCapacity {
		t.Fatalf("zone should be FULL at capacity: %+v", info)
	}

	// FULL zones reject further writes.
	if err := dev.Write(5, 1, nil); !errors.Is(err, ErrInvalidZoneWrite) {
		t.Fatalf("write to FULL err = %v", err)
	}

	// ZoneAppend atomically allocates the next LBA in another zone.
	assigned, err := dev.ZoneAppend(1, 2, []byte("z"))
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if assigned != 8 {
		t.Fatalf("assigned lba = %d, want 8", assigned)
	}
	if got, _ := dev.Read(8); string(got) != "z" {
		t.Fatalf("read appended payload = %q", got)
	}
	if info, _ := dev.Zone(1); info.WritePointer != 10 || info.State != ZoneImplicitlyOpen {
		t.Fatalf("append descriptor: %+v", info)
	}

	// Append rejects requests crossing the capacity boundary too.
	if _, err := dev.ZoneAppend(1, 4, nil); !errors.Is(err, ErrInvalidZoneWrite) {
		t.Fatalf("append over capacity err = %v", err)
	}
}
