package lsadb

import "testing"

func TestCompareSequence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		a, b int32
		want int
	}{
		{"equal", 5, 5, 0},
		{"normal newer", 10, 5, 1},
		{"normal older", 5, 10, -1},
		{"initial equals initial", InitSequenceNumber, InitSequenceNumber, 0},
		{"max equals max", MaxSequenceNumber, MaxSequenceNumber, 0},
		// RFC 2328 section 12.1.2: after MaxSequenceNumber the LSA wraps to
		// InitSequenceNumber, so the wrapped number must read as newer.
		{"wrap initial newer than max", InitSequenceNumber, MaxSequenceNumber, 1},
		{"wrap max older than initial", MaxSequenceNumber, InitSequenceNumber, -1},
		{"wrap mid-space newer", int32(-0x7ffffffb), MaxSequenceNumber, 1}, // 0x80000005
		{"wrap mid-space older", MaxSequenceNumber, int32(-0x7ffffffb), -1},
		{"small forward step across zero", int32(0x00000005), int32(0x00000002), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CompareSequence(tc.a, tc.b); got != tc.want {
				t.Fatalf("CompareSequence(%#x, %#x) = %d, want %d",
					uint32(tc.a), uint32(tc.b), got, tc.want)
			}
		})
	}
}

func TestSeqHex(t *testing.T) {
	t.Parallel()

	if got := seqHex(InitSequenceNumber); got != 0x80000001 {
		t.Fatalf("seqHex(init) = %#x, want 0x80000001", got)
	}
}

func TestNextSequence(t *testing.T) {
	t.Parallel()

	if next, flush := NextSequence(5); next != 6 || flush {
		t.Fatalf("NextSequence(5) = (%#x, %v), want (6, false)", uint32(next), flush)
	}
	if next, flush := NextSequence(InitSequenceNumber); next != InitSequenceNumber+1 || flush {
		t.Fatalf("NextSequence(init) = (%#x, %v), want (init+1, false)", uint32(next), flush)
	}
	next, flush := NextSequence(MaxSequenceNumber)
	if next != InitSequenceNumber || !flush {
		t.Fatalf("NextSequence(max) = (%#x, %v), want (init, true)", uint32(next), flush)
	}
}
