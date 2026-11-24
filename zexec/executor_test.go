package zexec

import (
	"bytes"
	"errors"
	"testing"

	"prescreen.local/ps-7e522fc03b/zdevice"
	"prescreen.local/ps-7e522fc03b/zplanner"
)

func sampleOps() []zplanner.Op {
	return []zplanner.Op{
		{ID: 0, Kind: zplanner.OpWrite, Writes: map[string]zplanner.CID{"a": 1, "b": 2}},
		{ID: 1, Kind: zplanner.OpSnapshot, Name: "snap"},
		{ID: 2, Kind: zplanner.OpWrite, Writes: map[string]zplanner.CID{"a": 3}},
		{ID: 3, Kind: zplanner.OpDeleteSnapshot, Name: "snap"},
		{ID: 4, Kind: zplanner.OpWrite, Writes: map[string]zplanner.CID{"b": 4}},
	}
}

func formatDevice(t *testing.T, dir string) *zdevice.Device {
	t.Helper()
	dev := zdevice.NewDevice(dir)
	if err := dev.Format(); err != nil {
		t.Fatalf("Format: %v", err)
	}
	return dev
}

func resume(t *testing.T, dev *zdevice.Device, ops []zplanner.Op, boot int) *Executor {
	t.Helper()
	ex := NewExecutor(dev, ops)
	if err := ex.Resume(boot); err != nil {
		t.Fatalf("Resume(boot=%d): %v", boot, err)
	}
	return ex
}

func TestRunCompletesSequence(t *testing.T) {
	dev := formatDevice(t, t.TempDir())
	ops := sampleOps()
	ex := resume(t, dev, ops, 0)
	applied, err := ex.Run()
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if applied != len(ops) {
		t.Fatalf("applied=%d want %d", applied, len(ops))
	}
	if ex.table.Roots["a"] != 3 || ex.table.Roots["b"] != 4 {
		t.Fatalf("unexpected roots %v", ex.table.Roots)
	}
	if len(ex.table.Snaps) != 0 {
		t.Fatalf("snapshot should be deleted, got %v", ex.table.Snaps)
	}
}

func TestSharedContentAcrossSnapshots(t *testing.T) {
	dev := formatDevice(t, t.TempDir())
	ops := []zplanner.Op{
		{ID: 0, Kind: zplanner.OpWrite, Writes: map[string]zplanner.CID{"a": 1}},
		{ID: 1, Kind: zplanner.OpSnapshot, Name: "s1"},
		{ID: 2, Kind: zplanner.OpSnapshot, Name: "s2"},
		{ID: 3, Kind: zplanner.OpWrite, Writes: map[string]zplanner.CID{"a": 2}},
	}
	ex := resume(t, dev, ops, 0)
	if _, err := ex.Run(); err != nil {
		t.Fatal(err)
	}
	tbl := ex.table
	if tbl.Roots["a"] != 2 {
		t.Fatalf("root should move to CID 2, got %d", tbl.Roots["a"])
	}
	if tbl.Snaps["s1"]["a"] != 1 || tbl.Snaps["s2"]["a"] != 1 {
		t.Fatalf("snapshots must share CID 1: %v %v", tbl.Snaps["s1"], tbl.Snaps["s2"])
	}
	shared, ok := tbl.Loc[1]
	if !ok {
		t.Fatal("shared CID 1 missing from locator")
	}
	data, err := dev.Read(shared)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload(1)) {
		t.Fatalf("shared payload mismatch %q", data)
	}
}

func TestUnpublishedCopyLostOnCrash(t *testing.T) {
	dir := t.TempDir()
	dev := formatDevice(t, dir)
	ops := sampleOps()
	ex := resume(t, dev, ops, 0)
	if _, err := ex.ProcessNext(); err != nil {
		t.Fatal(err)
	}

	// Stage an internal copy and cut power before it is published.
	phys := ex.table.Loc[1]
	data, err := dev.Read(phys)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := dev.Fill()
	if _, err := dev.Append(1, data); err != nil {
		t.Fatal(err)
	}
	dev.PowerOff()

	recovered := zdevice.NewDevice(dir)
	rx := resume(t, recovered, ops, 0)
	after, _ := recovered.Fill()
	if after != before {
		t.Fatalf("unpublished copy survived crash: before=%v after=%v", before, after)
	}
	if rx.LastAck() != 0 {
		t.Fatalf("LastAck=%d want 0", rx.LastAck())
	}
	// The sequence still completes once from the recovered state.
	if _, err := rx.Run(); err != nil {
		t.Fatalf("recovery run: %v", err)
	}
}

func TestAcknowledgedOpRedeliveredAfterReplyLoss(t *testing.T) {
	dev := formatDevice(t, t.TempDir())
	ops := sampleOps()
	ex := resume(t, dev, ops, 0)
	if _, err := ex.ProcessNext(); err != nil {
		t.Fatal(err)
	}
	// The client never received the reply and redelivers the same operation.
	dup, err := ex.ProcessOp(0)
	if err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if !dup {
		t.Fatal("redelivery must be answered as a duplicate, not reapplied")
	}
	if ex.LastAck() != 0 {
		t.Fatalf("LastAck changed to %d", ex.LastAck())
	}
	applied, err := ex.Run()
	if err != nil {
		t.Fatal(err)
	}
	if applied != len(ops)-1 {
		t.Fatalf("applied=%d want %d", applied, len(ops)-1)
	}
}

func TestCrashAfterEveryActionFromBothSlots(t *testing.T) {
	ops := sampleOps()
	total := countDeviceActions(t, ops)
	if total == 0 {
		t.Fatal("expected at least one fault injection point")
	}
	for fault := 0; fault < total; fault++ {
		crashDir := crashAt(t, ops, fault)
		for _, boot := range []int{0, 1} {
			dev := zdevice.NewDevice(crashDir)
			ex := resume(t, dev, ops, boot)
			applied, err := ex.Run()
			if err != nil {
				t.Fatalf("fault=%d boot=%d: %v", fault, boot, err)
			}
			verifyFinalState(t, ex, dev, applied)
			dev.PowerOff()
		}
	}
}

func countDeviceActions(t *testing.T, ops []zplanner.Op) int {
	t.Helper()
	dev := formatDevice(t, t.TempDir())
	ex := resume(t, dev, ops, 0)
	actions := 0
	for ex.LastAck() < ops[len(ops)-1].ID {
		stop, err := ex.ExecutePlanWithFault(-1, &actions)
		if err != nil {
			t.Fatal(err)
		}
		if stop {
			t.Fatal("faultAt=-1 must never fire")
		}
	}
	return actions
}

func crashAt(t *testing.T, ops []zplanner.Op, fault int) string {
	t.Helper()
	dir := t.TempDir()
	dev := formatDevice(t, dir)
	ex := resume(t, dev, ops, 0)
	actions := 0
	for ex.LastAck() < ops[len(ops)-1].ID {
		stop, err := ex.ExecutePlanWithFault(fault, &actions)
		if err != nil {
			t.Fatalf("fault=%d: %v", fault, err)
		}
		if stop {
			dev.PowerOff()
			return dir
		}
	}
	t.Fatalf("fault %d exceeded action count", fault)
	return ""
}

func verifyFinalState(t *testing.T, ex *Executor, dev *zdevice.Device, applied int) {
	t.Helper()
	tbl := ex.table
	if tbl.Roots["a"] != 3 || tbl.Roots["b"] != 4 {
		t.Fatalf("bad roots after recovery: %v", tbl.Roots)
	}
	if len(tbl.Snaps) != 0 {
		t.Fatalf("snapshots leaked after recovery: %v", tbl.Snaps)
	}
	for cid, phys := range tbl.Loc {
		data, err := dev.Read(phys)
		if err != nil {
			t.Fatalf("CID %d unreadable at %+v: %v", cid, phys, err)
		}
		if !bytes.Equal(data, payload(cid)) {
			t.Fatalf("CID %d payload mismatch %q", cid, data)
		}
	}
	if applied < 0 || applied > len(sampleOps()) {
		t.Fatalf("applied %d outside [0,%d]", applied, len(sampleOps()))
	}
}

func TestResetRefusesPinnedZoneAtExecutorLevel(t *testing.T) {
	dev := formatDevice(t, t.TempDir())
	ops := []zplanner.Op{
		{ID: 0, Kind: zplanner.OpWrite, Writes: map[string]zplanner.CID{"a": 1}},
	}
	ex := resume(t, dev, ops, 0)
	if _, err := ex.Run(); err != nil {
		t.Fatal(err)
	}
	phys := ex.table.Loc[1]
	if err := dev.ResetZone(phys.Zone); !errors.Is(err, zdevice.ErrPinned) {
		t.Fatalf("expected ErrPinned, got %v", err)
	}
}
