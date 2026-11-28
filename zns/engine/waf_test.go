package engine

import (
	"fmt"
	"testing"

	"prescreen.local/ps-3e28dd0313/zns/device"
)

const (
	wafZones       = 9
	wafZoneCap     = int64(8)
	wafLogicalHot  = 8
	wafLogicalCold = 40
	wafHotRounds   = 12
	wafColdBase    = int64(100)
)

func buildWAFEngine(t *testing.T, mode GCMode) *Engine {
	t.Helper()
	dev, err := device.New(device.Config{
		NumZones:     wafZones,
		ZoneSize:     wafZoneCap,
		ZoneCapacity: wafZoneCap,
	})
	if err != nil {
		t.Fatalf("device.New: %v", err)
	}
	eng, err := New(Config{Device: dev, Mode: mode, AutoHotThreshold: 2})
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return eng
}

func runWAFWorkload(t *testing.T, eng *Engine) GCStats {
	t.Helper()
	// Interleaved first-touch: hot and cold logical blocks are written in
	// round-robin, so a mixed engine physically co-locates them in every zone.
	for i := 0; i < wafLogicalCold; i++ {
		coldLBA := wafColdBase + int64(i)
		if _, err := eng.WriteWithGC(coldLBA, []byte(fmt.Sprintf("cold-%d", i))); err != nil {
			t.Fatalf("seed cold lba %d: %v", coldLBA, err)
		}
		if i < wafLogicalHot {
			if _, err := eng.WriteWithGC(int64(i), []byte(fmt.Sprintf("hot-%d-r0", i))); err != nil {
				t.Fatalf("seed hot lba %d: %v", i, err)
			}
		}
	}

	// Only the small hot subset is overwritten afterwards. In mixed mode these
	// invalidations strand cold blocks in the same zones, forcing GC to migrate
	// them; tiered mode keeps cold blocks in dedicated, mostly-stable zones.
	for round := 1; round <= wafHotRounds; round++ {
		for i := 0; i < wafLogicalHot; i++ {
			lba := int64(i)
			if _, err := eng.WriteWithGC(lba, []byte(fmt.Sprintf("hot-%d-r%d", i, round))); err != nil {
				t.Fatalf("overwrite lba %d round %d: %v", lba, round, err)
			}
		}
	}

	// Every hot block reads back its final version; every cold block survives.
	for i := 0; i < wafLogicalHot; i++ {
		lba := int64(i)
		got, err := eng.Read(lba)
		if err != nil {
			t.Fatalf("read hot %d: %v", lba, err)
		}
		if want := fmt.Sprintf("hot-%d-r%d", i, wafHotRounds); string(got) != want {
			t.Fatalf("hot lba %d = %q, want %q", lba, got, want)
		}
	}
	for i := 0; i < wafLogicalCold; i++ {
		coldLBA := wafColdBase + int64(i)
		got, err := eng.Read(coldLBA)
		if err != nil {
			t.Fatalf("read cold %d: %v", coldLBA, err)
		}
		if want := fmt.Sprintf("cold-%d", i); string(got) != want {
			t.Fatalf("cold lba %d = %q, want %q", coldLBA, got, want)
		}
	}
	if err := eng.CheckInvariants(); err != nil {
		t.Fatalf("invariants: %v", err)
	}
	return eng.Stats()
}

// TestTieredWAFOutperformsMixed covers acceptance item 3/4: with the exact same
// overwrite-heavy workload, hot/cold tiering produces a materially lower WAF
// than co-locating hot and cold blocks in shared zones.
func TestTieredWAFOutperformsMixed(t *testing.T) {
	tiered := runWAFWorkload(t, buildWAFEngine(t, ModeTiered))
	mixed := runWAFWorkload(t, buildWAFEngine(t, ModeMixed))

	t.Logf("tiered: host=%d flash=%d gcRuns=%d migrated=%d WAF=%.4f",
		tiered.HostWrites, tiered.FlashWrites, tiered.GCRuns,
		tiered.GCMigrations, tiered.WAF())
	t.Logf("mixed:  host=%d flash=%d gcRuns=%d migrated=%d WAF=%.4f",
		mixed.HostWrites, mixed.FlashWrites, mixed.GCRuns,
		mixed.GCMigrations, mixed.WAF())

	if tiered.HostWrites != mixed.HostWrites {
		t.Fatalf("host writes diverge: tiered=%d mixed=%d",
			tiered.HostWrites, mixed.HostWrites)
	}
	if tiered.GCRuns == 0 || mixed.GCRuns == 0 {
		t.Fatal("workload must actually trigger GC in both engines")
	}
	if mixed.GCMigrations == 0 {
		t.Fatal("mixed engine must migrate live cold blocks to exhibit amplification")
	}
	if tiered.WAF() >= mixed.WAF() {
		t.Fatalf("tiered WAF %.4f must be below mixed WAF %.4f",
			tiered.WAF(), mixed.WAF())
	}
	// Require a substantial, non-noise reduction.
	improvement := (mixed.WAF() - tiered.WAF()) / mixed.WAF()
	if improvement < 0.20 {
		t.Fatalf("tiering improvement only %.1f%%, want >= 20%%", improvement*100)
	}
}

// TestTieredRoutingSeparatesHeat verifies hot and cold logical blocks land in
// distinct zones and both pools stay independent.
func TestTieredRoutingSeparatesHeat(t *testing.T) {
	eng := buildWAFEngine(t, ModeTiered)

	for lba := int64(0); lba < wafLogicalHot+wafLogicalCold; lba++ {
		if _, err := eng.Write(lba, []byte("seed")); err != nil {
			t.Fatal(err)
		}
	}
	// Second write to the first 8 LBAs promotes them to HOT.
	for lba := int64(0); lba < wafLogicalHot; lba++ {
		if _, err := eng.Write(lba, []byte("hot")); err != nil {
			t.Fatal(err)
		}
	}

	hotZones := map[int]struct{}{}
	coldZones := map[int]struct{}{}
	for lba := int64(0); lba < wafLogicalHot+wafLogicalCold; lba++ {
		loc, ok := eng.Lookup(lba)
		if !ok {
			t.Fatalf("lba %d missing", lba)
		}
		if lba < wafLogicalHot {
			if loc.Heat != HeatHot {
				t.Fatalf("lba %d heat = %s, want HOT", lba, loc.Heat)
			}
			hotZones[loc.ZoneID] = struct{}{}
		} else {
			if loc.Heat != HeatCold {
				t.Fatalf("lba %d heat = %s, want COLD", lba, loc.Heat)
			}
			coldZones[loc.ZoneID] = struct{}{}
		}
	}
	for zoneID := range hotZones {
		if _, conflict := coldZones[zoneID]; conflict {
			t.Fatalf("zone %d hosts both hot and cold current mappings", zoneID)
		}
	}

	// The mixed engine must label everything cold regardless of overwrite count.
	mixedEng := buildWAFEngine(t, ModeMixed)
	for lba := int64(0); lba < 4; lba++ {
		if _, err := mixedEng.Write(lba, []byte("a")); err != nil {
			t.Fatal(err)
		}
	}
	for lba := int64(0); lba < 4; lba++ {
		if _, err := mixedEng.Write(lba, []byte("b")); err != nil {
			t.Fatal(err)
		}
		loc, _ := mixedEng.Lookup(lba)
		if loc.Heat != HeatCold {
			t.Fatalf("mixed engine lba %d heat = %s, want COLD", lba, loc.Heat)
		}
	}
}
