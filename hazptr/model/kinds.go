// Package model is an importable, instrumented event model for validating
// hazard-pointer protection in a lock-free object pool.
//
// The model does NOT assume sequential consistency. Communication between
// threads is split into a staging side (writes, local hazard-pointer slots)
// and a published side (visible roots, visible hazard-pointer slots). Data
// becomes visible to another thread only when an explicit flush event
// (OpFlushHP / OpFlushRoot) is scheduled by the enumerator. Scheduling those
// flushes at different positions reproduces relaxed-memory interleavings
// such as late publication without modeling a particular CPU architecture.
package model

import "fmt"

// OpKind enumerates the explicit visibility events a thread can emit.
type OpKind int

const (
	OpInvalid OpKind = iota

	// OpSetRoot stages a root pointer locally; it is not visible until
	// OpFlushRoot fires.
	OpSetRoot
	// OpFlushRoot publishes every staged root write of the thread.
	OpFlushRoot
	// OpLoadRoot reads the currently visible root into a local register.
	OpLoadRoot

	// OpProtect stages a hazard pointer (thread-local slot); not visible
	// until OpFlushHP fires.
	OpProtect
	// OpFlushHP publishes every staged hazard pointer of the thread.
	OpFlushHP
	// OpClearHP stages a slot clear; visible after the next OpFlushHP.
	OpClearHP

	// OpVerifyRoot re-reads a root and compares it against a local
	// register. A mismatch aborts the reader (retry); only after a
	// consistent re-read is OpDeref permitted.
	OpVerifyRoot
	// OpDeref dereferences an object held in a local register.
	OpDeref

	// OpRetire moves a live object into the retired set.
	OpRetire
	// OpScan scans every thread's visible hazard slots and, under one
	// stable scan generation, frees retired objects that nobody protects.
	OpScan

	// OpAlloc obtains an address, optionally reusing a freed one.
	OpAlloc
)

// String returns a stable, human-readable op-code name.
func (k OpKind) String() string {
	switch k {
	case OpSetRoot:
		return "SetRoot"
	case OpFlushRoot:
		return "FlushRoot"
	case OpLoadRoot:
		return "LoadRoot"
	case OpProtect:
		return "Protect"
	case OpFlushHP:
		return "FlushHP"
	case OpClearHP:
		return "ClearHP"
	case OpVerifyRoot:
		return "VerifyRoot"
	case OpDeref:
		return "Deref"
	case OpRetire:
		return "Retire"
	case OpScan:
		return "Scan"
	case OpAlloc:
		return "Alloc"
	default:
		return fmt.Sprintf("OpKind(%d)", int(k))
	}
}

// Op is one instrumented event in a per-thread program. Programs are ordered
// per thread; the enumerator interleaves the per-thread sequences.
type Op struct {
	Kind   OpKind
	Thread int

	// Label is optional and shows up in rendered witnesses.
	Label string

	// Root identifies which root pointer the op touches.
	Root string
	// Addr is the address operand for SetRoot / Protect / Retire / Deref.
	Addr string
	// Reg is the local register operand for LoadRoot / VerifyRoot /
	// Deref / Alloc.
	Reg string
	// Slot is the per-thread hazard slot used by Protect / ClearHP.
	Slot int

	// ReuseAddr asks Alloc to return a specific freed address. Empty lets
	// the allocator pick any freed address, otherwise a fresh one.
	ReuseAddr string

	// IncludeThreads constrains a Scan to a subset of threads. A nil
	// slice means "every thread", which is the only spec-compliant
	// setting; non-nil subsets model partial scans.
	IncludeThreads []int
}

// ViolationKind classifies a witnessed protocol violation.
type ViolationKind int

const (
	ViolationNone ViolationKind = iota

	// ErrDerefBeforeConsistency: deref without a passing re-read.
	ErrDerefBeforeConsistency
	// ErrUseAfterFree: deref of a reclaimed (freed) object.
	ErrUseAfterFree
	// ErrPrematureReclaim: scan freed an address present in some
	// thread's *visible* hazard slot.
	ErrPrematureReclaim
	// ErrPartialScan: scan did not consult every thread's slots, and the
	// omitted view would have changed the free decision.
	ErrPartialScan
	// ErrAddressReuse: address-only protection re-protects a reused
	// address whose object identity changed (ABA).
	ErrAddressReuse
	// ErrInfeasible: model-level precondition failure, not a protocol
	// violation; used to prune impossible branches.
	ErrInfeasible
)

// String returns a stable violation code name.
func (k ViolationKind) String() string {
	switch k {
	case ErrDerefBeforeConsistency:
		return "DEREF_BEFORE_CONSISTENCY"
	case ErrUseAfterFree:
		return "USE_AFTER_FREE"
	case ErrPrematureReclaim:
		return "PREMATURE_RECLAIM"
	case ErrPartialScan:
		return "PARTIAL_SCAN"
	case ErrAddressReuse:
		return "ADDRESS_REUSE_ABA"
	case ErrInfeasible:
		return "INFEASIBLE"
	default:
		return fmt.Sprintf("ViolationKind(%d)", int(k))
	}
}

// Violation carries the structured details of one failed model step.
type Violation struct {
	Kind   ViolationKind
	Thread int
	Op     Op
	// Addr is the object address involved, when applicable.
	Addr string
	// Identity is the object generation involved, when applicable.
	Identity int
	// Detail is a short human-readable explanation.
	Detail string
}

// Error implements the error interface.
func (v Violation) Error() string {
	return fmt.Sprintf("%s at thread T%d op %s addr=%s gen=%d: %s",
		v.Kind, v.Thread, v.Op.Kind, v.Addr, v.Identity, v.Detail)
}

// Config selects which protocol obligations the machine enforces.
type Config struct {
	// RequireConsistentReread rejects OpDeref unless the register's root
	// was re-read and matched while protection was visible.
	RequireConsistentReread bool
	// TrackIdentity compares object generations in addition to addresses
	// (tagged hazard pointers). When false, plain address-only hazard
	// pointers are modeled and address reuse can yield ABA violations.
	TrackIdentity bool
	// EnforceFullScan requires every scan to consult all threads.
	EnforceFullScan bool
}

// DefaultConfig returns the strict, spec-compliant configuration.
func DefaultConfig() Config {
	return Config{
		RequireConsistentReread: true,
		TrackIdentity:           true,
		EnforceFullScan:         true,
	}
}
