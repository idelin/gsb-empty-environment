package lsadb

// CompareSequence compares two signed 32-bit LS sequence numbers using the
// circular sequence space of RFC 2328 section 12.1.2 (see also section 13
// step 5a). It returns 1 when a is newer than b, -1 when a is older than b,
// and 0 when they are equal.
//
// Sequence numbers run from 0x80000001 up to 0x7fffffff; an LSA reaching the
// maximum is MaxAge-flushed and re-originated at 0x80000001, so a freshly
// wrapped initial sequence number must compare as newer than the maximum.
func CompareSequence(a, b int32) int {
	if a == b {
		return 0
	}
	// Unsigned modular forward distance from b to a. A distance inside the
	// first half of the 32-bit space means a is forward (newer) of b.
	delta := uint32(a) - uint32(b)
	if delta < 1<<31 {
		return 1
	}
	return -1
}

// NextSequence returns the sequence number to use when re-originating an LSA
// whose current sequence number is cur, following RFC 2328 section 12.1.4.
// When cur is MaxSequenceNumber the number space is exhausted: mustFlush is
// true and the caller must first MaxAge-flush the current instance before
// originating the returned InitSequenceNumber instance.
func NextSequence(cur int32) (next int32, mustFlush bool) {
	if cur >= MaxSequenceNumber {
		return InitSequenceNumber, true
	}
	return cur + 1, false
}

// seqHex renders a signed sequence number as its unsigned 32-bit hex value
// (for example InitSequenceNumber renders as 0x80000001). A variable hop is
// required because a negative constant cannot be converted to uint32
// directly.
func seqHex(seq int32) uint32 {
	v := seq
	return uint32(v)
}
