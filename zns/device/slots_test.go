package device

import (
	"errors"
	"sync"
	"testing"
)

func openCount(d *Device) int {
	n := 0
	for _, info := range d.Zones() {
		if info.State.IsOpen() {
			n++
		}
	}
	return n
}

func activeCount(d *Device) int {
	n := 0
	for _, info := range d.Zones() {
		if info.State.IsActive() {
			n++
		}
	}
	return n
}

// TestMORImplicitEvictionProtectsExplicit covers acceptance item 2 (MOR):
// full Open slots trigger eviction of exactly one IMPLICITLY_OPEN zone, while
// a pool of EXPLICITLY_OPEN zones is protected and reports TooManyOpenZones.
func TestMORImplicitEvictionProtectsExplicit(t *testing.T) {
	dev, err := New(Config{NumZones: 5, ZoneSize: 4, ZoneCapacity: 4, MaxOpenZones: 2})
	if err != nil {
		t.Fatal(err)
	}

	// Two implicit opens via writes fill MOR.
	if err := dev.Write(0, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := dev.Write(4, 1, nil); err != nil {
		t.Fatal(err)
	}
	if got := openCount(dev); got != 2 {
		t.Fatalf("open count = %d, want 2", got)
	}

	// A third implicit open must evict the lowest-id implicit zone to CLOSED.
	if err := dev.Write(8, 1, nil); err != nil {
		t.Fatalf("write triggering eviction: %v", err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneClosed {
		t.Fatalf("zone 0 state = %s, want CLOSED (implicit eviction)", info.State)
	}
	if info, _ := dev.Zone(1); info.State != ZoneImplicitlyOpen {
		t.Fatalf("zone 1 state = %s, want still IMPLICITLY_OPEN", info.State)
	}
	if info, _ := dev.Zone(2); info.State != ZoneImplicitlyOpen {
		t.Fatalf("zone 2 state = %s, want IMPLICITLY_OPEN", info.State)
	}
	if dev.Stats().EvictionCount != 1 {
		t.Fatalf("eviction count = %d, want 1", dev.Stats().EvictionCount)
	}

	// Reopen zone 0 explicitly and protect every open zone.
	if err := dev.Open(0); err != nil {
		t.Fatalf("explicit reopen: %v", err)
	}
	if err := dev.Close(1); err != nil {
		t.Fatal(err)
	}
	if err := dev.Open(1); err != nil {
		t.Fatalf("explicit open 1: %v", err)
	}
	for _, id := range []int{0, 1} {
		if info, _ := dev.Zone(id); info.State != ZoneExplicitlyOpen {
			t.Fatalf("zone %d = %s, want EXPLICITLY_OPEN", id, info.State)
		}
	}

	// All open zones are explicit: no eviction candidate -> TooManyOpenZones.
	err = dev.Write(12, 1, nil)
	if !errors.Is(err, ErrTooManyOpenZones) {
		t.Fatalf("explicit-protected open err = %v, want TooManyOpenZones", err)
	}
	if err := dev.Open(3); !errors.Is(err, ErrTooManyOpenZones) {
		t.Fatalf("explicit open 3 err = %v", err)
	}
	if info, _ := dev.Zone(3); info.State != ZoneEmpty {
		t.Fatalf("rejected open mutated zone 3 to %s", info.State)
	}

	// Finishing an explicit zone releases both slots and permits a new open.
	if err := dev.Finish(1); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if info, _ := dev.Zone(1); info.State != ZoneFull {
		t.Fatalf("zone 1 = %s, want FULL", info.State)
	}
	if err := dev.Open(3); err != nil {
		t.Fatalf("open after finish: %v", err)
	}
	if info, _ := dev.Zone(3); info.State != ZoneExplicitlyOpen {
		t.Fatalf("zone 3 = %s, want EXPLICITLY_OPEN", info.State)
	}
}

// TestMARActiveAccounting covers acceptance item 2 (MAR): EMPTY activation
// consumes an Active slot, CLOSED retains it, Finish/Reset releases it, and
// exhaustion reports TooManyActiveZones.
func TestMARActiveAccounting(t *testing.T) {
	dev, err := New(Config{
		NumZones:       5,
		ZoneSize:       4,
		ZoneCapacity:   4,
		MaxOpenZones:   0,
		MaxActiveZones: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := dev.Write(0, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := dev.Write(4, 1, nil); err != nil {
		t.Fatal(err)
	}
	if got := activeCount(dev); got != 2 {
		t.Fatalf("active = %d, want 2", got)
	}

	// Activating a third EMPTY zone exceeds MAR.
	if err := dev.Write(8, 1, nil); !errors.Is(err, ErrTooManyActiveZones) {
		t.Fatalf("MAR exhaustion err = %v, want TooManyActiveZones", err)
	}
	if err := dev.Open(2); !errors.Is(err, ErrTooManyActiveZones) {
		t.Fatalf("explicit open MAR err = %v", err)
	}

	// Closing keeps the Active slot: MAR still exhausted.
	if err := dev.Close(0); err != nil {
		t.Fatal(err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneClosed {
		t.Fatalf("zone 0 = %s, want CLOSED", info.State)
	}
	if got := activeCount(dev); got != 2 {
		t.Fatalf("active after close = %d, want 2", got)
	}
	if err := dev.Write(8, 1, nil); !errors.Is(err, ErrTooManyActiveZones) {
		t.Fatalf("MAR still exhausted after close: %v", err)
	}

	// Writing back into a CLOSED zone needs no new Active slot.
	if err := dev.Write(1, 1, nil); err != nil {
		t.Fatalf("reopen CLOSED for write: %v", err)
	}

	// Finish forces FULL and releases the Active slot.
	if err := dev.Finish(1); err != nil {
		t.Fatal(err)
	}
	if got := activeCount(dev); got != 1 {
		t.Fatalf("active after finish = %d, want 1", got)
	}
	if err := dev.Write(8, 1, nil); err != nil {
		t.Fatalf("activation after finish freed slot: %v", err)
	}

	// Reset releases the remaining CLOSED zone's Active slot back to EMPTY.
	if err := dev.Reset(0); err != nil {
		t.Fatal(err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneEmpty || info.WritePointer != info.SLBA {
		t.Fatalf("zone 0 after reset: %+v", info)
	}
	if got := activeCount(dev); got != 1 {
		t.Fatalf("active after reset = %d, want 1", got)
	}
}

// TestSevenStateLifecycle walks every documented zone state and checks the
// legal/illegal transitions plus read-only/offline command rejection.
func TestSevenStateLifecycle(t *testing.T) {
	dev, _ := New(Config{NumZones: 3, ZoneSize: 4, ZoneCapacity: 4})

	info, _ := dev.Zone(0)
	if info.State != ZoneEmpty {
		t.Fatalf("initial = %s", info.State)
	}

	if err := dev.Write(0, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := dev.Open(0); err != nil {
		t.Fatal(err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneExplicitlyOpen {
		t.Fatalf("state = %s", info.State)
	}
	if err := dev.Close(0); err != nil {
		t.Fatal(err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneClosed {
		t.Fatalf("state = %s", info.State)
	}
	if err := dev.Finish(0); err != nil {
		t.Fatal(err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneFull {
		t.Fatalf("state = %s", info.State)
	}
	if err := dev.Close(0); !errors.Is(err, ErrInvalidZoneState) {
		t.Fatalf("close FULL err = %v", err)
	}

	if err := dev.Reset(0); err != nil {
		t.Fatal(err)
	}
	if info, _ := dev.Zone(0); info.State != ZoneEmpty {
		t.Fatalf("state = %s", info.State)
	}
	if err := dev.Reset(0); !errors.Is(err, ErrInvalidZoneState) {
		t.Fatalf("reset EMPTY err = %v", err)
	}

	// READ_ONLY rejects writes but still serves reads.
	if err := dev.Write(4, 1, []byte("ro")); err != nil {
		t.Fatal(err)
	}
	if err := dev.SetReadOnly(1); err != nil {
		t.Fatal(err)
	}
	if info, _ := dev.Zone(1); info.State != ZoneReadOnly {
		t.Fatalf("state = %s", info.State)
	}
	if err := dev.Write(5, 1, nil); !errors.Is(err, ErrZoneReadOnly) {
		t.Fatalf("write RO err = %v", err)
	}
	if got, err := dev.Read(4); err != nil || string(got) != "ro" {
		t.Fatalf("RO read = %q, %v", got, err)
	}

	// OFFLINE rejects reads and writes.
	if err := dev.SetOffline(2); err != nil {
		t.Fatal(err)
	}
	info, _ = dev.Zone(2)
	if info.State != ZoneOffline {
		t.Fatalf("state = %s", info.State)
	}
	if _, err := dev.Read(8); !errors.Is(err, ErrZoneOffline) {
		t.Fatalf("read OFFLINE err = %v", err)
	}
	if err := dev.Write(8, 1, nil); !errors.Is(err, ErrZoneOffline) {
		t.Fatalf("write OFFLINE err = %v", err)
	}
}

// TestConcurrentZoneAppend exercises the device mutex: many goroutines append
// to the same zone and every assigned LBA must be unique and in range.
func TestConcurrentZoneAppend(t *testing.T) {
	dev, _ := New(Config{NumZones: 1, ZoneSize: 64, ZoneCapacity: 64})

	const writers = 16
	const perWriter = 4
	var wg sync.WaitGroup
	assigned := make(chan int64, writers*perWriter)
	errCh := make(chan error, 1)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				lba, err := dev.ZoneAppend(0, 1, []byte("x"))
				if err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
				assigned <- lba
			}
		}()
	}
	wg.Wait()
	close(assigned)

	select {
	case err := <-errCh:
		t.Fatalf("concurrent append failed: %v", err)
	default:
	}

	seen := make(map[int64]struct{}, writers*perWriter)
	for lba := range assigned {
		if lba < 0 || lba >= 64 {
			t.Fatalf("assigned lba %d out of range", lba)
		}
		if _, dup := seen[lba]; dup {
			t.Fatalf("duplicate assigned lba %d", lba)
		}
		seen[lba] = struct{}{}
	}
	if len(seen) != writers*perWriter {
		t.Fatalf("unique appends = %d, want %d", len(seen), writers*perWriter)
	}
	if info, _ := dev.Zone(0); info.State != ZoneFull || info.WritePointer != 64 {
		t.Fatalf("final zone state: %+v", info)
	}
}
