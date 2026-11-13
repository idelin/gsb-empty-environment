package netsec

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Runtime is a small host model for JIT activations. Every Frame pins the
// manifest that was current when it entered. Metadata queries are deliberately
// frame-based: there is no API for querying the "current" version, so an old
// activation can never observe metadata from a later publication.
type Runtime struct {
	pub *Publisher

	mu     sync.Mutex
	nextID uint64
	frames map[uint64]*Frame
}

// NewRuntime creates a host runtime bound to p.
func NewRuntime(p *Publisher) *Runtime {
	return &Runtime{pub: p, frames: make(map[uint64]*Frame)}
}

// Frame is one activation pinned to exactly one manifest generation.
type Frame struct {
	rt     *Runtime
	id     uint64
	parent uint64

	manifest *Manifest
	unit     *UnitVersion
	lease    *Lease

	pc       atomic.Uint64
	returnPC atomic.Uint64
	exited   atomic.Bool
}

// ID returns the activation identifier.
func (f *Frame) ID() uint64 { return f.id }

// ParentID returns the parent activation identifier, or zero for a root frame.
func (f *Frame) ParentID() uint64 { return f.parent }

// UnitID returns the code unit of this frame.
func (f *Frame) UnitID() string { return f.unit.UnitID() }

// Generation returns the pinned unit generation.
func (f *Frame) Generation() uint64 { return f.unit.Generation() }

// ManifestRoot returns the pinned manifest root.
func (f *Frame) ManifestRoot() Digest { return f.manifest.Root() }

// ContentRoot returns the pinned unit content root.
func (f *Frame) ContentRoot() Digest { return f.unit.ContentRoot() }

// PC returns the current program counter.
func (f *Frame) PC() uint64 { return f.pc.Load() }

// SetPC updates the current program counter.
func (f *Frame) SetPC(pc uint64) { f.pc.Store(pc) }

// ReturnPC returns the return address reported to the code barrier.
func (f *Frame) ReturnPC() uint64 { return f.returnPC.Load() }

// SetReturnPC updates the return address.
func (f *Frame) SetReturnPC(pc uint64) { f.returnPC.Store(pc) }

// Exited reports whether the activation has left its code.
func (f *Frame) Exited() bool { return f.exited.Load() }

// BarrierReport returns this frame's safepoint reply. The frame's lease pins
// the old generation for as long as the frame is live.
func (f *Frame) BarrierReport() BarrierReport {
	return BarrierReport{
		PC:       f.PC(),
		ReturnPC: f.ReturnPC(),
		Lease:    f.lease,
	}
}

// Exit drops the activation and its generation lease.
func (f *Frame) Exit() error {
	return f.rt.Exit(f)
}

// StackMapAt resolves the stack map at pc through this frame's pinned unit.
func (f *Frame) StackMapAt(pc uint64) (StackMapEntry, bool) {
	if f == nil || f.exited.Load() {
		return StackMapEntry{}, false
	}
	return f.unit.stackMapAt(pc)
}

// ExceptionAt resolves the exception entry covering pc through this frame's
// pinned unit.
func (f *Frame) ExceptionAt(pc uint64) (ExceptionEntry, bool) {
	if f == nil || f.exited.Load() {
		return ExceptionEntry{}, false
	}
	return f.unit.exceptionAt(pc)
}

// CallTargetAt resolves the call target at pc through this frame's pinned unit.
func (f *Frame) CallTargetAt(pc uint64) (CallTarget, bool) {
	if f == nil || f.exited.Load() {
		return CallTarget{}, false
	}
	return f.unit.callTargetAt(pc)
}

// InstructionAt resolves the instruction at pc through this frame's pinned
// unit.
func (f *Frame) InstructionAt(pc uint64) (InstructionBoundary, bool) {
	if f == nil || f.exited.Load() {
		return InstructionBoundary{}, false
	}
	return f.unit.InstructionAt(pc)
}

// FrameMetadata is a generation-consistent view of a frame's metadata.
type FrameMetadata struct {
	UnitID           string
	Generation       uint64
	ParentGeneration uint64
	ManifestRoot     Digest
	ContentRoot      Digest
	Digests          UnitDigests

	PC             uint64
	Instruction    InstructionBoundary
	HasInstruction bool
	StackMap       StackMapEntry
	HasStackMap    bool
	Exception      ExceptionEntry
	HasException   bool
	CallTarget     CallTarget
	HasCallTarget  bool
}

// MetadataAt returns all metadata for pc from the frame's pinned version. It
// never consults Publisher.CurrentManifest.
func (f *Frame) MetadataAt(pc uint64) (FrameMetadata, error) {
	if f == nil {
		return FrameMetadata{}, fmt.Errorf("netsec: nil frame")
	}
	if f.exited.Load() {
		return FrameMetadata{}, ErrFrameExited
	}
	md := FrameMetadata{
		UnitID:           f.unit.UnitID(),
		Generation:       f.unit.Generation(),
		ParentGeneration: f.unit.ParentGeneration(),
		ManifestRoot:     f.manifest.Root(),
		ContentRoot:      f.unit.ContentRoot(),
		Digests:          f.unit.Digests(),
		PC:               pc,
	}
	md.Instruction, md.HasInstruction = f.unit.InstructionAt(pc)
	md.StackMap, md.HasStackMap = f.unit.stackMapAt(pc)
	md.Exception, md.HasException = f.unit.exceptionAt(pc)
	md.CallTarget, md.HasCallTarget = f.unit.callTargetAt(pc)
	return md, nil
}

// NewRuntimeFrame creates a root activation using the currently published
// manifest. The entry-table load and lease acquisition are serialized with
// publication commit, so a call can never straddle the swap.
func (r *Runtime) Enter(unitID string) (*Frame, error) {
	return r.enterCurrent(unitID, 0)
}

// EnterOSR is a new top-level on-stack-replacement entry. It observes the
// currently published manifest.
func (r *Runtime) EnterOSR(unitID string, pc uint64) (*Frame, error) {
	return r.enterCurrent(unitID, pc)
}

func (r *Runtime) enterCurrent(unitID string, pc uint64) (*Frame, error) {
	r.pub.gate.RLock()
	defer r.pub.gate.RUnlock()
	m := r.pub.state.Load()
	u, ok := m.Resolve(unitID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownUnit, unitID)
	}
	lease, err := r.pub.pin(m, LeaseFrame)
	if err != nil {
		return nil, err
	}
	return r.install(nil, m, u, lease, pc), nil
}

// Call creates an internal/recursive activation. It deliberately reuses the
// parent frame's manifest rather than the current entry table: recursive old
// frames must stay on their pinned generation until they unwind.
func (r *Runtime) Call(parent *Frame, unitID string) (*Frame, error) {
	if parent == nil {
		return nil, fmt.Errorf("netsec: nil parent frame")
	}
	if parent.exited.Load() {
		return nil, ErrFrameExited
	}
	if parent.rt != r {
		return nil, fmt.Errorf("netsec: parent frame belongs to another runtime")
	}
	u, ok := parent.manifest.Resolve(unitID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownUnit, unitID)
	}
	lease, err := r.pub.pin(parent.manifest, LeaseFrame)
	if err != nil {
		return nil, err
	}
	return r.install(parent, parent.manifest, u, lease, 0), nil
}

// OSRFrom performs on-stack replacement inside an existing activation. The
// new frame stays on the parent's pinned unit and manifest.
func (r *Runtime) OSRFrom(parent *Frame, pc uint64) (*Frame, error) {
	if parent == nil {
		return nil, fmt.Errorf("netsec: nil parent frame")
	}
	if parent.exited.Load() {
		return nil, ErrFrameExited
	}
	if parent.rt != r {
		return nil, fmt.Errorf("netsec: parent frame belongs to another runtime")
	}
	lease, err := r.pub.pin(parent.manifest, LeaseFrame)
	if err != nil {
		return nil, err
	}
	return r.install(parent, parent.manifest, parent.unit, lease, pc), nil
}

func (r *Runtime) install(parent *Frame, m *Manifest, u *UnitVersion, lease *Lease, pc uint64) *Frame {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	f := &Frame{
		rt:       r,
		id:       r.nextID,
		manifest: m,
		unit:     u,
		lease:    lease,
	}
	if parent != nil {
		f.parent = parent.id
	}
	f.pc.Store(pc)
	r.frames[f.id] = f
	return f
}

// Exit removes f from the live-frame set and releases its generation lease.
func (r *Runtime) Exit(f *Frame) error {
	if f == nil {
		return fmt.Errorf("netsec: nil frame")
	}
	if f.rt != r {
		return fmt.Errorf("netsec: frame belongs to another runtime")
	}
	if !f.exited.CompareAndSwap(false, true) {
		return ErrFrameExited
	}
	r.mu.Lock()
	delete(r.frames, f.id)
	r.mu.Unlock()
	if f.lease != nil {
		f.lease.Release()
	}
	return nil
}

// FrameCount returns the number of live activations.
func (r *Runtime) FrameCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

// StackMapAt resolves pc through f's pinned generation.
func (r *Runtime) StackMapAt(f *Frame, pc uint64) (StackMapEntry, bool) {
	if f == nil || f.rt != r {
		return StackMapEntry{}, false
	}
	return f.StackMapAt(pc)
}

// ExceptionAt resolves pc through f's pinned generation.
func (r *Runtime) ExceptionAt(f *Frame, pc uint64) (ExceptionEntry, bool) {
	if f == nil || f.rt != r {
		return ExceptionEntry{}, false
	}
	return f.ExceptionAt(pc)
}

// CallTargetAt resolves pc through f's pinned generation.
func (r *Runtime) CallTargetAt(f *Frame, pc uint64) (CallTarget, bool) {
	if f == nil || f.rt != r {
		return CallTarget{}, false
	}
	return f.CallTargetAt(pc)
}

// MetadataAt resolves all metadata through f's pinned generation.
func (r *Runtime) MetadataAt(f *Frame, pc uint64) (FrameMetadata, error) {
	if f == nil || f.rt != r {
		return FrameMetadata{}, fmt.Errorf("netsec: frame belongs to another runtime")
	}
	return f.MetadataAt(pc)
}

// FrameSample is one sampler observation. The metadata was resolved through
// the sampled frame's pinned generation, not through the current manifest.
type FrameSample struct {
	FrameID  uint64
	Metadata FrameMetadata
}

// SampleAt walks all live frames. For each frame it takes a sampler lease on
// that frame's manifest, resolves metadata by the frame's pinned generation,
// and drops the lease. A sampler that arrives after a publication therefore
// still reads old metadata for old frames and automatically prevents those
// manifests from being reclaimed during the walk.
func (r *Runtime) SampleAt(pc uint64) ([]FrameSample, error) {
	r.mu.Lock()
	frames := make([]*Frame, 0, len(r.frames))
	for _, f := range r.frames {
		frames = append(frames, f)
	}
	r.mu.Unlock()
	sort.Slice(frames, func(i, j int) bool { return frames[i].id < frames[j].id })

	samples := make([]FrameSample, 0, len(frames))
	for _, f := range frames {
		if f.exited.Load() {
			continue
		}
		lease, err := r.pub.PinSampler(f.manifest)
		if err != nil {
			// The frame may have exited between the snapshot and the pin.
			if f.exited.Load() {
				continue
			}
			return nil, err
		}
		md, err := f.MetadataAt(pc)
		lease.Release()
		if err != nil {
			if f.exited.Load() {
				continue
			}
			return nil, err
		}
		samples = append(samples, FrameSample{FrameID: f.id, Metadata: md})
	}
	return samples, nil
}

// FrameThread adapts a Frame to the barrier Thread interface. It reports the
// frame's PC, return address and generation lease. It is not parked by the
// adapter, so the caller must use an actually quiescent host thread in a real
// runtime.
type FrameThread struct {
	Name  string
	Frame *Frame
}

// ID returns the registered thread name.
func (t *FrameThread) ID() string { return t.Name }

// Probe reports the frame's safepoint state.
func (t *FrameThread) Probe(_ context.Context, _ BarrierRequest) (BarrierReport, error) {
	if t.Frame == nil {
		return BarrierReport{}, fmt.Errorf("netsec: frame thread %q has no frame", t.Name)
	}
	return t.Frame.BarrierReport(), nil
}
