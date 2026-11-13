package netsec

import (
	"errors"
	"fmt"
	"sort"
)

const (
	// CloseRangeUnshare asks close_range to unshare the descriptor table before
	// applying the range. A private table cannot affect other threads.
	CloseRangeUnshare uint32 = 1 << 1

	// CloseRangeCLOEXEC marks descriptors in the range close-on-exec instead of
	// closing them immediately.
	CloseRangeCLOEXEC uint32 = 1 << 2

	// CloseRangeEnd is UINT_MAX. As the last argument to close_range it means
	// "through the end of the descriptor table".
	CloseRangeEnd uint32 = ^uint32(0)
)

var (
	// ErrInvalidRange reports a call whose first descriptor is greater than its
	// last descriptor.
	ErrInvalidRange = errors.New("close_range: first is greater than last")

	// ErrUnsupportedFlags reports flags outside the close_range flags modeled
	// by this package.
	ErrUnsupportedFlags = errors.New("close_range: unsupported flags")
)

const knownCloseRangeFlags = CloseRangeUnshare | CloseRangeCLOEXEC

// Descriptor is one entry in the descriptor table observed before exec.
type Descriptor struct {
	// FD is the descriptor number.
	FD uint32

	// CloseOnExec records whether the descriptor already has O_CLOEXEC set.
	CloseOnExec bool

	// Whitelist marks a descriptor that the child is intentionally allowed to
	// inherit.
	Whitelist bool
}

// CloseRangeCall is one close_range(first, last, flags) invocation.
//
// Both range endpoints are inclusive. First must not be greater than Last.
type CloseRangeCall struct {
	First uint32
	Last  uint32
	Flags uint32
}

// DescriptorTable is the descriptor table visible to the launcher when it
// constructs its close_range plan. Shared means the table is shared with
// another thread.
type DescriptorTable struct {
	Descriptors []Descriptor
	Shared      bool
}

// SharedTableWarning records a closing call that was applied without
// CLOSE_RANGE_UNSHARE while the descriptor table was shared with other
// threads. Such a call changes the table observed by those threads.
type SharedTableWarning struct {
	// CallIndex is the zero-based index of the offending call.
	CallIndex int

	// Call is the offending invocation.
	Call CloseRangeCall
}

// Report is the result of simulating a close_range sequence.
type Report struct {
	// Leaked contains non-whitelisted descriptors that are still present with
	// O_CLOEXEC clear when exec is attempted.
	Leaked []Descriptor

	// BeforeExec is the descriptor table after the simulated calls and before
	// exec processes O_CLOEXEC. It is useful for checking that CLOEXEC-only
	// calls did not close descriptors immediately.
	BeforeExec []Descriptor

	// Warnings contains every closing call that could affect another thread
	// through a shared descriptor table.
	Warnings []SharedTableWarning
}

// Simulate applies calls to table in order and reports the descriptors that
// would leak across exec. The input table and calls are not modified.
//
// Ranges are inclusive. First greater than Last is invalid. Last may be
// CloseRangeEnd (UINT_MAX) to cover every descriptor through the end of the
// table. A call with CloseRangeCLOEXEC only sets the close-on-exec bit; it
// does not remove descriptors from BeforeExec.
//
// A closing call on a shared table without CloseRangeUnshare is reported in
// Warnings. An unshare takes effect before the rest of that call and remains
// in effect for later calls in the plan.
func Simulate(table DescriptorTable, calls []CloseRangeCall) (Report, error) {
	current := cloneAndSort(table.Descriptors)
	shared := table.Shared
	var warnings []SharedTableWarning

	for i, call := range calls {
		if call.First > call.Last {
			return Report{}, fmt.Errorf("call %d: %w: %d > %d", i, ErrInvalidRange, call.First, call.Last)
		}
		if call.Flags&^knownCloseRangeFlags != 0 {
			return Report{}, fmt.Errorf("call %d: %w: %#x", i, ErrUnsupportedFlags, call.Flags)
		}

		if call.Flags&CloseRangeUnshare != 0 {
			shared = false
		}

		if call.Flags&CloseRangeCLOEXEC != 0 {
			for j := range current {
				if inRange(current[j].FD, call) {
					current[j].CloseOnExec = true
				}
			}
			continue
		}

		if shared {
			warnings = append(warnings, SharedTableWarning{
				CallIndex: i,
				Call:      call,
			})
		}
		current = removeRange(current, call)
	}

	leaked := make([]Descriptor, 0)
	for _, descriptor := range current {
		if !descriptor.Whitelist && !descriptor.CloseOnExec {
			leaked = append(leaked, descriptor)
		}
	}

	return Report{
		Leaked:     leaked,
		BeforeExec: current,
		Warnings:   warnings,
	}, nil
}

// MinimalCleanup returns a minimum-length sequence of closing calls that
// removes every non-whitelisted descriptor in table while preserving every
// whitelisted descriptor.
//
// The planner groups non-whitelisted descriptors across unallocated descriptor
// numbers, but starts a new call whenever a whitelisted descriptor lies
// between two groups. Consequently, holes do not add calls and whitelisted
// descriptors are never included in a closing range.
//
// If the table is shared, the first call carries CloseRangeUnshare. That makes
// the rest of the plan operate on the launcher's private table.
func MinimalCleanup(table DescriptorTable) []CloseRangeCall {
	whitelist := make(map[uint32]struct{})
	for _, descriptor := range table.Descriptors {
		if descriptor.Whitelist {
			whitelist[descriptor.FD] = struct{}{}
		}
	}

	nonWhitelist := make(map[uint32]struct{})
	for _, descriptor := range table.Descriptors {
		if descriptor.Whitelist {
			continue
		}
		if _, protected := whitelist[descriptor.FD]; !protected {
			nonWhitelist[descriptor.FD] = struct{}{}
		}
	}

	whitelistFDs := sortedFDs(whitelist)
	nonWhitelistFDs := sortedFDs(nonWhitelist)
	if len(nonWhitelistFDs) == 0 {
		return nil
	}

	calls := make([]CloseRangeCall, 0, 1)
	for _, fd := range nonWhitelistFDs {
		last := len(calls) - 1
		if last < 0 || whitelistBetween(whitelistFDs, calls[last].Last, fd) {
			calls = append(calls, CloseRangeCall{First: fd, Last: fd})
			continue
		}
		calls[last].Last = fd
	}

	if table.Shared {
		calls[0].Flags |= CloseRangeUnshare
	}
	return calls
}

func cloneAndSort(descriptors []Descriptor) []Descriptor {
	out := append([]Descriptor(nil), descriptors...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].FD < out[j].FD
	})
	return out
}

func inRange(fd uint32, call CloseRangeCall) bool {
	return call.First <= fd && fd <= call.Last
}

func removeRange(descriptors []Descriptor, call CloseRangeCall) []Descriptor {
	out := descriptors[:0]
	for _, descriptor := range descriptors {
		if !inRange(descriptor.FD, call) {
			out = append(out, descriptor)
		}
	}
	return out
}

func sortedFDs(set map[uint32]struct{}) []uint32 {
	fds := make([]uint32, 0, len(set))
	for fd := range set {
		fds = append(fds, fd)
	}
	sort.Slice(fds, func(i, j int) bool {
		return fds[i] < fds[j]
	})
	return fds
}

func whitelistBetween(whitelist []uint32, after, before uint32) bool {
	i := sort.Search(len(whitelist), func(i int) bool {
		return whitelist[i] > after
	})
	return i < len(whitelist) && whitelist[i] < before
}
