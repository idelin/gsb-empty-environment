package netsec

import "testing"

func TestDecodeFirstPacketStartsAtZero(t *testing.T) {
	// Before any authenticated packet expected is 0; the smallest legal
	// candidate for any truncated value is the truncated value itself.
	for width := Width1; width <= Width4; width++ {
		got, err := decodePacketNumber(1, width, 0)
		if err != nil || got != 1 {
			t.Fatalf("width %d first packet = (%d,%v), want 1", width, got, err)
		}
	}
}

func TestDecodeNearNumberSpaceCeiling(t *testing.T) {
	// expected is one beyond the highest legal number; a truncated value that
	// would wrap past MaxPacketNumber must settle on the in-range candidate.
	expected := MaxPacketNumber
	got, err := decodePacketNumber(MaxPacketNumber&0xff, Width1, expected)
	if err != nil {
		t.Fatalf("decode near ceiling: %v", err)
	}
	if got > MaxPacketNumber {
		t.Fatalf("decoded %d exceeds MaxPacketNumber %d", got, MaxPacketNumber)
	}
}

func TestDecodeWidthsProduceSelfAtWindowStart(t *testing.T) {
	for width := Width1; width <= Width4; width++ {
		window := uint64(1) << (width * 8)
		// At expected = window/2, truncated 0 is equidistant from candidates
		// 0 and window; the tie must resolve to the smaller candidate 0.
		got, err := decodePacketNumber(0, width, window/2)
		if err != nil {
			t.Fatalf("width %d: %v", width, err)
		}
		if got != 0 {
			t.Fatalf("width %d aligned decode = %d, want tie-broken 0", width, got)
		}
	}
}

func TestBuildHeaderRejectsOverflow(t *testing.T) {
	if _, err := BuildHeader(Width1, 0, 256); err != ErrTruncatedNumber {
		t.Fatalf("overflow: want ErrTruncatedNumber, got %v", err)
	}
	if _, err := EncodeDatagram(Width2, 0, 1<<16, nil); err != ErrTruncatedNumber {
		t.Fatalf("encode overflow: want ErrTruncatedNumber, got %v", err)
	}
	if _, err := BuildHeader(9, 0, 0); err != ErrBadWidth {
		t.Fatalf("bad width: want ErrBadWidth, got %v", err)
	}
}
