package netsec

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func makeUnitSpec(unitID string, generation, parentGeneration uint64, variant string) CodeUnitSpec {
	code := make([]byte, 0x60)
	for i := range code {
		code[i] = variant[0]
	}
	return CodeUnitSpec{
		UnitID:           unitID,
		Generation:       generation,
		ParentGeneration: parentGeneration,
		Code:             code,
		Functions: []FunctionBoundary{
			{Name: "f", Start: 0, End: 0x60},
		},
		Instructions: []InstructionBoundary{
			{PC: 0x10, Size: 1},
			{PC: 0x20, Size: 1},
			{PC: 0x30, Size: 1},
		},
		StackMap: []StackMapEntry{
			{PC: 0x10, FrameSize: map[string]uint64{"A": 8, "B": 16, "C": 24}[variant], PointerBitmap: []byte{variant[0]}},
			{PC: 0x20, FrameSize: 32, PointerBitmap: []byte{1, 2}},
		},
		ExceptionTable: []ExceptionEntry{
			{StartPC: 0x10, EndPC: 0x40, HandlerPC: map[string]uint64{"A": 0x30, "B": 0x50, "C": 0x60}[variant], Action: "catch-" + variant},
		},
		CallTargets: []CallTarget{
			{CallSite: 0x10, UnitID: "callee", Generation: generation, Offset: map[string]uint64{"A": 1, "B": 2, "C": 3}[variant]},
			{CallSite: 0x20, UnitID: "callee", Generation: generation, Offset: 7},
		},
		PatchRanges: []Range{{Start: 0, End: 0x60}},
	}
}

func makeUnit(t *testing.T, unitID string, generation, parentGeneration uint64, variant string) *UnitVersion {
	t.Helper()
	spec := makeUnitSpec(unitID, generation, parentGeneration, variant)
	u, err := NewUnitVersion(spec, Validators{})
	if err != nil {
		t.Fatalf("NewUnitVersion: %v", err)
	}
	return u
}

func mustManifest(t *testing.T, units []*UnitVersion, parent *Manifest) *Manifest {
	t.Helper()
	m, err := BuildManifest(units, parent, Validators{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	return m
}

func recordingValidators(calls *[]string, failOn string) Validators {
	check := func(name string) error {
		*calls = append(*calls, name)
		if name == failOn {
			return fmt.Errorf("rejected %s", name)
		}
		return nil
	}
	return Validators{
		Code:           func(CodeContent) error { return check("code") },
		Boundaries:     func(BoundaryContent) error { return check("boundaries") },
		StackMap:       func(StackMapContent) error { return check("stackMap") },
		ExceptionTable: func(ExceptionContent) error { return check("exceptionTable") },
		CallTargets:    func(CallTargetContent) error { return check("callTargets") },
	}
}

type fakeThread struct {
	id string

	mu       sync.Mutex
	report   BarrierReport
	probeErr error
	onProbe  func()
	probes   int
	releases []uint64
}

func newFakeThread(id string, report BarrierReport) *fakeThread {
	return &fakeThread{id: id, report: report}
}

func (f *fakeThread) ID() string { return f.id }

func (f *fakeThread) Probe(_ context.Context, _ BarrierRequest) (BarrierReport, error) {
	f.mu.Lock()
	f.probes++
	report := f.report
	err := f.probeErr
	onProbe := f.onProbe
	f.mu.Unlock()
	if onProbe != nil {
		onProbe()
	}
	return report, err
}

func (f *fakeThread) ReleaseBarrier(publicationID uint64) {
	f.mu.Lock()
	f.releases = append(f.releases, publicationID)
	f.mu.Unlock()
}

func (f *fakeThread) probeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.probes
}

func (f *fakeThread) releaseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.releases)
}
