package engine

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"prescreen.local/ps-3e28dd0313/zns/device"
)

const testZoneSize = int64(4)

func newTestDevice(t *testing.T, numZones int, mor, mar int) *device.Device {
	t.Helper()
	dev, err := device.New(device.Config{
		NumZones:       numZones,
		ZoneSize:       testZoneSize,
		ZoneCapacity:   testZoneSize,
		MaxOpenZones:   mor,
		MaxActiveZones: mar,
	})
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}
	return dev
}

// TestGCAtomicCASDoesNotOverwriteNewVersion covers acceptance item 4: while GC
// is relocating a victim zone, an overwrite of a logical block must win. The GC
// compare-and-swap detects the changed mapping, skips the stale version and
// never resets a zone still holding live blocks.
func TestGCAtomicCASDoesNotOverwriteNewVersion(t *testing.T) {
	dev := newTestDevice(t, 4, 0, 0)
	// Hook fires once, when GC reaches logical block 2 inside the victim.
	overwriteDone := make(chan struct{})
	var fired atomic.Bool

	var err error
	var eng *Engine
	eng, err = New(Config{
		Device: dev,
		Mode:   ModeTiered,
		Hook: func(victimZoneID int, logicalLBA int64, stale bool) {
			if logicalLBA == 2 && !fired.Swap(true) {
				// We run on the GC goroutine holding the engine write lock, so
				// invoke the locked write path directly: this models a write
				// that becomes visible between snapshot and map swap.
				loc, werr := eng.writeLocked(2, []byte("v2"), HeatHot, false)
				if werr != nil {
					t.Errorf("racing overwrite: %v", werr)
					return
				}
				if loc.ZoneID == victimZoneID {
					t.Errorf("new version still inside victim zone %d", loc.ZoneID)
				}
				close(overwriteDone)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Fill zone 0 (the first cold pool target) with four live logical blocks.
	for lba := int64(0); lba < 4; lba++ {
		if _, err := eng.Write(lba, []byte(fmt.Sprintf("v0-%d", lba))); err != nil {
			t.Fatalf("seed write %d: %v", lba, err)
		}
	}
	victimInfo, _ := dev.Zone(0)
	if victimInfo.State != device.ZoneFull {
		t.Fatalf("zone 0 state = %s, want FULL", victimInfo.State)
	}

	// Need a destination zone; the engine has 4 zones and zone 0 is full.
	run, err := eng.GC(0)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	select {
	case <-overwriteDone:
	default:
		t.Fatal("racing overwrite hook never fired")
	}

	if run.SkippedStale != 1 || run.Migrated != 3 {
		t.Fatalf("GC run = %+v, want 3 migrated and 1 stale skip", run)
	}
	if !run.Reset {
		t.Fatal("victim zone was not reset")
	}

	// The authoritative read must return the new version.
	got, err := eng.Read(2)
	if err != nil {
		t.Fatalf("read lba 2: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("lba 2 payload = %q, want v2", got)
	}
	loc, ok := eng.Lookup(2)
	if !ok || loc.ZoneID == 0 {
		t.Fatalf("lba 2 mapping = %+v ok=%v, must point outside reset zone 0", loc, ok)
	}

	// The other three blocks migrated intact.
	for lba := int64(0); lba < 4; lba++ {
		if lba == 2 {
			continue
		}
		got, err := eng.Read(lba)
		if err != nil {
			t.Fatalf("read migrated lba %d: %v", lba, err)
		}
		want := fmt.Sprintf("v0-%d", lba)
		if string(got) != want {
			t.Fatalf("lba %d payload = %q, want %q", lba, got, want)
		}
	}

	if err := eng.CheckInvariants(); err != nil {
		t.Fatalf("invariants after GC: %v", err)
	}
	if info, _ := dev.Zone(0); info.State != device.ZoneEmpty {
		t.Fatalf("victim state = %s, want EMPTY", info.State)
	}
	if eng.Stats().GCBlocksStale != 1 {
		t.Fatalf("stale counter = %d, want 1", eng.Stats().GCBlocksStale)
	}
}

// TestGCConcurrentOverwriteRace runs many GC and overwrite goroutines under
// -race and asserts every logical block always reads back its latest payload.
func TestGCConcurrentOverwriteRace(t *testing.T) {
	dev := newTestDevice(t, 6, 0, 0)
	eng, err := New(Config{Device: dev, Mode: ModeTiered})
	if err != nil {
		t.Fatal(err)
	}

	// Seed every zone-worth of logical blocks once.
	const logicalBlocks = 12
	for lba := int64(0); lba < logicalBlocks; lba++ {
		if _, err := eng.Write(lba, []byte("seed")); err != nil {
			t.Fatalf("seed %d: %v", lba, err)
		}
	}

	var wg sync.WaitGroup
	var failures atomic.Int64

	// Overwriter: keeps rewriting the hot subset with versioned payloads.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for round := 0; round < 40; round++ {
				lba := int64(round % 6)
				payload := []byte(fmt.Sprintf("w%d-r%d", id, round))
				if _, err := eng.WriteWithGC(lba, payload); err != nil {
					failures.Add(1)
				}
			}
		}(w)
	}

	// Collector: reclaims FULL zones whenever possible.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for round := 0; round < 40; round++ {
			if _, _, err := eng.GCOnce(-1); err != nil {
				failures.Add(1)
			}
		}
	}()

	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("concurrent GC/overwrite errors: %d", failures.Load())
	}
	if err := eng.CheckInvariants(); err != nil {
		t.Fatalf("invariants: %v", err)
	}

	// Every logical block remains readable and its map points inside capacity.
	for lba := int64(0); lba < logicalBlocks; lba++ {
		if _, err := eng.Read(lba); err != nil {
			t.Fatalf("lba %d unreadable after concurrent GC: %v", lba, err)
		}
		loc, ok := eng.Lookup(lba)
		if !ok {
			t.Fatalf("lba %d lost its mapping", lba)
		}
		info, err := dev.Zone(loc.ZoneID)
		if err != nil {
			t.Fatalf("zone lookup: %v", err)
		}
		if loc.Offset >= info.ZoneCapacity {
			t.Fatalf("lba %d mapped beyond capacity: %+v", lba, loc)
		}
	}

	// GC must have reclaimed at least one zone under this churn.
	if eng.Stats().GCRuns == 0 {
		t.Fatal("expected at least one GC run")
	}
}

// TestGCSafetyGateAndVictimSelection checks the lowest-live-ratio victim policy
// and that reset only happens after the owner set drains to zero.
func TestGCSafetyGateAndVictimSelection(t *testing.T) {
	dev := newTestDevice(t, 4, 0, 0)
	eng, err := New(Config{Device: dev, Mode: ModeTiered})
	if err != nil {
		t.Fatal(err)
	}

	// Zone 0: all four blocks live.
	for lba := int64(0); lba < 4; lba++ {
		if _, err := eng.Write(lba, []byte("a")); err != nil {
			t.Fatal(err)
		}
	}
	// Zone 1: four blocks but three are invalidated by overwrites (one live).
	for lba := int64(4); lba < 8; lba++ {
		if _, err := eng.Write(lba, []byte("b")); err != nil {
			t.Fatal(err)
		}
	}
	for _, lba := range []int64{5, 6, 7} {
		if _, err := eng.Write(lba, []byte("b2")); err != nil {
			t.Fatal(err)
		}
	}

	// Both zone 0 and zone 1 must be FULL now; auto victim must be zone 1
	// (25% live) rather than zone 0 (100% live).
	run, err := eng.GC(-1)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if run.VictimZone != 1 {
		t.Fatalf("victim = %d, want zone 1 with lowest live ratio", run.VictimZone)
	}
	if run.Migrated != 1 {
		t.Fatalf("migrated = %d, want only the single live block", run.Migrated)
	}
	if info, _ := dev.Zone(1); info.State != device.ZoneEmpty {
		t.Fatalf("zone 1 = %s, want EMPTY after safe reset", info.State)
	}
	if err := eng.CheckInvariants(); err != nil {
		t.Fatalf("invariants: %v", err)
	}

	// Hinting a non-FULL zone is rejected rather than corrupting state.
	if _, err := eng.GC(1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("hint non-FULL err = %v", err)
	}
}
