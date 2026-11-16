package netsec

import (
	"fmt"
	"time"
)

// Severity classifies a finding.
type Severity uint8

const (
	SeverityInfo Severity = iota
	SeverityWarning
	SeverityViolation
)

// String returns the severity name.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityViolation:
		return "violation"
	default:
		return fmt.Sprintf("Severity(%d)", uint8(s))
	}
}

// FindingKind identifies a detected critical-section problem.
type FindingKind uint8

const (
	FindingUnknown FindingKind = iota

	// FindingJNICallInCritical is reported when native code invokes a JNI
	// function other than Get/Release...Critical while holding a critical
	// array pointer.
	FindingJNICallInCritical

	// FindingBlockingInCritical is reported when native code may block while
	// holding a critical array pointer.
	FindingBlockingInCritical

	// FindingOutOfBoundsWrite is reported when a write leaves the range of the
	// active critical array.
	FindingOutOfBoundsWrite

	// FindingOutOfBoundsRead is reported when a read leaves the range of the
	// active critical array.
	FindingOutOfBoundsRead

	// FindingUseAfterRelease is reported for a read or write overlapping an
	// array pointer that has already been released.
	FindingUseAfterRelease

	// FindingDuplicateRelease is reported when an array is released after its
	// critical section has already ended.
	FindingDuplicateRelease

	// FindingUnreleasedAtThreadEnd is reported for each critical pointer still
	// held when its thread terminates.
	FindingUnreleasedAtThreadEnd

	// FindingWritesDiscarded is a notice that a JNI_ABORT release discarded
	// writes made through the critical pointer.
	FindingWritesDiscarded

	// FindingInvalidRelease is reported when a release does not match any
	// active or previously released critical array.
	FindingInvalidRelease
)

// String returns a stable name for the finding kind.
func (k FindingKind) String() string {
	switch k {
	case FindingJNICallInCritical:
		return "JNICallInCritical"
	case FindingBlockingInCritical:
		return "BlockingInCritical"
	case FindingOutOfBoundsWrite:
		return "OutOfBoundsWrite"
	case FindingOutOfBoundsRead:
		return "OutOfBoundsRead"
	case FindingUseAfterRelease:
		return "UseAfterRelease"
	case FindingDuplicateRelease:
		return "DuplicateRelease"
	case FindingUnreleasedAtThreadEnd:
		return "UnreleasedAtThreadEnd"
	case FindingWritesDiscarded:
		return "WritesDiscarded"
	case FindingInvalidRelease:
		return "InvalidRelease"
	default:
		return fmt.Sprintf("FindingKind(%d)", uint8(k))
	}
}

// Finding describes one detected event or pattern.
//
// Length is the access length for pointer findings, and the array length for
// lifetime findings. Offset is the byte offset of an access relative to the
// array pointer; it may be negative when an access starts before the pointer.
// Writes and WriteBytes are populated for FindingWritesDiscarded.
type Finding struct {
	Kind        FindingKind
	Severity    Severity
	Thread      ThreadID
	Array       ArrayID
	Function    string
	Address     uintptr
	Length      int64
	Offset      int64
	ArrayLength int64
	ElementSize int64
	ArrayBytes  int64
	Mode        ReleaseMode
	Writes      int
	WriteBytes  int64
	Message     string
	At          time.Time
}

// String renders a compact diagnostic suitable for logs.
func (f Finding) String() string {
	return fmt.Sprintf("%s: %s (thread=%q array=%q)", f.Severity, f.Message, f.Thread, f.Array)
}
