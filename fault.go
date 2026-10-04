package waldb

import "sync"

// CrashPoint enumerates every injectable exit point, both in normal
// execution and during recovery. A crash at any of these points must
// leave the database recoverable.
type CrashPoint int

const (
	PtNone CrashPoint = iota
	// ---- normal execution ----
	PtUpdateLogged  // update record appended to log buffer, not yet durable
	PtPageDirtied   // page modified in buffer pool, not yet flushed
	PtCommitLogged  // commit record appended, not yet durable
	PtCommitFlushed // commit record durable, end not yet written
	PtEndLogged     // end record appended (maybe not durable)
	PtPageFlush     // while flushing dirty pages
	PtCheckpoint    // while writing a checkpoint
	PtAbortUndo     // abort undo: after CLR append, before page fix
	// ---- recovery ----
	PtRecAnalysis // after analysis, before redo
	PtRecRedo     // during the redo scan
	PtRecUndo     // recovery undo: after CLR append, before page fix
	PtRecEnd      // recovery finished, before returning
)

// ExecCrashPoints are the exit points of normal execution.
var ExecCrashPoints = []CrashPoint{
	PtUpdateLogged, PtPageDirtied, PtCommitLogged, PtCommitFlushed,
	PtEndLogged, PtPageFlush, PtCheckpoint, PtAbortUndo,
}

// RecoveryCrashPoints are the exit points of recovery itself.
var RecoveryCrashPoints = []CrashPoint{
	PtRecAnalysis, PtRecRedo, PtRecUndo, PtRecEnd,
}

func (p CrashPoint) String() string {
	switch p {
	case PtUpdateLogged:
		return "exec/update-logged"
	case PtPageDirtied:
		return "exec/page-dirtied"
	case PtCommitLogged:
		return "exec/commit-logged"
	case PtCommitFlushed:
		return "exec/commit-flushed"
	case PtEndLogged:
		return "exec/end-logged"
	case PtPageFlush:
		return "exec/page-flush"
	case PtCheckpoint:
		return "exec/checkpoint"
	case PtAbortUndo:
		return "exec/abort-undo"
	case PtRecAnalysis:
		return "recovery/analysis"
	case PtRecRedo:
		return "recovery/redo"
	case PtRecUndo:
		return "recovery/undo"
	case PtRecEnd:
		return "recovery/end"
	}
	return "none"
}

// crashPanic is panicked by the injector to simulate an abrupt crash.
type crashPanic struct{ pt CrashPoint }

// FaultInjector crashes the process at armed CrashPoints. It fires at
// most once per arm, so a test can crash exactly once and then let
// the code run to completion.
type FaultInjector struct {
	mu    sync.Mutex
	armed map[CrashPoint]bool
	fired bool
	// Hook runs on every hit (armed or not), before any panic. Tests
	// use it to force flushes at precise points, e.g. "flush the CLR
	// but crash before the page is modified".
	Hook func(pt CrashPoint)
}

func newFaultInjector() *FaultInjector {
	return &FaultInjector{armed: map[CrashPoint]bool{}}
}

func (f *FaultInjector) arm(pts ...CrashPoint) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range pts {
		f.armed[p] = true
	}
}

func (f *FaultInjector) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed = map[CrashPoint]bool{}
	f.fired = false
	f.Hook = nil
}

func (f *FaultInjector) hit(pt CrashPoint) {
	if f == nil {
		return
	}
	f.mu.Lock()
	fire := !f.fired && f.armed[pt]
	if fire {
		f.fired = true
	}
	hook := f.Hook
	f.mu.Unlock()
	if hook != nil {
		hook(pt)
	}
	if fire {
		panic(crashPanic{pt})
	}
}
