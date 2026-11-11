package netsec

import (
	"sync"
)

// fakeGate is a one-shot pause point. Arm it before starting the work, wait
// until the worker reaches it, interleave whatever lifecycle event the test
// needs, then release it.
type fakeGate struct {
	mu      sync.Mutex
	armed   bool
	reached chan struct{}
	release chan struct{}
}

func newFakeGate() *fakeGate {
	return &fakeGate{}
}

func (g *fakeGate) arm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.armed = true
	g.reached = make(chan struct{})
	g.release = make(chan struct{})
}

func (g *fakeGate) wait() {
	g.mu.Lock()
	if !g.armed {
		g.mu.Unlock()
		return
	}
	reached := g.reached
	release := g.release
	// Consume the gate: later requests hitting the same checkpoint pass
	// straight through until the test arms it again.
	g.armed = false
	g.mu.Unlock()
	close(reached)
	<-release
}

func (g *fakeGate) waitReached() {
	g.mu.Lock()
	reached := g.reached
	g.mu.Unlock()
	<-reached
}

func (g *fakeGate) releaseOnce() {
	g.mu.Lock()
	release := g.release
	g.mu.Unlock()
	close(release)
}

// handleRecord is the fake's bookkeeping for one opened handle.
type handleRecord struct {
	id          int64
	closed      bool
	transferred bool
	injectedRef ProcessRef
}

// fakeSystem is a controllable System for tests. It enforces the same
// ownership rules the real interface promises (generation matching, single
// transfer, no closing of foreign handles) and can block at manager
// checkpoints or fail individual operations on demand.
type fakeSystem struct {
	mu sync.Mutex

	nextHandleID int64
	handles      map[int64]*handleRecord

	// liveGeneration mirrors the process supervisor view used by the fake
	// to refuse injection/response into a replaced process instance.
	liveGeneration map[ProcessID]uint64

	gates map[Checkpoint]*fakeGate

	// Scripted failures. nil means the operation succeeds.
	openErr     error
	injectErr   error
	responseErr error

	// violations records attempts the ownership rules forbid, e.g. closing a
	// handle after it was transferred.
	violations []string

	callCounts map[string]int
}

func newFakeSystem() *fakeSystem {
	return &fakeSystem{
		nextHandleID:   1,
		handles:        make(map[int64]*handleRecord),
		liveGeneration: make(map[ProcessID]uint64),
		gates: map[Checkpoint]*fakeGate{
			CheckpointAfterSubmit:    newFakeGate(),
			CheckpointBeforeOpen:     newFakeGate(),
			CheckpointBeforeInject:   newFakeGate(),
			CheckpointBeforeResponse: newFakeGate(),
		},
		callCounts: make(map[string]int),
	}
}

func (f *fakeSystem) setLive(ref ProcessRef) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.liveGeneration[ref.PID] = ref.Generation
}

func (f *fakeSystem) removeLive(pid ProcessID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.liveGeneration, pid)
}

func (f *fakeSystem) gate(point Checkpoint) *fakeGate {
	return f.gates[point]
}

func (f *fakeSystem) record(call string) {
	f.callCounts[call]++
}

func (f *fakeSystem) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCounts[call]
}

func (f *fakeSystem) recordOf(handleID int64) *handleRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.handles[handleID]
	if record == nil {
		return nil
	}
	copy := *record
	return &copy
}

func (f *fakeSystem) Checkpoint(point Checkpoint) {
	f.gate(point).wait()
}

type fakeHandle struct {
	id  int64
	sys *fakeSystem
}

func (h *fakeHandle) ID() int64 { return h.id }

func (f *fakeSystem) Open(pid ProcessID, path []byte) (Handle, error) {
	f.mu.Lock()
	if f.openErr != nil {
		err := f.openErr
		f.record("open")
		f.mu.Unlock()
		return nil, err
	}
	id := f.nextHandleID
	f.nextHandleID++
	f.handles[id] = &handleRecord{id: id}
	f.record("open")
	f.mu.Unlock()
	return &fakeHandle{id: id, sys: f}, nil
}

func (f *fakeSystem) InjectHandle(ref ProcessRef, handle Handle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("inject")

	record := f.handles[handle.ID()]
	if record == nil {
		return ErrRequestInvalid
	}
	if record.closed {
		f.violations = append(f.violations, "inject into closed handle")
		return ErrHandleAlreadyTransferred
	}
	if record.transferred {
		f.violations = append(f.violations, "second injection of the same handle")
		return ErrHandleAlreadyTransferred
	}
	if generation, ok := f.liveGeneration[ref.PID]; !ok || generation != ref.Generation {
		f.violations = append(f.violations, "injection into wrong process instance")
		return ErrStaleGeneration
	}
	if f.injectErr != nil {
		return f.injectErr
	}
	record.transferred = true
	record.injectedRef = ref
	return nil
}

func (f *fakeSystem) SendResponse(ref ProcessRef, requestID RequestID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("response")

	if generation, ok := f.liveGeneration[ref.PID]; !ok || generation != ref.Generation {
		return ErrStaleGeneration
	}
	if f.responseErr != nil {
		return f.responseErr
	}
	return nil
}

func (f *fakeSystem) Close(handle Handle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("close")

	record := f.handles[handle.ID()]
	if record == nil {
		return ErrRequestInvalid
	}
	if record.transferred {
		// A real implementation must no-op here: closing a descriptor number
		// that now belongs to the target could close an unrelated object. The
		// fake surfaces the attempt as a violation so tests can catch it.
		f.violations = append(f.violations, "close of transferred handle")
		return ErrHandleAlreadyTransferred
	}
	record.closed = true
	return nil
}

func (f *fakeSystem) violationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.violations)
}

func (f *fakeSystem) violationText() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.violations))
	copy(out, f.violations)
	return out
}
