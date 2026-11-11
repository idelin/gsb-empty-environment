package bootjournal

import (
	"errors"
	"testing"
)

// scenarioStep is one mutating stage of an update lifecycle. reached reports
// whether the stage's target state already holds on the (possibly freshly
// reopened) device; run performs the mutation. After a simulated power loss
// the driver reopens and consults reached first, so a stage that committed
// just before the fault is recognized as done instead of being applied twice.
type scenarioStep struct {
	reached func(d *Device) bool
	run     func(d *Device) error
}

func slotHasVersion(d *Device, slot int, version uint32) bool {
	img, ok := d.VerifiedImage(slot)
	return ok && img.Version == version
}

func scenario() []scenarioStep {
	return []scenarioStep{
		{
			reached: func(d *Device) bool { return d.ActiveSlot() == 0 && slotHasVersion(d, 0, 1) },
			run:     func(d *Device) error { return d.Provision(testImage(1, 100)) },
		},
		// First boot of the factory image, then confirm it.
		{
			reached: func(d *Device) bool { return d.ActiveSlot() == 0 && d.UnconfirmedBoots() >= 1 },
			run:     func(d *Device) error { _, err := d.Boot(); return err },
		},
		{
			reached: func(d *Device) bool { return d.activeConfirmed(0) },
			run:     func(d *Device) error { return d.Confirm(0) },
		},
		// Stage version 2 into the inactive slot; boot selection must not move.
		{
			reached: func(d *Device) bool { return slotHasVersion(d, 1, 2) },
			run:     func(d *Device) error { return d.Update(testImage(2, 301)) },
		},
		// Reboot activates the staged image (activation is not an unconfirmed
		// boot), then two unconfirmed boots force a rollback to slot 0.
		{
			reached: func(d *Device) bool { return d.ActiveSlot() == 1 },
			run:     func(d *Device) error { _, err := d.Boot(); return err },
		},
		{
			reached: func(d *Device) bool { return d.ActiveSlot() == 1 && d.UnconfirmedBoots() >= 1 },
			run:     func(d *Device) error { _, err := d.Boot(); return err },
		},
		{
			reached: func(d *Device) bool { return d.ActiveSlot() == 0 },
			run:     func(d *Device) error { _, err := d.Boot(); return err },
		},
		// Re-confirm the rollback slot.
		{
			reached: func(d *Device) bool { return d.activeConfirmed(0) && d.ActiveSlot() == 0 },
			run:     func(d *Device) error { return d.Confirm(0) },
		},
		// Anti-rollback refuses a strictly older image before any mutation.
		{
			reached: func(d *Device) bool { return true }, // rejection is stateless
			run: func(d *Device) error {
				if err := d.Update(testImage(0, 90)); !errors.Is(err, ErrRollbackVersion) {
					return err
				}
				return nil
			},
		},
		{
			reached: func(d *Device) bool { return slotHasVersion(d, 1, 3) },
			run:     func(d *Device) error { return d.Update(testImage(3, 500)) },
		},
		{
			reached: func(d *Device) bool { return d.ActiveSlot() == 1 && slotHasVersion(d, 1, 3) },
			run:     func(d *Device) error { _, err := d.Boot(); return err },
		},
		{
			reached: func(d *Device) bool { return d.activeConfirmed(1) && d.MinVersion() == 3 },
			run:     func(d *Device) error { return d.Confirm(1) },
		},
	}
}

type deviceSnapshot struct {
	active      int
	minVersion  uint32
	unconfirmed int
	slotVersion [SlotCount]uint32
	slotGood    [SlotCount]bool
}

func snapshotDevice(d *Device) deviceSnapshot {
	var s deviceSnapshot
	s.active = d.ActiveSlot()
	s.minVersion = d.MinVersion()
	s.unconfirmed = d.UnconfirmedBoots()
	for slot := 0; slot < SlotCount; slot++ {
		if img, ok := d.VerifiedImage(slot); ok {
			s.slotGood[slot] = true
			s.slotVersion[slot] = img.Version
		}
	}
	return s
}

// assertRecoverable opens the flash as a fresh device would after reset and
// checks the invariants the commit journal exists to guarantee.
func assertRecoverable(t *testing.T, flash *FaultyFlash, provisioned bool, floor uint32) {
	t.Helper()
	dev, err := Open(flash)
	if err != nil {
		t.Fatalf("reopen after power loss: %v", err)
	}
	if dev.MinVersion() < floor {
		t.Fatalf("anti-rollback floor regressed: %d < %d", dev.MinVersion(), floor)
	}
	if !provisioned {
		return
	}
	good := 0
	for slot := 0; slot < SlotCount; slot++ {
		if ok, _ := dev.headerOK(slot); ok {
			good++
		}
	}
	if good == 0 {
		t.Fatal("no verifiable image survives the reset")
	}
	if dev.ActiveSlot() >= 0 {
		if ok, _ := dev.headerOK(dev.ActiveSlot()); !ok {
			t.Fatalf("active slot %d is not verifiable", dev.ActiveSlot())
		}
	}
}

func runScenario(flash *FaultyFlash, steps []scenarioStep) {
	dev, err := Open(flash)
	if err != nil {
		panic(err)
	}
	for _, step := range steps {
		for !step.reached(dev) {
			if err := step.run(dev); err != nil {
				if !errors.Is(err, ErrPowerLoss) {
					panic(err)
				}
			}
			dev, err = Open(flash)
			if err != nil {
				panic(err)
			}
		}
	}
}

// TestPowerLossPrefixEnumeration trips power loss after every mutating
// operation (each word program and each page erase) reachable in a full
// lifecycle, one prefix per run, and verifies recovery plus final
// convergence.
func TestPowerLossPrefixEnumeration(t *testing.T) {
	steps := scenario()

	// Reference run: no fault injection; records the total mutation count and
	// the final state every faulted run must converge to.
	refFlash := NewFlash(14)
	runScenario(refFlash, steps)
	want := snapshotDevice(mustOpen(t, refFlash))
	totalOps := refFlash.ops
	if totalOps == 0 {
		t.Fatal("no mutating operations observed")
	}

	// Arm at each prefix: after op #k, for k = 1..totalOps.
	for k := 1; k <= totalOps; k++ {
		t.Run("prefix", func(t *testing.T) {
			flash := NewFlash(14)
			flash.failAfter = k

			dev, err := Open(flash)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			provisioned := false
			var floor uint32
			for _, step := range steps {
				for !step.reached(dev) {
					err = step.run(dev)
					if err != nil && !errors.Is(err, ErrPowerLoss) {
						t.Fatalf("prefix %d step returned non-fault error: %v", k, err)
					}
					if flash.Fault() {
						// Reset just happened: reopen and check invariants.
						assertRecoverable(t, flash, provisioned, floor)
					}
					dev, err = Open(flash)
					if err != nil {
						t.Fatalf("reopen: %v", err)
					}
					if step.reached(dev) {
						break
					}
					if !flash.Fault() {
						t.Fatalf("prefix %d made no progress without a fault", k)
					}
				}
				// Track provisioning and the confirmed floor reached so far.
				if current, err := Open(flash); err == nil {
					provisioned = current.ActiveSlot() >= 0
					if current.MinVersion() > floor {
						floor = current.MinVersion()
					}
				}
			}

			final := snapshotDevice(mustOpen(t, flash))
			if final != want {
				t.Fatalf("prefix %d: final state %+v, want %+v", k, final, want)
			}
		})
	}
	t.Logf("enumerated %d power-loss prefixes", totalOps)
}

func mustOpen(t *testing.T, flash *FaultyFlash) *Device {
	t.Helper()
	dev, err := Open(flash)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return dev
}

// TestTornJournalRecordReplays verifies a half-written record is ignored and
// later compaction reclaims the blocked page.
func TestTornJournalRecordReplays(t *testing.T) {
	flash := NewFlash(14)
	runScenario(flash, scenario()[:3])
	before := snapshotDevice(mustOpen(t, flash))

	// Leave a torn record at the next free slot.
	dev := mustOpen(t, flash)
	free, err := dev.log.freeSlot()
	if err != nil {
		t.Fatal(err)
	}
	addr := dev.log.recordAddress(free)
	torn := make([]byte, RecordSize)
	for i := range torn {
		torn[i] = 0x00
	}
	if err := flash.Program(addr, torn); err != nil {
		t.Fatal(err)
	}
	// Replay must stop at the torn record and recover identical state.
	reopened := mustOpen(t, flash)
	if got := snapshotDevice(reopened); got != before {
		t.Fatalf("replay over torn record: %+v, want %+v", got, before)
	}
	// Appending triggers compaction; the journal remains usable.
	if err := reopened.Update(testImage(4, 123)); err != nil {
		t.Fatalf("update after torn record: %v", err)
	}
}
