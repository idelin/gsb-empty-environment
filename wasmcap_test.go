package wasmcap

import (
	"errors"
	"testing"
)

const (
	capNet = Capability("net")
	capFS  = Capability("fs")
)

var echoSig = Signature{Params: []ValueType{I32}, Results: []ValueType{I32}}

func echoBody(args []uint64) ([]uint64, error) { return args, nil }

// setup returns a host with one module granted capNet, plus a live
// instance, a captured echo function, and a 4-slot table.
func setup(t *testing.T) (*Host, *Module, *Instance, *Func, *Table) {
	t.Helper()
	h := NewHost()
	m := h.LoadModule("plugin")
	h.Grant(m, capNet)
	inst := m.Instantiate()
	fn, err := inst.CaptureFunc(echoSig, capNet, echoBody)
	if err != nil {
		t.Fatalf("CaptureFunc: %v", err)
	}
	tbl, err := inst.NewTable(4)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return h, m, inst, fn, tbl
}

func TestCallIndirectHappyPath(t *testing.T) {
	_, _, _, fn, tbl := setup(t)
	if err := tbl.Set(1, fn, capNet); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := tbl.CallIndirect(1, echoSig, 42)
	if err != nil {
		t.Fatalf("CallIndirect: %v", err)
	}
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("got %v, want [42]", got)
	}
}

func TestCallIndirectValidation(t *testing.T) {
	h, m, inst, fn, tbl := setup(t)
	if err := tbl.Set(0, fn, capNet); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"out of bounds", func() error { _, err := tbl.CallIndirect(9, echoSig, 1); return err }, ErrOutOfBounds},
		{"negative index", func() error { _, err := tbl.CallIndirect(-1, echoSig, 1); return err }, ErrOutOfBounds},
		{"null slot", func() error { _, err := tbl.CallIndirect(2, echoSig, 1); return err }, ErrNilSlot},
		{"signature mismatch", func() error {
			_, err := tbl.CallIndirect(0, Signature{Params: []ValueType{I64}}, 1)
			return err
		}, ErrSignatureMismatch},
		{"arg count mismatch", func() error { _, err := tbl.CallIndirect(0, echoSig, 1, 2); return err }, ErrSignatureMismatch},
		{"revoked capability", func() error {
			h.Revoke(m, capNet)
			defer h.Grant(m, capNet)
			_, err := tbl.CallIndirect(0, echoSig, 1)
			return err
		}, ErrCapabilityNotGranted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	// Set-time validation.
	if err := tbl.Set(1, fn, capFS); !errors.Is(err, ErrCapabilityMismatch) {
		t.Fatalf("Set with wrong cap tag: got %v", err)
	}
	fsFn, err := inst.CaptureFunc(echoSig, capFS, echoBody)
	if !errors.Is(err, ErrCapabilityNotGranted) || fsFn != nil {
		t.Fatalf("CaptureFunc without grant: fn=%v err=%v", fsFn, err)
	}
	if err := tbl.Set(7, fn, capNet); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("Set out of bounds: got %v", err)
	}
}

func TestCapabilityRevocationAndRegrant(t *testing.T) {
	h, m, _, fn, tbl := setup(t)
	if err := tbl.Set(0, fn, capNet); err != nil {
		t.Fatalf("Set: %v", err)
	}
	h.Revoke(m, capNet)
	if _, err := tbl.CallIndirect(0, echoSig, 1); !errors.Is(err, ErrCapabilityNotGranted) {
		t.Fatalf("indirect after revoke: got %v", err)
	}
	if _, err := fn.Call(1); !errors.Is(err, ErrCapabilityNotGranted) {
		t.Fatalf("direct after revoke: got %v", err)
	}
	h.Grant(m, capNet)
	if _, err := tbl.CallIndirect(0, echoSig, 1); err != nil {
		t.Fatalf("indirect after re-grant: %v", err)
	}
}

func TestCrossModuleFunctionRejected(t *testing.T) {
	h := NewHost()
	modA := h.LoadModule("a")
	modB := h.LoadModule("b")
	h.Grant(modA, capNet)
	h.Grant(modB, capNet)
	fnA, err := modA.Instantiate().CaptureFunc(echoSig, capNet, echoBody)
	if err != nil {
		t.Fatalf("CaptureFunc: %v", err)
	}
	tblB, err := modB.Instantiate().NewTable(1)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	if err := tblB.Set(0, fnA, capNet); !errors.Is(err, ErrCrossModule) {
		t.Fatalf("got %v, want ErrCrossModule", err)
	}
}

// mustFn captures a distinct function per slot so copies are observable.
func mustFn(t *testing.T, inst *Instance, id uint64) *Func {
	t.Helper()
	fn, err := inst.CaptureFunc(echoSig, capNet, func(args []uint64) ([]uint64, error) {
		return []uint64{id}, nil
	})
	if err != nil {
		t.Fatalf("CaptureFunc: %v", err)
	}
	return fn
}

func slotIDs(t *testing.T, tbl *Table, n int) []uint64 {
	t.Helper()
	ids := make([]uint64, n)
	for i := 0; i < n; i++ {
		got, err := tbl.CallIndirect(i, echoSig, 0)
		if err != nil {
			t.Fatalf("CallIndirect(%d): %v", i, err)
		}
		ids[i] = got[0]
	}
	return ids
}

func fillTable(t *testing.T, inst *Instance, tbl *Table, ids ...uint64) {
	t.Helper()
	for i, id := range ids {
		if err := tbl.Set(i, mustFn(t, inst, id), capNet); err != nil {
			t.Fatalf("Set(%d): %v", i, err)
		}
	}
}

func TestCopyTableOverlap(t *testing.T) {
	_, _, inst, _, _ := setup(t)

	// Forward overlap: dst > src within one table.
	tbl, _ := inst.NewTable(4)
	fillTable(t, inst, tbl, 1, 2, 3, 4)
	if err := CopyTable(tbl, tbl, 1, 0, 3); err != nil {
		t.Fatalf("CopyTable: %v", err)
	}
	if got, want := slotIDs(t, tbl, 4), []uint64{1, 1, 2, 3}; !equalIDs(got, want) {
		t.Fatalf("forward overlap: got %v, want %v", got, want)
	}

	// Backward overlap: dst < src within one table.
	tbl2, _ := inst.NewTable(4)
	fillTable(t, inst, tbl2, 1, 2, 3, 4)
	if err := CopyTable(tbl2, tbl2, 0, 1, 3); err != nil {
		t.Fatalf("CopyTable: %v", err)
	}
	if got, want := slotIDs(t, tbl2, 4), []uint64{2, 3, 4, 4}; !equalIDs(got, want) {
		t.Fatalf("backward overlap: got %v, want %v", got, want)
	}

	// Cross-table copy.
	dst, _ := inst.NewTable(2)
	fillTable(t, inst, dst, 9, 9)
	if err := CopyTable(dst, tbl2, 0, 1, 2); err != nil {
		t.Fatalf("CopyTable: %v", err)
	}
	if got, want := slotIDs(t, dst, 2), []uint64{3, 4}; !equalIDs(got, want) {
		t.Fatalf("cross-table: got %v, want %v", got, want)
	}
}

func TestCopyTableOutOfBoundsWritesNothing(t *testing.T) {
	_, _, inst, _, _ := setup(t)
	src, _ := inst.NewTable(4)
	fillTable(t, inst, src, 1, 2, 3, 4)
	dst, _ := inst.NewTable(4)
	fillTable(t, inst, dst, 9, 9, 9, 9)
	before := slotIDs(t, dst, 4)

	cases := []struct {
		name                 string
		dstOff, srcOff, n    int
	}{
		{"dst overflow", 2, 0, 3},
		{"src overflow", 0, 2, 3},
		{"negative n", 0, 0, -1},
		{"negative dstOff", -1, 0, 1},
		{"negative srcOff", 0, -1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := CopyTable(dst, src, tc.dstOff, tc.srcOff, tc.n); !errors.Is(err, ErrOutOfBounds) {
				t.Fatalf("got %v, want ErrOutOfBounds", err)
			}
			if got := slotIDs(t, dst, 4); !equalIDs(got, before) {
				t.Fatalf("dst mutated on failed copy: got %v, want %v", got, before)
			}
		})
	}

	// Self-copy overflow must also leave the table untouched.
	if err := CopyTable(src, src, 2, 0, 3); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("self-copy overflow: got %v", err)
	}
	if got, want := slotIDs(t, src, 4), []uint64{1, 2, 3, 4}; !equalIDs(got, want) {
		t.Fatalf("src mutated on failed self-copy: got %v, want %v", got, want)
	}
}

// TestStaleReferencesCannotRideNewInstance interleaves captured
// references, capability revocation, and module unload to prove that
// references captured from an old instance version can never execute
// through a new instance, even after capabilities are re-granted.
func TestStaleReferencesCannotRideNewInstance(t *testing.T) {
	h := NewHost()
	m := h.LoadModule("plugin")
	h.Grant(m, capNet)

	inst1 := m.Instantiate()
	oldFn, err := inst1.CaptureFunc(echoSig, capNet, echoBody)
	if err != nil {
		t.Fatalf("CaptureFunc: %v", err)
	}
	oldTbl, err := inst1.NewTable(1)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	if err := oldTbl.Set(0, oldFn, capNet); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := oldTbl.CallIndirect(0, echoSig, 7); err != nil {
		t.Fatalf("pre-unload call: %v", err)
	}

	// Interleave: revoke, unload, re-instantiate, re-grant.
	h.Revoke(m, capNet)
	m.Unload()
	inst2 := m.Instantiate()
	h.Grant(m, capNet)

	if inst2.Version() == inst1.Version() {
		t.Fatalf("new instance reused version %d", inst1.Version())
	}

	// The old captured reference is dead everywhere.
	if _, err := oldFn.Call(1); !errors.Is(err, ErrStaleInstance) {
		t.Fatalf("old fn direct call: got %v", err)
	}
	if _, err := oldTbl.CallIndirect(0, echoSig, 1); !errors.Is(err, ErrStaleInstance) {
		t.Fatalf("old table indirect call: got %v", err)
	}
	if _, err := inst1.NewTable(1); !errors.Is(err, ErrStaleInstance) {
		t.Fatalf("new table on dead instance: got %v", err)
	}
	if _, err := inst1.CaptureFunc(echoSig, capNet, echoBody); !errors.Is(err, ErrStaleInstance) {
		t.Fatalf("capture on dead instance: got %v", err)
	}

	// The old reference cannot be installed into the new instance's table.
	newTbl, err := inst2.NewTable(1)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	if err := newTbl.Set(0, oldFn, capNet); !errors.Is(err, ErrStaleInstance) {
		t.Fatalf("install stale fn into new table: got %v", err)
	}
	if _, err := newTbl.CallIndirect(0, echoSig, 1); !errors.Is(err, ErrNilSlot) {
		t.Fatalf("new table slot must remain null: got %v", err)
	}

	// A fresh reference on the new instance works fine.
	newFn, err := inst2.CaptureFunc(echoSig, capNet, echoBody)
	if err != nil {
		t.Fatalf("CaptureFunc: %v", err)
	}
	if err := newTbl.Set(0, newFn, capNet); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := newTbl.CallIndirect(0, echoSig, 99)
	if err != nil || len(got) != 1 || got[0] != 99 {
		t.Fatalf("new instance call: got=%v err=%v", got, err)
	}

	// And the old reference still fails after the new instance is live.
	if _, err := oldFn.Call(1); !errors.Is(err, ErrStaleInstance) {
		t.Fatalf("old fn after re-instantiation: got %v", err)
	}
}

func equalIDs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
