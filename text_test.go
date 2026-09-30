package fpcodec

import "testing"

func TestTextDisplay(t *testing.T) {
	cases := []struct {
		w    Width
		bits uint64
		want string
	}{
		{Width16, 0x0000, "+0"},
		{Width16, 0x8000, "-0"},
		{Width16, 0x7C00, "+Inf"},
		{Width16, 0xFC00, "-Inf"},
		{Width16, 0x7E00, "+qNaN(payload=0x200)"},
		{Width16, 0x7C01, "+sNaN(payload=0x1)"},
		{Width16, 0xFC01, "-sNaN(payload=0x1)"},
		{Width16, 0x3C00, "0x1p+00"}, // 1.0
		{Width32, 0x80000000, "-0"},
		{Width32, 0x7FC00001, "+qNaN(payload=0x400001)"},
		{Width64, 0x7FF0000000000001, "+sNaN(payload=0x1)"},
		{Width64, 0xFFF8DEADBEEFCAFE, "-qNaN(payload=0x8DEADBEEFCAFE)"},
		{Width(8), 0, "<unsupported width 8>"},
	}
	for _, c := range cases {
		if got := Text(c.w, c.bits); got != c.want {
			t.Errorf("Text(%d, %#x) = %q, want %q", c.w, c.bits, got, c.want)
		}
	}
}
