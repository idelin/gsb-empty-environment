package ttfglyph

import "testing"

func TestReadCoordinateDeltas(t *testing.T) {
	// Four points exercising every x-encoding:
	// p0 short positive, p1 short negative, p2 same(zero), p3 signed 16-bit.
	flags := []byte{
		flagXShort | flagSameX,
		flagXShort,
		flagSameX,
		0,
	}
	c := newCursor([]byte{7, 5, 0x00, 0x2A}) // +7, -5, (zero), +42
	got, err := readCoordinateDeltas(c, flags, flagXShort, flagSameX)
	if err != nil {
		t.Fatalf("readCoordinateDeltas: %v", err)
	}
	want := []int32{7, -5, 0, 42}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("delta %d = %d, want %d", i, got[i], want[i])
		}
	}
	if c.pos != len(c.b) {
		t.Errorf("cursor pos=%d, want fully consumed %d", c.pos, len(c.b))
	}
}

func TestReadCoordinateDeltasTruncated(t *testing.T) {
	flags := []byte{flagXShort | flagSameX}
	if _, err := readCoordinateDeltas(newCursor(nil), flags, flagXShort, flagSameX); err == nil {
		t.Fatal("expected error on truncated coordinate stream")
	}
}

func TestExpandContourVariants(t *testing.T) {
	cases := []struct {
		name string
		in   []Point
		want []Point
	}{
		{
			name: "all on-curve unchanged",
			in:   []Point{on(0, 0), on(1, 0), on(1, 1)},
			want: []Point{on(0, 0), on(1, 0), on(1, 1)},
		},
		{
			name: "single off-curve between on-curve anchors",
			in:   []Point{on(0, 0), off(2, 2), on(4, 0)},
			want: []Point{on(0, 0), off(2, 2), on(4, 0)},
		},
		{
			name: "two adjacent off-curve get midpoint",
			in:   []Point{on(0, 0), off(2, 0), off(4, 0), on(4, 2)},
			want: []Point{on(0, 0), off(2, 0), on(3, 0), off(4, 0), on(4, 2)},
		},
		{
			name: "all off-curve seeds midpoint",
			in:   []Point{off(0, 0), off(4, 0), off(4, 4)},
			want: []Point{
				on(2, 2),
				off(0, 0),
				on(2, 0),
				off(4, 0),
				on(4, 2),
				off(4, 4),
				on(2, 2), // closing edge midpoint, equal to the seed
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandContour(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d points %+v, want %d %+v", len(got), got, len(tc.want), tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("point %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestCursorBounds(t *testing.T) {
	c := newCursor([]byte{0x01, 0x02})
	if _, err := c.u32(); err == nil {
		t.Error("u32 past end must error")
	}
	if v, err := c.u16(); err != nil || v != 0x0102 {
		t.Errorf("u16 = (%d,%v), want (258,nil)", v, err)
	}
	if _, err := c.u8(); err == nil {
		t.Error("u8 past end must error")
	}
}
