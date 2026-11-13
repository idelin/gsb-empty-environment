package netsec

// RejectReason explains why an unlink was refused. A non-empty reason means
// the precheck failed before the first write.
type RejectReason string

const (
	RejectNone             RejectReason = ""
	RejectInvalidVersion   RejectReason = "invalid unlink version"
	RejectInvalidHeader    RejectReason = "block header is not an in-range 4-word-aligned address"
	RejectOutOfBounds      RejectReason = "prev/next field addresses are outside 0..63"
	RejectBacklinkMismatch RejectReason = "next.prev or prev.next does not point to the current block"
	RejectUnregisteredFree RejectReason = "one or both neighbours are not registered free block headers"
)

// ReplayResult is an independent execution of one (prev, next) candidate.
//
// Initial is the caller's memory before the attacker's permitted header edits.
// Before is the snapshot immediately after those fields have been set but
// before the unlink starts. Setup writes to the two permitted header fields
// are deliberately not part of Trace. If Accepted is false, After is exactly
// Before: no unlink read or write changed memory.
type ReplayResult struct {
	Version  Version
	Header   uint32
	Prev     uint32
	Next     uint32
	Accepted bool
	Reason   RejectReason
	Initial  Memory
	Before   Memory
	After    Memory
	Trace    []Access
}

// ChangedWords returns the addresses whose values differ between Before and
// After, in ascending address order.
func (r ReplayResult) ChangedWords() []uint32 {
	var changed []uint32
	for addr := uint32(0); addr < WordCount; addr++ {
		if r.Before[addr] != r.After[addr] {
			changed = append(changed, addr)
		}
	}
	return changed
}

func headerValid(header uint32) bool {
	return header%HeaderAlign == 0 && uint64(header)+1 < WordCount
}

func fieldAddressesValid(prev, next uint32) bool {
	// Use 64-bit arithmetic so prev == ^uint32(0) cannot wrap to address 0.
	return uint64(prev)+1 < WordCount && uint64(next) < WordCount
}

// Replay executes one unlink candidate using VersionA, VersionB, or VersionC.
//
// The attacker is represented by writing prev to Memory[header+PrevField] and
// next to Memory[header+NextField] before the precheck. The unlink itself then
// reads the two header fields (in that order), performs any version-specific
// precheck reads, and only after every check succeeds executes:
//
//	next.prev = prev   // m[next] = prev
//	prev.next = next   // m[prev+1] = next
//
// If the two write addresses alias, the second write wins.
func Replay(mem Memory, header, prev, next uint32, version Version, freeHeaders HeaderSet) ReplayResult {
	result := ReplayResult{
		Version: version,
		Header:  header,
		Prev:    prev,
		Next:    next,
		Initial: mem,
		Before:  mem,
		After:   mem,
	}

	if !version.Valid() {
		result.Reason = RejectInvalidVersion
		return result
	}
	if !headerValid(header) {
		result.Reason = RejectInvalidHeader
		return result
	}

	baseline := mem
	baseline[header+PrevField] = prev
	baseline[header+NextField] = next
	result.Before = baseline
	result.After = baseline

	// The unlink reads the attacker-controlled header fields first.
	loadedPrev := baseline[header+PrevField]
	loadedNext := baseline[header+NextField]
	result.Trace = append(result.Trace,
		Access{Kind: Read, Addr: header + PrevField, Value: loadedPrev},
		Access{Kind: Read, Addr: header + NextField, Value: loadedNext},
	)

	// Every address touched by the two writes (and, for B/C, by their
	// backlink reads) must be in range. This check is complete before any
	// write is executed.
	if !fieldAddressesValid(loadedPrev, loadedNext) {
		result.Reason = RejectOutOfBounds
		return result
	}

	if version >= VersionB {
		// next.prev must point back to the current block, then prev.next
		// must point back to it. Both reads are observable even if the
		// first one already fails.
		nextPrev := baseline[loadedNext]
		result.Trace = append(result.Trace,
			Access{Kind: Read, Addr: loadedNext, Value: nextPrev},
		)
		prevNext := baseline[loadedPrev+1]
		result.Trace = append(result.Trace,
			Access{Kind: Read, Addr: loadedPrev + 1, Value: prevNext},
		)
		if nextPrev != header || prevNext != header {
			result.Reason = RejectBacklinkMismatch
			return result
		}
	}

	if version == VersionC {
		if !freeHeaders.Contains(loadedPrev) || !freeHeaders.Contains(loadedNext) {
			result.Reason = RejectUnregisteredFree
			return result
		}
	}

	after := baseline
	after[loadedNext] = loadedPrev
	result.Trace = append(result.Trace,
		Access{Kind: Write, Addr: loadedNext, Value: loadedPrev},
	)
	after[loadedPrev+1] = loadedNext
	result.Trace = append(result.Trace,
		Access{Kind: Write, Addr: loadedPrev + 1, Value: loadedNext},
	)

	result.Accepted = true
	result.After = after
	return result
}

// VersionComparison holds three independent replays of the same candidate.
type VersionComparison struct {
	A ReplayResult
	B ReplayResult
	C ReplayResult
}

// ReplayVersions runs the same candidate independently under A, B, and C.
func ReplayVersions(mem Memory, header, prev, next uint32, freeHeaders HeaderSet) VersionComparison {
	return VersionComparison{
		A: Replay(mem, header, prev, next, VersionA, freeHeaders),
		B: Replay(mem, header, prev, next, VersionB, freeHeaders),
		C: Replay(mem, header, prev, next, VersionC, freeHeaders),
	}
}
