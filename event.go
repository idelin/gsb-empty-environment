package netsec

import (
	"fmt"
	"time"
)

// ThreadID identifies the JVM or native thread that emitted a trace event.
// It is deliberately a string so that native thread IDs, goroutine labels,
// and stable test names can all be used without lossy conversion.
type ThreadID string

// ArrayID identifies a JNI primitive array. Integrators may use a jobject
// handle formatted as a string or a stable application-level identifier.
type ArrayID string

// ReleaseMode is the mode passed to ReleasePrimitiveArrayCritical.
type ReleaseMode int

const (
	// ReleaseCopyBack copies changes back to the Java array and releases the
	// critical region. This is JNI mode 0.
	ReleaseCopyBack ReleaseMode = 0
	// ReleaseCommit copies changes back but keeps the critical region pinned.
	// This is JNI_COMMIT. A later Release*Critical call is still required.
	ReleaseCommit ReleaseMode = 1
	// ReleaseAbort releases the critical region without copying changes back.
	// This is JNI_ABORT.
	ReleaseAbort ReleaseMode = 2
)

const (
	// JNICopyBack, JNICommit, and JNIAbort are aliases using the JNI names.
	JNICopyBack = ReleaseCopyBack
	JNICommit   = ReleaseCommit
	JNIAbort    = ReleaseAbort
)

// String returns the JNI name of the release mode.
func (m ReleaseMode) String() string {
	switch m {
	case ReleaseCopyBack:
		return "copy_back"
	case ReleaseCommit:
		return "JNI_COMMIT"
	case ReleaseAbort:
		return "JNI_ABORT"
	default:
		return fmt.Sprintf("ReleaseMode(%d)", int(m))
	}
}

// EventKind identifies the kind of event observed through a JNI trace.
type EventKind string

const (
	// EventGetCritical records GetPrimitiveArrayCritical or another
	// Get*Critical call.
	EventGetCritical EventKind = "get_critical"
	// EventReleaseCritical records ReleasePrimitiveArrayCritical or another
	// Release*Critical call.
	EventReleaseCritical EventKind = "release_critical"
	// EventJNICall records a JNI function invocation other than the
	// Get*Critical and Release*Critical events above.
	EventJNICall EventKind = "jni_call"
	// EventBlockingCall records a call known to block, such as a monitor
	// wait, a GC wait, or an I/O operation.
	EventBlockingCall EventKind = "blocking_call"
	// EventPointerRead records bytes read through a pointer returned by
	// GetPrimitiveArrayCritical.
	EventPointerRead EventKind = "pointer_read"
	// EventPointerWrite records bytes written through a pointer returned by
	// GetPrimitiveArrayCritical.
	EventPointerWrite EventKind = "pointer_write"
	// EventThreadExit records native thread termination.
	EventThreadExit EventKind = "thread_exit"
)

// Event is the normalized event delivered to trace subscribers and accepted
// by Detector.Observe.
//
// Get and Release events carry the array identity and length. ArrayLength is
// measured in elements and ElementSize is the size of one element in bytes;
// an ElementSize of zero or less is treated as one byte. Pointer read and
// write events carry Address and Length in bytes. The byte count of a pointer
// access is called Length, while an array's element count is called
// ArrayLength.
type Event struct {
	// Seq is assigned by the detector. A value supplied by a trace producer
	// is overwritten.
	Seq uint64
	// Time is assigned by the detector when it is zero.
	Time time.Time
	// Kind identifies the event.
	Kind EventKind
	// Thread is the thread that performed the operation.
	Thread ThreadID
	// Array identifies the JNI array for Get, Release, and, when known,
	// pointer accesses.
	Array ArrayID
	// ArrayLength is the number of elements in the array.
	ArrayLength int
	// ElementSize is the number of bytes in one array element. Zero and
	// negative values are normalized to one.
	ElementSize int
	// Address is the base pointer for Get, the released pointer for Release,
	// and the first accessed byte for pointer reads and writes.
	Address uintptr
	// Length is the number of bytes read or written. It is not used for Get
	// and Release; use ArrayLength for those events.
	Length int
	// Mode is the release mode for Release events.
	Mode ReleaseMode
	// Function is the JNI function name for JNI and blocking-call events.
	Function string
	// Duration is the observed duration of a blocking call, when available.
	Duration time.Duration
	// CriticalDepth is set by the detector to the number of critical regions
	// active on Thread after the event has been applied.
	CriticalDepth int
}

// ArrayBytes returns the size of the array in bytes.
func (e Event) ArrayBytes() int {
	elementSize := e.ElementSize
	if elementSize <= 0 {
		elementSize = 1
	}
	span := byteSpan(e.ArrayLength, elementSize)
	if span > uint64(maxInt) {
		return maxInt
	}
	return int(span)
}

// FindingKind identifies a violation or warning produced by the detector.
type FindingKind string

const (
	// FindingJNICallInCritical reports a JNI call inside a critical region
	// other than Release*Critical or a nested Get*Critical.
	FindingJNICallInCritical FindingKind = "jni_call_in_critical"
	// FindingBlockingCallInCritical reports a potentially blocking call while
	// a critical region is active.
	FindingBlockingCallInCritical FindingKind = "blocking_call_in_critical"
	// FindingOutOfBoundsWrite reports a write that is not fully contained in
	// the array's byte range.
	FindingOutOfBoundsWrite FindingKind = "out_of_bounds_write"
	// FindingOutOfBoundsRead reports a read that is not fully contained in
	// the array's byte range.
	FindingOutOfBoundsRead FindingKind = "out_of_bounds_read"
	// FindingUseAfterRelease reports a pointer access after the array has
	// been released.
	FindingUseAfterRelease FindingKind = "use_after_release"
	// FindingDuplicateRelease reports a second release without an intervening
	// Get*Critical.
	FindingDuplicateRelease FindingKind = "duplicate_release"
	// FindingUnreleasedAtThreadExit reports a critical region that was still
	// active when its thread exited.
	FindingUnreleasedAtThreadExit FindingKind = "unreleased_at_thread_exit"
	// FindingAbortedWritesDiscarded reports writes that JNI_ABORT discarded.
	// This is an informational warning, not a critical-section violation.
	FindingAbortedWritesDiscarded FindingKind = "aborted_writes_discarded"
	// FindingReleaseWithoutGet reports a release with no matching Get.
	FindingReleaseWithoutGet FindingKind = "release_without_get"
)

// Severity classifies a finding.
type Severity string

const (
	// SeverityViolation marks a JNI critical-section rule violation.
	SeverityViolation Severity = "violation"
	// SeverityWarning marks a potentially surprising but legal outcome.
	SeverityWarning Severity = "warning"
)

// Finding describes one detected problem. Pointer is the base pointer of the
// affected critical region. Offset is the accessed address relative to that
// base and is meaningful for pointer-access and use-after-release findings.
type Finding struct {
	Kind         FindingKind
	Severity     Severity
	Thread       ThreadID
	Array        ArrayID
	Function     string
	Address      uintptr
	Length       int
	ArrayLength  int
	ElementSize  int
	Pointer      uintptr
	Offset       int64
	Mode         ReleaseMode
	GetSeq       uint64
	ReleaseSeq   uint64
	WriteCount   int
	BytesWritten int64
	Message      string
	EventSeq     uint64
	At           time.Time
}

// Region is a snapshot of an active critical region.
type Region struct {
	Thread      ThreadID
	Array       ArrayID
	Pointer     uintptr
	Length      int
	ElementSize int
	GetSeq      uint64
	Depth       int
}

// ByteLength returns the size of the active region in bytes.
func (r Region) ByteLength() int {
	elementSize := r.ElementSize
	if elementSize <= 0 {
		elementSize = 1
	}
	span := byteSpan(r.Length, elementSize)
	if span > uint64(maxInt) {
		return maxInt
	}
	return int(span)
}

const maxInt = int(^uint(0) >> 1)

func byteSpan(length, elementSize int) uint64 {
	if length <= 0 || elementSize <= 0 {
		return 0
	}
	elems := uint64(length)
	size := uint64(elementSize)
	if elems > ^uint64(0)/size {
		return ^uint64(0)
	}
	return elems * size
}
