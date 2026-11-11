package h264bs

import (
	"bytes"
	"testing"
)

func TestSplitAnnexBStartCodes(t *testing.T) {
	nal1 := []byte{0x67, 0xAA, 0xBB}
	nal2 := []byte{0x68, 0xCC}
	nal3 := []byte{0x65, 0xDD, 0xEE, 0xFF}

	// Mix of 3-byte and 4-byte start codes plus zero padding.
	stream := bytes.Join([][]byte{
		{0x00, 0x00, 0x00, 0x01}, nal1,
		{0x00, 0x00, 0x01}, nal2,
		{0x00, 0x00, 0x00, 0x01}, nal3,
		{0x00, 0x00}, // trailing padding
	}, nil)

	got := SplitAnnexB(stream)
	want := [][]byte{nal1, nal2, nal3}
	if len(got) != len(want) {
		t.Fatalf("got %d NALs, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("NAL %d mismatch\nwant %x\n got %x", i, want[i], got[i])
		}
	}
}

func TestSplitAnnexBEdgeCases(t *testing.T) {
	if got := SplitAnnexB(nil); got != nil {
		t.Fatalf("nil stream should yield nil, got %v", got)
	}
	if got := SplitAnnexB([]byte{0x01, 0x02, 0x03}); got != nil {
		t.Fatalf("stream without start code should yield nil, got %x", got)
	}

	// Start code immediately followed by another start code (empty NAL
	// skipped), and a start code split at the buffer boundary.
	stream := []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x01, 0x41, 0x42}
	got := SplitAnnexB(stream)
	if len(got) != 1 || !bytes.Equal(got[0], []byte{0x41, 0x42}) {
		t.Fatalf("unexpected split result: %x", got)
	}
}

func TestPackAnnexBUsesFourByteStartCodes(t *testing.T) {
	nals := [][]byte{{0x67, 0x01}, {0x68, 0x02}}
	got := PackAnnexB(nals)
	want := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x01,
		0x00, 0x00, 0x00, 0x01, 0x68, 0x02,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("PackAnnexB mismatch\nwant %x\n got %x", want, got)
	}
	if split := SplitAnnexB(got); len(split) != 2 {
		t.Fatalf("re-split got %d NALs", len(split))
	}
}

func TestAnnexBAVCCRoundTrip(t *testing.T) {
	nals := [][]byte{
		{0x67, 0x42, 0x00, 0x00, 0x03, 0x01}, // SPS containing escaped bytes
		{0x68, 0xCE},
		{0x65, 0x88, 0x80},
	}
	annexB := PackAnnexB(nals)

	for _, lengthSize := range []int{1, 2, 4} {
		avcc, err := PackAVCC(nals, lengthSize)
		if err != nil {
			t.Fatalf("PackAVCC(%d): %v", lengthSize, err)
		}
		split, err := SplitAVCC(avcc, lengthSize)
		if err != nil {
			t.Fatalf("SplitAVCC(%d): %v", lengthSize, err)
		}
		if len(split) != len(nals) {
			t.Fatalf("lengthSize %d: got %d NALs", lengthSize, len(split))
		}
		for i := range nals {
			if !bytes.Equal(split[i], nals[i]) {
				t.Fatalf("lengthSize %d NAL %d mismatch", lengthSize, i)
			}
		}

		// Annex B -> AVCC -> Annex B must preserve every NAL payload.
		fromAnnexB, err := PackAVCC(SplitAnnexB(annexB), lengthSize)
		if err != nil {
			t.Fatalf("repack: %v", err)
		}
		back, err := SplitAVCC(fromAnnexB, lengthSize)
		if err != nil {
			t.Fatalf("resplit: %v", err)
		}
		repacked := PackAnnexB(back)
		if !bytes.Equal(repacked, annexB) {
			t.Fatalf("lengthSize %d round trip mismatch\nwant %x\n got %x",
				lengthSize, annexB, repacked)
		}
	}
}

func TestAVCCErrors(t *testing.T) {
	for _, bad := range []int{0, 3, 8} {
		if _, err := PackAVCC([][]byte{{0x65}}, bad); err == nil {
			t.Errorf("lengthSize %d should be rejected", bad)
		}
		if _, err := SplitAVCC([]byte{0x01, 0x65}, bad); err == nil {
			t.Errorf("lengthSize %d should be rejected", bad)
		}
	}

	// 1-byte prefix cannot encode a 256-byte NAL.
	big := make([]byte, 256)
	if _, err := PackAVCC([][]byte{big}, 1); err == nil {
		t.Error("expected ErrNALTooLong for 256-byte NAL with 1-byte prefix")
	}

	// Truncated payload.
	if _, err := SplitAVCC([]byte{0x05, 0x65}, 1); err == nil {
		t.Error("expected error for truncated AVCC payload")
	}
	// Empty NAL.
	if _, err := SplitAVCC([]byte{0x00}, 1); err == nil {
		t.Error("expected error for zero-length NAL")
	}
}
