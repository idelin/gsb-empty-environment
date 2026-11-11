package reassembly

// seqLess compares TCP sequence numbers using RFC 1982 serial number
// arithmetic, so it is correct across the 32-bit wrap boundary as long as
// the two values are within 2^31 of each other.
func seqLess(a, b uint32) bool { return int32(a-b) < 0 }

// seqLEQ reports whether a is before or equal to b in serial space.
func seqLEQ(a, b uint32) bool { return a == b || seqLess(a, b) }
