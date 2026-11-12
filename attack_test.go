package netsec

import (
	"errors"
	"testing"
)

// buildIndirectWord builds an indirect descriptor pointing at tableAddr with
// count entries.
func buildIndirectWord(tableAddr uint64, count uint16) []byte {
	return word(
		fieldAt(0, 1, opIndirect),
		fieldAt(1, 2, uint64(count)),
		fieldAt(8, 8, tableAddr),
	)
}

func buildWriteWord(addr, value uint32) []byte {
	return word(
		fieldAt(0, 1, opDirectWrite),
		fieldAt(4, 4, uint64(addr)),
		fieldAt(8, 4, uint64(value)),
	)
}

// TestSharedSubTable points two top-level indirect descriptors at the same
// sub-table. The shared entry must be parsed along both reachable paths
// without a false cycle report, and every reachable page must end up frozen.
func TestSharedSubTable(t *testing.T) {
	dev := NewSimDevice(0x30000)
	const (
		tableA uint64 = 0x10000
		tableB uint64 = 0x11000
		shared uint64 = 0x12000
	)
	entry := buildWriteWord(0x20000, 0x1111)
	if err := dev.Load(shared, entry); err != nil {
		t.Fatal(err)
	}
	if err := dev.Load(tableA, buildIndirectWord(shared, 1)); err != nil {
		t.Fatal(err)
	}
	if err := dev.Load(tableB, buildIndirectWord(shared, 1)); err != nil {
		t.Fatal(err)
	}
	v := newTestValidator(t, dev)
	plan, err := v.Prepare([][]byte{
		buildIndirectWord(tableA, 1),
		buildIndirectWord(tableB, 1),
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if plan.WriteCount() != 2 {
		t.Fatalf("WriteCount = %d, want 2 (shared table resolved per reachable path)", plan.WriteCount())
	}
	wantPages := []uint64{0x10000, 0x11000, 0x12000, 0x20000}
	if got := plan.Pages(); !equalUint64(got, wantPages) {
		t.Fatalf("Pages = %#x, want %#x", got, wantPages)
	}
	if err := plan.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	var dst [4]byte
	if err := dev.Read(0x20000, dst[:]); err != nil {
		t.Fatal(err)
	}
	if binaryUint32(dst[:]) != 0x1111 {
		t.Fatalf("shared-table write = %#x, want 0x1111", dst)
	}
}

// TestIndirectCycle ensures a self-referential table is rejected instead of
// recursing until stack exhaustion.
func TestIndirectCycle(t *testing.T) {
	dev := NewSimDevice(0x30000)
	const table uint64 = 0x10000
	if err := dev.Load(table, buildIndirectWord(table, 1)); err != nil {
		t.Fatal(err)
	}
	v := newTestValidator(t, dev)
	if _, err := v.Prepare([][]byte{buildIndirectWord(table, 1)}); !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
	if pages := dev.FrozenPages(); len(pages) != 0 {
		t.Fatalf("device left frozen on failed prepare: %#x", pages)
	}
}

// TestSelfModifyingTable snapshots table contents during Prepare, so a later
// rewrite of the table cannot redirect the commit. It also verifies that
// non-commit writes are refused while the commit holds the pages frozen.
func TestSelfModifyingTable(t *testing.T) {
	dev := NewSimDevice(0x30000)
	const table uint64 = 0x10000
	benign := buildWriteWord(0x20000, 0xabcd)
	malicious := buildWriteWord(0x21000, 0x9999)
	if err := dev.Load(table, benign); err != nil {
		t.Fatal(err)
	}
	v := newTestValidator(t, dev)
	plan, err := v.Prepare([][]byte{buildIndirectWord(table, 1)})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	// Privileged tampering after snapshot cannot redirect the commit: the
	// plan never re-reads device table memory.
	if err := dev.Load(table, malicious); err != nil {
		t.Fatal(err)
	}
	freezing := dev.Freeze(0x10000)
	if freezing != nil {
		t.Fatal(freezing)
	}
	if err := dev.ExternalWrite(table, malicious); !errors.Is(err, ErrFrozenPage) {
		t.Fatalf("ExternalWrite while frozen err = %v, want ErrFrozenPage", err)
	}
	if err := dev.UnfreezeAll(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	var benignDst, evilDst [4]byte
	if err := dev.Read(0x20000, benignDst[:]); err != nil {
		t.Fatal(err)
	}
	if err := dev.Read(0x21000, evilDst[:]); err != nil {
		t.Fatal(err)
	}
	if binaryUint32(benignDst[:]) != 0xabcd {
		t.Fatalf("benign destination = %#x, want 0xabcd", benignDst)
	}
	if binaryUint32(evilDst[:]) != 0 {
		t.Fatalf("malicious destination = %#x, want zero (snapshot not re-read)", evilDst)
	}
}

// TestIllegalTailEntry builds a batch whose final descriptor references an
// unauthorized write region. Prepare must reject it before any freeze, and
// no plan must be committable: zero partial execution.
func TestIllegalTailEntry(t *testing.T) {
	dev := NewSimDevice(0x30000)
	const table uint64 = 0x10000
	legal := buildWriteWord(0x20000, 1)
	illegal := buildWriteWord(0x50000, 2)
	if err := dev.Load(table, append(append([]byte{}, legal...), illegal...)); err != nil {
		t.Fatal(err)
	}
	v := newTestValidator(t, dev)
	plan, err := v.Prepare([][]byte{buildIndirectWord(table, 2)})
	if !errors.Is(err, ErrWriteDenied) {
		t.Fatalf("Prepare err = %v, want ErrWriteDenied", err)
	}
	if plan != nil {
		t.Fatalf("plan = %v, want nil on rejected batch", plan)
	}
	if pages := dev.FrozenPages(); len(pages) != 0 {
		t.Fatalf("pages frozen on failed prepare: %#x", pages)
	}
	var dst [4]byte
	if err := dev.Read(0x20000, dst[:]); err != nil {
		t.Fatal(err)
	}
	if binaryUint32(dst[:]) != 0 {
		t.Fatalf("device mutated before commit: %#x", dst)
	}
}

// TestCommitRollback injects a device failure on the second write. The first
// write must be restored from the Prepare shadow and every page released.
func TestCommitRollback(t *testing.T) {
	dev := NewSimDevice(0x30000)
	first := buildWriteWord(0x20000, 0xaaaa)
	second := buildWriteWord(0x20100, 0xbbbb)
	v := newTestValidator(t, dev)
	dev.FailWriteFrom(0x20100, errors.New("simulated bus fault"))
	plan, err := v.Prepare([][]byte{first, second})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	err = plan.Commit()
	if !errors.Is(err, ErrCommitAborted) {
		t.Fatalf("Commit err = %v, want ErrCommitAborted", err)
	}
	var dst0, dst1 [4]byte
	if err := dev.Read(0x20000, dst0[:]); err != nil {
		t.Fatal(err)
	}
	if err := dev.Read(0x20100, dst1[:]); err != nil {
		t.Fatal(err)
	}
	if binaryUint32(dst0[:]) != 0 || binaryUint32(dst1[:]) != 0 {
		t.Fatalf("partial execution survived rollback: %#x %#x", dst0, dst1)
	}
	if pages := dev.FrozenPages(); len(pages) != 0 {
		t.Fatalf("pages left frozen after abort: %#x", pages)
	}
}

// TestFreezeFailure means Commit aborts before the first write when a page
// cannot be frozen, guaranteeing freeze-all-before-commit ordering.
func TestFreezeFailure(t *testing.T) {
	dev := NewSimDevice(0x30000)
	faulty := &freezeFaultDevice{DeviceMemory: dev, badPage: 0x20000}
	fv, err := NewValidator(testPatternSet(), testAuthz(), faulty)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := fv.Prepare([][]byte{buildWriteWord(0x20000, 7)})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := plan.Commit(); !errors.Is(err, ErrCommitAborted) {
		t.Fatalf("Commit err = %v, want ErrCommitAborted", err)
	}
	var dst [4]byte
	if err := dev.Read(0x20000, dst[:]); err != nil {
		t.Fatal(err)
	}
	if binaryUint32(dst[:]) != 0 {
		t.Fatalf("write happened despite freeze failure: %#x", dst)
	}
}

type freezeFaultDevice struct {
	DeviceMemory
	badPage uint64
}

func (f *freezeFaultDevice) Freeze(pageAddr uint64) error {
	if pageAddr == f.badPage {
		return errors.New("simulated freeze failure")
	}
	return f.DeviceMemory.Freeze(pageAddr)
}
