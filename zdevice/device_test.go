package zdevice

import (
	"errors"
	"testing"

	"prescreen.local/ps-7e522fc03b/zplanner"
)

func newFormattedDevice(t *testing.T) *Device {
	t.Helper()
	dir := t.TempDir()
	dev := NewDevice(dir)
	if err := dev.Format(); err != nil {
		t.Fatalf("Format: %v", err)
	}
	return dev
}

func tableWith(id zplanner.CID, zone zplanner.ZoneID, off uint8, gen, lastOp int) *zplanner.Table {
	return &zplanner.Table{
		Generation: gen,
		LastOp:     lastOp,
		Loc:        map[zplanner.CID]zplanner.Phys{id: {Zone: zone, Off: off}},
		Roots:      map[string]zplanner.CID{"a": id},
		Snaps:      map[string]map[string]zplanner.CID{},
	}
}

func TestPreviousSlotPinsZoneUntilAck(t *testing.T) {
	dev := newFormattedDevice(t)

	phys, err := dev.Append(0, []byte("CID1"))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	t1 := tableWith(1, phys.Zone, phys.Off, 1, 0)
	if err := dev.WriteSlot(t1); err != nil {
		t.Fatalf("WriteSlot: %v", err)
	}
	if err := dev.Swap(); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	if err := dev.Ack(); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// Publish a second table that still references a page in zone 0, while
	// the retained previous slot also references zone 0.
	t2 := tableWith(1, phys.Zone, phys.Off, 2, 1)
	if err := dev.WriteSlot(t2); err != nil {
		t.Fatalf("WriteSlot: %v", err)
	}
	if err := dev.Swap(); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	pending, _ := dev.Pending()
	if !pending {
		t.Fatal("expected unacknowledged publish to retain both slots")
	}
	if err := dev.ResetZone(0); !errors.Is(err, ErrPinned) {
		t.Fatalf("expected ErrPinned before Ack, got %v", err)
	}

	if err := dev.Ack(); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	// Zone 0 is still referenced by the sole surviving slot, so it remains
	// pinned even after Ack.
	if err := dev.ResetZone(0); !errors.Is(err, ErrPinned) {
		t.Fatalf("expected live slot to keep zone pinned, got %v", err)
	}
}

func TestPinnedByPreviousSlotAfterCurrentRootMigrates(t *testing.T) {
	dev := newFormattedDevice(t)

	old, err := dev.Append(0, []byte("CID1"))
	if err != nil {
		t.Fatal(err)
	}
	t1 := tableWith(1, old.Zone, old.Off, 1, 0)
	mustPublish(t, dev, t1)

	// Move the live content physically to zone 1 and publish. The current
	// root no longer touches zone 0, but the retained previous slot still does.
	newPhys, err := dev.Append(1, []byte("CID1"))
	if err != nil {
		t.Fatal(err)
	}
	t2 := tableWith(1, newPhys.Zone, newPhys.Off, 2, 1)
	if err := dev.WriteSlot(t2); err != nil {
		t.Fatal(err)
	}
	if err := dev.Swap(); err != nil {
		t.Fatal(err)
	}
	if err := dev.ResetZone(0); !errors.Is(err, ErrPinned) {
		t.Fatalf("previous slot must keep zone 0 pinned, got %v", err)
	}
	if err := dev.Ack(); err != nil {
		t.Fatal(err)
	}
	// After Ack zone 0 is only garbage and must reset cleanly.
	if err := dev.ResetZone(0); err != nil {
		t.Fatalf("zone 0 should be resettable after Ack: %v", err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestSecondPublishRejectedBeforeAck(t *testing.T) {
	dev := newFormattedDevice(t)
	phys, _ := dev.Append(0, []byte("x"))
	t1 := tableWith(1, phys.Zone, phys.Off, 1, 0)
	if err := dev.WriteSlot(t1); err != nil {
		t.Fatal(err)
	}
	if err := dev.Swap(); err != nil {
		t.Fatal(err)
	}
	if err := dev.WriteSlot(t1); !errors.Is(err, ErrPublishPending) {
		t.Fatalf("expected ErrPublishPending, got %v", err)
	}
}

func TestVolatileBoundaryDiscardsUnflushedAppend(t *testing.T) {
	dev := newFormattedDevice(t)
	if _, err := dev.Append(2, []byte("lost")); err != nil {
		t.Fatal(err)
	}
	dev.PowerOff()

	tbl, _, err := dev.PowerOn(0)
	if err != nil {
		t.Fatalf("PowerOn: %v", err)
	}
	fill, err := dev.Fill()
	if err != nil {
		t.Fatal(err)
	}
	if fill[2] != 0 {
		t.Fatalf("unflushed append survived power loss: fill=%d", fill[2])
	}
	if tbl == nil {
		t.Fatal("expected boot table even when empty")
	}
}

func TestBootFromBothSlotsAfterUnacknowledgedSwap(t *testing.T) {
	dev := newFormattedDevice(t)
	phys, _ := dev.Append(0, []byte("x"))
	t1 := tableWith(1, phys.Zone, phys.Off, 1, 0)
	mustPublish(t, dev, t1)

	phys2, _ := dev.Append(1, []byte("y"))
	t2 := tableWith(2, phys2.Zone, phys2.Off, 2, 1)
	t2.Loc[1] = phys
	if err := dev.WriteSlot(t2); err != nil {
		t.Fatal(err)
	}
	if err := dev.Swap(); err != nil {
		t.Fatal(err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatal(err)
	}
	dev.PowerOff()

	for _, boot := range []int{0, 1} {
		tbl, choice, err := dev.PowerOn(boot)
		if err != nil {
			t.Fatalf("boot %d: %v", boot, err)
		}
		if choice != boot {
			t.Fatalf("boot %d returned slot %d", boot, choice)
		}
		// Slots alternate: t2 landed in physical slot 0, retained t1 in slot 1.
		wantGen := t2.Generation
		if boot == 1 {
			wantGen = t1.Generation
		}
		if tbl.Generation != wantGen {
			t.Fatalf("boot %d returned gen %d want %d", boot, tbl.Generation, wantGen)
		}
		dev.PowerOff()
	}
}

func TestTornSlotDetected(t *testing.T) {
	dev := newFormattedDevice(t)
	phys, _ := dev.Append(0, []byte("x"))
	t1 := tableWith(1, phys.Zone, phys.Off, 1, 0)
	mustPublish(t, dev, t1)
	phys2, _ := dev.Append(1, []byte("y"))
	t2 := tableWith(2, phys2.Zone, phys2.Off, 2, 1)
	if err := dev.WriteSlot(t2); err != nil {
		t.Fatal(err)
	}
	if err := dev.Swap(); err != nil {
		t.Fatal(err)
	}
	inactive, _ := dev.CurrentSlot()
	if err := dev.CorruptSlot(1 - inactive); err != nil {
		t.Fatal(err)
	}
	if err := dev.Flush(); err != nil {
		t.Fatal(err)
	}
	dev.PowerOff()
	if _, _, err := dev.PowerOn(1 - inactive); !errors.Is(err, ErrTornSlot) {
		t.Fatalf("expected ErrTornSlot, got %v", err)
	}
}

func mustPublish(t *testing.T, dev *Device, tbl *zplanner.Table) {
	t.Helper()
	if err := dev.WriteSlot(tbl); err != nil {
		t.Fatal(err)
	}
	if err := dev.Swap(); err != nil {
		t.Fatal(err)
	}
	if err := dev.Ack(); err != nil {
		t.Fatal(err)
	}
}
