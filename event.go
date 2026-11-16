package netsec

import (
	"fmt"
	"strings"
	"time"
)

// ThreadID identifies the JNI thread that produced an event.
type ThreadID string

// ArrayID is the host's identity for a JNI array. It is normally the
// hexadecimal jobject value, but any stable per-array string is accepted.
type ArrayID string

// EventKind identifies the JNI trace record.
type EventKind uint8

const (
	EventUnknown EventKind = iota

	// EventGet records a successful GetPrimitiveArrayCritical (or
	// GetStringCritical) call. Address is the returned native pointer,
	// Length is the number of elements and ElementSize is the size of one
	// element in bytes. ElementSize defaults to one byte.
	EventGet

	// EventRelease records ReleasePrimitiveArrayCritical (or
	// ReleaseStringCritical). Address may be zero when Array is known.
	EventRelease

	// EventJNICall records any other JNI function call made by native code.
	EventJNICall

	// EventBlockingCall records a native call that may block, for example a
	// mutex wait, condition variable wait, sleep or synchronous I/O call.
	EventBlockingCall

	// EventRead records a read through a critical pointer. Address is the
	// first byte read and Length (or Size) is the number of bytes read.
	EventRead

	// EventWrite records a write through a critical pointer. Address is the
	// first byte written and Length (or Size) is the number of bytes written.
	EventWrite

	// EventThreadEnd records that a JNI thread is terminating.
	EventThreadEnd
)

// Short aliases for callers that prefer names matching the JNI API.
const (
	EventGetPrimitiveArrayCritical     = EventGet
	EventReleasePrimitiveArrayCritical = EventRelease
	EventOtherJNICall                  = EventJNICall
	EventBlocking                      = EventBlockingCall
	EventPointerRead                   = EventRead
	EventPointerWrite                  = EventWrite
)

// String returns a stable name for the event kind.
func (k EventKind) String() string {
	switch k {
	case EventGet:
		return "GetPrimitiveArrayCritical"
	case EventRelease:
		return "ReleasePrimitiveArrayCritical"
	case EventJNICall:
		return "JNICall"
	case EventBlockingCall:
		return "BlockingCall"
	case EventRead:
		return "PointerRead"
	case EventWrite:
		return "PointerWrite"
	case EventThreadEnd:
		return "ThreadEnd"
	default:
		return fmt.Sprintf("EventKind(%d)", uint8(k))
	}
}

// ReleaseMode is the mode passed to ReleasePrimitiveArrayCritical. The values
// match the JNI constants.
type ReleaseMode int32

const (
	// ReleaseModeDefault copies modified elements back and ends the critical
	// section (JNI's mode 0).
	ReleaseModeDefault ReleaseMode = 0

	// JNI_COMMIT copies modified elements back but leaves the critical
	// section active.
	JNI_COMMIT ReleaseMode = 1

	// JNI_ABORT ends the critical section without copying modifications back.
	JNI_ABORT ReleaseMode = 2
)

// Conventional aliases.
const (
	ReleaseModeCommit = JNI_COMMIT
	ReleaseModeAbort  = JNI_ABORT
	ModeCommit        = JNI_COMMIT
	ModeAbort         = JNI_ABORT
)

// String returns the JNI name of the release mode.
func (m ReleaseMode) String() string {
	switch m {
	case ReleaseModeDefault:
		return "0"
	case JNI_COMMIT:
		return "JNI_COMMIT"
	case JNI_ABORT:
		return "JNI_ABORT"
	default:
		return fmt.Sprintf("ReleaseMode(%d)", int32(m))
	}
}

// Event is one record from the host's JNI call trace.
//
// For EventGet, Length is measured in array elements. For EventRead and
// EventWrite, Length (or Size when non-zero) is measured in bytes. Array is
// optional on access events; when set, it disambiguates accesses that are far
// outside the array's own address range.
//
// At is optional. The detector fills it with the current time when zero.
type Event struct {
	Kind        EventKind
	Thread      ThreadID
	Array       ArrayID
	Function    string
	Address     uintptr
	Length      int64
	Size        int64
	ElementSize int64
	Mode        ReleaseMode
	At          time.Time
}

// GetEvent creates an EventGet for a byte array.
func GetEvent(thread ThreadID, array ArrayID, address uintptr, length int64) Event {
	return GetEventSized(thread, array, address, length, 1)
}

// GetEventSized creates an EventGet and records the element size in bytes.
func GetEventSized(thread ThreadID, array ArrayID, address uintptr, length, elementSize int64) Event {
	return Event{
		Kind:        EventGet,
		Thread:      thread,
		Array:       array,
		Address:     address,
		Length:      length,
		ElementSize: elementSize,
	}
}

// ReleaseEvent creates an EventRelease.
func ReleaseEvent(thread ThreadID, array ArrayID, address uintptr, length int64, mode ReleaseMode) Event {
	return Event{
		Kind:    EventRelease,
		Thread:  thread,
		Array:   array,
		Address: address,
		Length:  length,
		Mode:    mode,
	}
}

// JNICallEvent creates an EventJNICall.
func JNICallEvent(thread ThreadID, function string) Event {
	return Event{Kind: EventJNICall, Thread: thread, Function: function}
}

// BlockingCallEvent creates an EventBlockingCall.
func BlockingCallEvent(thread ThreadID, function string) Event {
	return Event{Kind: EventBlockingCall, Thread: thread, Function: function}
}

// ReadEvent creates an EventRead for length bytes starting at address.
func ReadEvent(thread ThreadID, address uintptr, length int64) Event {
	return Event{Kind: EventRead, Thread: thread, Address: address, Length: length}
}

// WriteEvent creates an EventWrite for length bytes starting at address.
func WriteEvent(thread ThreadID, address uintptr, length int64) Event {
	return Event{Kind: EventWrite, Thread: thread, Address: address, Length: length}
}

// ThreadEndEvent creates an EventThreadEnd.
func ThreadEndEvent(thread ThreadID) Event {
	return Event{Kind: EventThreadEnd, Thread: thread}
}

func (e Event) accessSize() int64 {
	if e.Size > 0 {
		return e.Size
	}
	if e.Length > 0 {
		return e.Length
	}
	return 0
}

func (e Event) elementSize() int64 {
	if e.ElementSize > 0 {
		return e.ElementSize
	}
	return 1
}

// isAllowedInCritical reports whether a function name is one of the JNI calls
// that the critical-section contract permits. State-changing Get/Release
// events must still use EventGet/EventRelease so the detector can track the
// pointer lifetime.
func isAllowedInCritical(function string) bool {
	name := strings.TrimSpace(function)
	if i := strings.IndexAny(name, "( \t"); i >= 0 {
		name = name[:i]
	}
	if name == "" {
		return false
	}
	return (strings.HasPrefix(name, "Get") || strings.HasPrefix(name, "Release")) &&
		strings.HasSuffix(name, "Critical")
}
