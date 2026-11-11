// Package crashfp builds stable crash fingerprints and groups crash reports
// into equivalence classes for a Go-based security test harness.
//
// A fingerprint is derived from fully normalized stack frames only: module
// digest, module-relative offset, symbolized frame metadata and the inline
// chain. Absolute addresses are removed using the module load map supplied by
// the caller, so address space layout randomization does not split one defect
// into several classes. Exception names are intentionally excluded from the
// fingerprint: the same exception name can originate from distinct defects,
// and distinct names may label the same defect.
//
// Frame ordering convention: Frames are ordered outermost call first, with the
// faulting (leaf) frame last. Go runtime stack traces print leaf first and must
// be reversed by the caller before normalization.
package crashfp

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// InlineFrame is one entry of the inline chain captured at a physical frame.
// Entry zero is the most deeply inlined call site; later entries move outward
// toward the physical function.
type InlineFrame struct {
	Function string
	File     string
	Line     int
}

// RawFrame is one physical stack frame before address normalization.
type RawFrame struct {
	// Module is the loaded module name; it must match a ModuleLoad.Name.
	Module string
	// Address is the absolute return address captured in the crash report.
	Address uint64
	// Inlines is the inline chain at Address, innermost call first.
	Inlines []InlineFrame
	// Function, File and Line describe the physical function at Address.
	Function string
	File     string
	Line     int
}

// ModuleLoad describes one loaded module image for one crash report.
type ModuleLoad struct {
	// Name identifies the module inside the report's frame list.
	Name string
	// Digest is a build-id or content digest shared across loads of the
	// same binary, even when it is loaded at different base addresses.
	Digest string
	// Base is the runtime load address used to derive relative offsets.
	Base uint64
}

// Frame is an address-independent stack frame.
type Frame struct {
	// ModuleDigest identifies the binary build rather than a load instance.
	ModuleDigest string
	// Offset is Address minus the module load base.
	Offset uint64
	// Function, File and Line describe the physical function.
	Function string
	File     string
	Line     int
	// Inlines is the normalized inline chain, innermost call first.
	Inlines []InlineFrame
}

// Stack is a normalized crash stack.
type Stack struct {
	// Frames are ordered outermost call first, faulting frame last.
	Frames []Frame
	// Truncated reports that the collector could not capture the full
	// stack. Truncated stacks never force a merge; they only participate
	// in candidate associations through compatible known suffixes.
	Truncated bool
}

// Report is one crash observation handed to the Classifier.
//
// Either provide Frames plus Modules and let the Classifier normalize them,
// or provide an already normalized Stack. When Stack is non-nil it takes
// precedence and Frames/Modules are ignored.
type Report struct {
	// ID is the caller-chosen unique, stable identifier of the report.
	ID string
	// Exception is the raw exception name, retained for display only.
	Exception string
	// Modules are the module loads captured alongside the crash.
	Modules []ModuleLoad
	// Frames are the raw physical frames, outermost call first.
	Frames []RawFrame
	// Stack is an optional pre-normalized stack.
	Stack *Stack
}

var (
	// ErrEmptyStack is returned when a report carries no frames.
	ErrEmptyStack = errors.New("crashfp: stack has no frames")
	// ErrUnknownModule is returned when a frame references a module that
	// has no matching ModuleLoad entry.
	ErrUnknownModule = errors.New("crashfp: frame references unknown module")
	// ErrAddressBeforeBase is returned when a frame address is lower than
	// its module load base, so no relative offset exists.
	ErrAddressBeforeBase = errors.New("crashfp: frame address precedes module base")
	// ErrInvalidReport is returned when required report fields are missing.
	ErrInvalidReport = errors.New("crashfp: invalid report")
	// ErrDuplicateReport is returned when a report ID is added twice.
	ErrDuplicateReport = errors.New("crashfp: duplicate report id")
)

// Normalize removes absolute addressing from raw frames using the module load
// map. The returned Stack is a deep copy and never aliases the input slices.
func Normalize(frames []RawFrame, modules []ModuleLoad) (*Stack, error) {
	if len(frames) == 0 {
		return nil, ErrEmptyStack
	}

	bases := make(map[string]ModuleLoad, len(modules))
	for _, module := range modules {
		bases[module.Name] = module
	}

	normalized := make([]Frame, 0, len(frames))
	for index, raw := range frames {
		module, ok := bases[raw.Module]
		if !ok {
			return nil, fmt.Errorf("%w: frame %d module %q", ErrUnknownModule, index, raw.Module)
		}
		if raw.Address < module.Base {
			return nil, fmt.Errorf("%w: frame %d address %#x base %#x",
				ErrAddressBeforeBase, index, raw.Address, module.Base)
		}

		frame := Frame{
			ModuleDigest: module.Digest,
			Offset:       raw.Address - module.Base,
			Function:     raw.Function,
			File:         raw.File,
			Line:         raw.Line,
			Inlines:      cloneInlines(raw.Inlines),
		}
		normalized = append(normalized, frame)
	}

	return &Stack{Frames: normalized}, nil
}

// Fingerprint returns the stable SHA-256 fingerprint of the normalized stack.
// The Truncated flag is deliberately excluded: truncation describes capture
// quality, not the defect identity, and must not change frame identity.
func (s *Stack) Fingerprint() string {
	var buffer bytes.Buffer

	putUint32(&buffer, uint32(len(s.Frames)))
	for _, frame := range s.Frames {
		putString(&buffer, frame.ModuleDigest)
		putUint64(&buffer, frame.Offset)
		putString(&buffer, frame.Function)
		putString(&buffer, frame.File)
		putInt64(&buffer, int64(frame.Line))

		putUint32(&buffer, uint32(len(frame.Inlines)))
		for _, inline := range frame.Inlines {
			putString(&buffer, inline.Function)
			putString(&buffer, inline.File)
			putInt64(&buffer, int64(inline.Line))
		}
	}

	sum := sha256.Sum256(buffer.Bytes())
	return fmt.Sprintf("%x", sum)
}

func cloneInlines(inlines []InlineFrame) []InlineFrame {
	if len(inlines) == 0 {
		return nil
	}
	clone := make([]InlineFrame, len(inlines))
	copy(clone, inlines)
	return clone
}

func cloneStack(stack *Stack) *Stack {
	frames := make([]Frame, len(stack.Frames))
	for index, frame := range stack.Frames {
		frames[index] = Frame{
			ModuleDigest: frame.ModuleDigest,
			Offset:       frame.Offset,
			Function:     frame.Function,
			File:         frame.File,
			Line:         frame.Line,
			Inlines:      cloneInlines(frame.Inlines),
		}
	}
	return &Stack{Frames: frames, Truncated: stack.Truncated}
}

func framesEqual(a, b []Frame) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if !frameEqual(a[index], b[index]) {
			return false
		}
	}
	return true
}

func frameEqual(a, b Frame) bool {
	if a.ModuleDigest != b.ModuleDigest || a.Offset != b.Offset ||
		a.Function != b.Function || a.File != b.File || a.Line != b.Line {
		return false
	}
	if len(a.Inlines) != len(b.Inlines) {
		return false
	}
	for index := range a.Inlines {
		if a.Inlines[index] != b.Inlines[index] {
			return false
		}
	}
	return true
}

// commonSuffixLength counts matching frames starting at the faulting end of
// each stack. The inline chain is part of frame identity.
func commonSuffixLength(a, b []Frame) int {
	matched := 0
	left, right := len(a)-1, len(b)-1
	for left >= 0 && right >= 0 && frameEqual(a[left], b[right]) {
		matched++
		left--
		right--
	}
	return matched
}

func putString(buffer *bytes.Buffer, value string) {
	putUint32(buffer, uint32(len(value)))
	buffer.WriteString(value)
}

func putUint32(buffer *bytes.Buffer, value uint32) {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], value)
	buffer.Write(raw[:])
}

func putUint64(buffer *bytes.Buffer, value uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	buffer.Write(raw[:])
}

func putInt64(buffer *bytes.Buffer, value int64) {
	putUint64(buffer, uint64(value))
}
