package fmp4mux

import (
	"bytes"
	"testing"
)

func TestSplitAnnexB(t *testing.T) {
	nal1 := []byte{0x67, 0x42, 0x00, 0x1e}
	nal2 := []byte{0x68, 0xce}
	nal3 := []byte{0x65, 0x88, 0x80, 0x40}

	cases := []struct {
		name string
		data []byte
		want [][]byte
	}{
		{
			name: "4-byte start codes",
			data: append(append(
				append([]byte{0, 0, 0, 1}, nal1...),
				append([]byte{0, 0, 0, 1}, nal2...)...),
				append([]byte{0, 0, 0, 1}, nal3...)...),
			want: [][]byte{nal1, nal2, nal3},
		},
		{
			name: "3-byte start codes",
			data: append(append(
				append([]byte{0, 0, 1}, nal1...),
				append([]byte{0, 0, 1}, nal2...)...),
				append([]byte{0, 0, 1}, nal3...)...),
			want: [][]byte{nal1, nal2, nal3},
		},
		{
			name: "mixed start code widths and trailing NAL",
			data: append(
				append([]byte{0, 0, 1}, nal1...),
				append(append([]byte{0, 0, 0, 1}, nal2...),
					append([]byte{0, 0, 1}, nal3...)...)...),
			want: [][]byte{nal1, nal2, nal3},
		},
		{
			name: "start code at buffer end",
			data: append(append([]byte{0, 0, 0, 1}, nal1...),
				[]byte{0, 0, 1}...),
			want: [][]byte{nal1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitAnnexB(tc.data)
			if len(got) != len(tc.want) {
				t.Fatalf("NAL count = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if !bytes.Equal(got[i], tc.want[i]) {
					t.Errorf("NAL %d = %x, want %x", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSplitLengthPrefixedRoundTrip(t *testing.T) {
	nals := [][]byte{
		{0x67, 0x42, 0x00, 0x1e},
		{0x68, 0xce},
		{0x65, 0xaa, 0xbb},
	}
	lp := lengthPrefixed(nals...)
	got, ok := splitLengthPrefixed(lp)
	if !ok {
		t.Fatal("valid length-prefixed sample rejected")
	}
	if len(got) != len(nals) {
		t.Fatalf("NAL count = %d, want %d", len(got), len(nals))
	}
	for i := range got {
		if !bytes.Equal(got[i], nals[i]) {
			t.Errorf("NAL %d mismatch", i)
		}
	}

	// Truncated length field must be rejected rather than misparsed.
	bad := append([]byte{0, 0, 0, 9}, 0x65)
	if _, ok := splitLengthPrefixed(bad); ok {
		t.Error("truncated sample should fail length-prefixed parsing")
	}
}

func TestToAVCSamplePrefixes(t *testing.T) {
	nal := []byte{0x65, 0x01, 0x02}
	annex := append([]byte{0, 0, 0, 1}, nal...)
	got := toAVCSample(annex)
	if len(got) != 4+len(nal) || got[3] != byte(len(nal)) {
		t.Fatalf("unexpected AVCC sample: %x", got)
	}
	if !bytes.Equal(got[4:], nal) {
		t.Error("AVCC payload mismatch")
	}
}
