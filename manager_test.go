package netsec

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

const (
	testPID  ProcessID = 4242
	testReq  RequestID = 7
	testPath           = "/tmp/sandbox/secret.txt"
)

func newHarness(t *testing.T) (*Registry, *fakeSystem, *Manager) {
	t.Helper()
	registry := NewRegistry()
	sys := newFakeSystem()
	manager := NewManager(registry, sys)
	return registry, sys, manager
}

func runHandle(t *testing.T, manager *Manager, ref ProcessRef, requestID RequestID, path []byte) <-chan error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		errCh <- manager.Handle(ref, requestID, path)
	}()
	return errCh
}

func waitErr(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Handle did not finish when expected")
		return nil
	}
}

// TestHappyPath establishes the baseline: a request that never races with a
// restart delivers exactly one handle to exactly the requested process
// instance, and the proxy never closes it.
func TestHappyPath(t *testing.T) {
	registry, sys, manager := newHarness(t)
	ref := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(ref)
	sys.setLive(ref)

	if err := manager.Handle(ref, testReq, []byte(testPath)); err != nil {
		t.Fatalf("Handle: unexpected error %v", err)
	}

	if got := sys.count("open"); got != 1 {
		t.Fatalf("open calls = %d, want 1", got)
	}
	if got := sys.count("inject"); got != 1 {
		t.Fatalf("inject calls = %d, want 1", got)
	}
	if got := sys.count("close"); got != 0 {
		t.Fatalf("close calls = %d, want 0 (handle belongs to target)", got)
	}
	if got := sys.violationCount(); got != 0 {
		t.Fatalf("ownership violations: %v", sys.violationText())
	}
	record := sys.recordOf(1)
	if record == nil || !record.transferred || record.injectedRef != ref {
		t.Fatalf("handle not transferred to %+v: %+v", ref, record)
	}
	if registry.activeCount() != 0 {
		t.Fatalf("active requests = %d, want 0 after completion", registry.activeCount())
	}
}

// TestRestartBeforeExecution drives a restart into the window before the
// pre-execution gate. No syscall work happens, no handle exists, and the
// request is rejected as invalid.
func TestRestartBeforeExecution(t *testing.T) {
	registry, sys, manager := newHarness(t)
	oldRef := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(oldRef)
	sys.setLive(oldRef)

	sys.gate(CheckpointBeforeOpen).arm()
	errCh := runHandle(t, manager, oldRef, testReq, []byte(testPath))
	sys.gate(CheckpointBeforeOpen).waitReached()

	newRef := ProcessRef{PID: testPID, Generation: 2}
	registry.RestartProcess(testPID, 2)
	sys.setLive(newRef)

	sys.gate(CheckpointBeforeOpen).releaseOnce()
	if err := waitErr(t, errCh); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("err = %v, want ErrStaleGeneration", err)
	}

	if got := sys.count("open"); got != 0 {
		t.Fatalf("open calls = %d, want 0 (gate must block execution)", got)
	}
	if got := sys.count("inject"); got != 0 {
		t.Fatalf("inject calls = %d, want 0", got)
	}
	if got := sys.count("close"); got != 0 {
		t.Fatalf("close calls = %d, want 0 (no handle was opened)", got)
	}
}

// TestRestartAfterOpenBeforeDelivery drives a restart between Open and the
// pre-delivery gate. A proxy-owned handle exists at that point, so it must be
// closed immediately and must never reach the replacement process.
func TestRestartAfterOpenBeforeDelivery(t *testing.T) {
	registry, sys, manager := newHarness(t)
	oldRef := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(oldRef)
	sys.setLive(oldRef)

	sys.gate(CheckpointBeforeInject).arm()
	errCh := runHandle(t, manager, oldRef, testReq, []byte(testPath))
	sys.gate(CheckpointBeforeInject).waitReached()

	if got := sys.count("open"); got != 1 {
		t.Fatalf("open calls = %d, want 1 before restart", got)
	}

	newRef := ProcessRef{PID: testPID, Generation: 2}
	registry.RestartProcess(testPID, 2)
	sys.setLive(newRef)

	sys.gate(CheckpointBeforeInject).releaseOnce()
	err := waitErr(t, errCh)
	if !(errors.Is(err, ErrStaleGeneration) || errors.Is(err, ErrRequestInvalid)) {
		t.Fatalf("err = %v, want stale/invalid", err)
	}

	if got := sys.count("inject"); got != 0 {
		t.Fatalf("inject calls = %d, want 0 (gate must block delivery)", got)
	}
	if got := sys.count("close"); got != 1 {
		t.Fatalf("close calls = %d, want 1 (invalidated handle closed)", got)
	}
	record := sys.recordOf(1)
	if record == nil || !record.closed || record.transferred {
		t.Fatalf("handle must be closed and never transferred: %+v", record)
	}
	if got := sys.violationCount(); got != 0 {
		t.Fatalf("ownership violations: %v", sys.violationText())
	}
}

// TestRequestIDReuseAcrossRestart proves request IDs can be reused safely
// once the old process instance is gone. The old request's handle belongs to
// the dead instance; the new instance's identical request ID gets a brand new
// handle with exactly one transfer each.
func TestRequestIDReuseAcrossRestart(t *testing.T) {
	registry, sys, manager := newHarness(t)
	oldRef := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(oldRef)
	sys.setLive(oldRef)

	if err := manager.Handle(oldRef, testReq, []byte(testPath)); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	firstHandle := sys.recordOf(1)
	if firstHandle == nil || !firstHandle.transferred {
		t.Fatalf("first handle not transferred: %+v", firstHandle)
	}

	newRef := ProcessRef{PID: testPID, Generation: 2}
	registry.RestartProcess(testPID, 2)
	sys.setLive(newRef)

	if err := manager.Handle(newRef, testReq, []byte(testPath)); err != nil {
		t.Fatalf("reused-id Handle: %v", err)
	}

	if got := sys.count("open"); got != 2 {
		t.Fatalf("open calls = %d, want 2 (independent handles per instance)", got)
	}
	if got := sys.count("inject"); got != 2 {
		t.Fatalf("inject calls = %d, want 2", got)
	}
	if got := sys.count("close"); got != 0 {
		t.Fatalf("close calls = %d, want 0", got)
	}
	secondHandle := sys.recordOf(2)
	if secondHandle == nil || !secondHandle.transferred || secondHandle.injectedRef != newRef {
		t.Fatalf("second handle not bound to new instance %+v: %+v", newRef, secondHandle)
	}
	if firstHandle.injectedRef == secondHandle.injectedRef {
		t.Fatal("both handles injected into the same process instance")
	}
	if got := sys.violationCount(); got != 0 {
		t.Fatalf("ownership violations: %v", sys.violationText())
	}

	// While an instance is alive, the same ID must not be accepted twice.
	if err := manager.Handle(newRef, testReq, []byte(testPath)); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("duplicate err = %v, want ErrDuplicateRequest", err)
	}
}

// TestResponseFailureAfterInjection simulates a successful injection followed
// by a failed completion response. The handle has already changed owners, so
// the proxy must report ErrResponseFailed without closing, re-injecting, or
// otherwise touching the handle: ownership moves exactly once.
func TestResponseFailureAfterInjection(t *testing.T) {
	registry, sys, manager := newHarness(t)
	ref := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(ref)
	sys.setLive(ref)
	sys.responseErr = errors.New("reply channel closed")

	err := manager.Handle(ref, testReq, []byte(testPath))
	if !errors.Is(err, ErrResponseFailed) {
		t.Fatalf("err = %v, want ErrResponseFailed", err)
	}

	if got := sys.count("inject"); got != 1 {
		t.Fatalf("inject calls = %d, want 1", got)
	}
	if got := sys.count("close"); got != 0 {
		t.Fatalf("close calls = %d, want 0 (target owns the handle)", got)
	}
	record := sys.recordOf(1)
	if record == nil || !record.transferred || record.closed {
		t.Fatalf("handle must stay transferred and open: %+v", record)
	}
	if got := sys.violationCount(); got != 0 {
		t.Fatalf("ownership violations: %v", sys.violationText())
	}
}

// TestRestartBetweenInjectionAndResponse is the nastiest race: injection
// succeeds, then the process dies and a new instance takes over before the
// response is sent. The response fails, the handle remains with the original
// instance, and the new instance can neither receive nor close it.
func TestRestartBetweenInjectionAndResponse(t *testing.T) {
	registry, sys, manager := newHarness(t)
	oldRef := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(oldRef)
	sys.setLive(oldRef)

	sys.gate(CheckpointBeforeResponse).arm()
	errCh := runHandle(t, manager, oldRef, testReq, []byte(testPath))
	sys.gate(CheckpointBeforeResponse).waitReached()

	record := sys.recordOf(1)
	if record == nil || !record.transferred {
		t.Fatalf("handle should be transferred before response: %+v", record)
	}

	newRef := ProcessRef{PID: testPID, Generation: 2}
	registry.RestartProcess(testPID, 2)
	sys.setLive(newRef)

	sys.gate(CheckpointBeforeResponse).releaseOnce()
	if err := waitErr(t, errCh); !errors.Is(err, ErrResponseFailed) {
		t.Fatalf("err = %v, want ErrResponseFailed", err)
	}

	if got := sys.count("inject"); got != 1 {
		t.Fatalf("inject calls = %d, want 1 (no re-injection)", got)
	}
	if got := sys.count("close"); got != 0 {
		t.Fatalf("close calls = %d, want 0 (proxy relinquished ownership)", got)
	}
	record = sys.recordOf(1)
	if record.injectedRef != oldRef {
		t.Fatalf("handle moved instances: got %+v, want %+v", record.injectedRef, oldRef)
	}
	if got := sys.violationCount(); got != 0 {
		t.Fatalf("ownership violations: %v", sys.violationText())
	}

	// A fresh request from the new instance opens a separate handle and the
	// old one is never reused.
	if err := manager.Handle(newRef, testReq, []byte(testPath)); err != nil {
		t.Fatalf("new instance Handle: %v", err)
	}
	if got := sys.count("open"); got != 2 {
		t.Fatalf("open calls = %d, want 2", got)
	}
	if got := sys.recordOf(2).injectedRef; got != newRef {
		t.Fatalf("new handle ref = %+v, want %+v", got, newRef)
	}
}

// TestPathBytesAreDefensivelyCopied ensures later mutation of the caller's
// slice cannot change the path held by the registry.
func TestPathBytesAreDefensivelyCopied(t *testing.T) {
	registry := NewRegistry()
	ref := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(ref)

	original := []byte("/tmp/b")
	req, err := registry.Submit(ref, 8, original)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	original[0] = 'Y'
	if got := string(req.Path()); got != "/tmp/b" {
		t.Fatalf("stored path mutated through caller slice: %q", got)
	}
	returned := req.Path()
	returned[0] = 'Z'
	if got := string(req.Path()); got != "/tmp/b" {
		t.Fatalf("stored path mutated through returned slice: %q", got)
	}
}

// TestSubmitRejectsUnknownAndStaleProcesses checks the entry conditions.
func TestSubmitRejectsUnknownAndStaleProcesses(t *testing.T) {
	registry := NewRegistry()
	sys := newFakeSystem()
	manager := NewManager(registry, sys)
	ref := ProcessRef{PID: testPID, Generation: 1}

	if err := manager.Handle(ref, testReq, []byte(testPath)); !errors.Is(err, ErrUnknownProcess) {
		t.Fatalf("unknown process err = %v, want ErrUnknownProcess", err)
	}

	registry.StartProcess(ref)
	sys.setLive(ref)
	registry.RestartProcess(testPID, 2)
	if err := manager.Handle(ref, testReq, []byte(testPath)); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale generation err = %v, want ErrStaleGeneration", err)
	}
}

// TestHandleSingleTransferUnderConcurrency hammers one process with many
// concurrent requests and asserts every opened handle is either delivered
// exactly once or closed exactly once, never both and never twice.
func TestHandleSingleTransferUnderConcurrency(t *testing.T) {
	registry, sys, manager := newHarness(t)
	ref := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(ref)
	sys.setLive(ref)

	const workers = 50
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			path := []byte(fmt.Sprintf("/tmp/file-%d", i))
			if err := manager.Handle(ref, RequestID(100+i), path); err != nil {
				t.Errorf("worker %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if got := sys.count("open"); got != workers {
		t.Fatalf("open calls = %d, want %d", got, workers)
	}
	if got := sys.count("inject"); got != workers {
		t.Fatalf("inject calls = %d, want %d", got, workers)
	}
	if got := sys.count("close"); got != 0 {
		t.Fatalf("close calls = %d, want 0", got)
	}
	for id := int64(1); id <= workers; id++ {
		record := sys.recordOf(id)
		if record == nil || !record.transferred || record.closed {
			t.Fatalf("handle %d in wrong state: %+v", id, record)
		}
	}
	if got := sys.violationCount(); got != 0 {
		t.Fatalf("ownership violations: %v", sys.violationText())
	}
}

// TestOpenFailureClosesNothingAndFailsRequest covers a failed open syscall:
// no handle exists, nothing is injected, and the request terminally fails.
func TestOpenFailureClosesNothingAndFailsRequest(t *testing.T) {
	registry, sys, manager := newHarness(t)
	ref := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(ref)
	sys.setLive(ref)
	sys.openErr = errors.New("no such file")

	err := manager.Handle(ref, testReq, []byte(testPath))
	if err == nil {
		t.Fatal("expected open error, got nil")
	}
	if got := sys.count("inject"); got != 0 {
		t.Fatalf("inject calls = %d, want 0", got)
	}
	if got := sys.count("close"); got != 0 {
		t.Fatalf("close calls = %d, want 0 (no handle to close)", got)
	}
	if _, err := registry.Validate(ref, testReq); !errors.Is(err, ErrRequestInvalid) {
		t.Fatalf("validate after open failure: %v, want ErrRequestInvalid", err)
	}
}

// TestInjectionFailureClosesHandle covers an InjectHandle failure: the proxy
// retains ownership on the way out and must close the orphaned handle once.
func TestInjectionFailureClosesHandle(t *testing.T) {
	registry, sys, manager := newHarness(t)
	ref := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(ref)
	sys.setLive(ref)
	sys.injectErr = errors.New("process table slot gone")

	if err := manager.Handle(ref, testReq, []byte(testPath)); err == nil {
		t.Fatal("expected injection error, got nil")
	}
	if got := sys.count("close"); got != 1 {
		t.Fatalf("close calls = %d, want 1", got)
	}
	record := sys.recordOf(1)
	if record == nil || !record.closed || record.transferred {
		t.Fatalf("handle must be closed and not transferred: %+v", record)
	}
}

// TestProcessExitBeforeExecution covers process teardown (rather than
// restart) landing before the pre-execution gate: the request is rejected and
// no syscall work happens.
func TestProcessExitBeforeExecution(t *testing.T) {
	registry, sys, manager := newHarness(t)
	ref := ProcessRef{PID: testPID, Generation: 1}
	registry.StartProcess(ref)
	sys.setLive(ref)

	sys.gate(CheckpointAfterSubmit).arm()
	errCh := runHandle(t, manager, ref, testReq, []byte(testPath))
	sys.gate(CheckpointAfterSubmit).waitReached()

	registry.ExitProcess(testPID)
	sys.removeLive(testPID)

	sys.gate(CheckpointAfterSubmit).releaseOnce()
	if err := waitErr(t, errCh); !errors.Is(err, ErrUnknownProcess) {
		t.Fatalf("err = %v, want ErrUnknownProcess", err)
	}
	if got := sys.count("open"); got != 0 {
		t.Fatalf("open calls = %d, want 0", got)
	}
}
