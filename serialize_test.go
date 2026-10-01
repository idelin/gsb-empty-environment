package graphindex

import (
	"errors"
	"reflect"
	"testing"
)

func TestMarshalParseRoundTrip(t *testing.T) {
	g := buildComplete(t, 64, [][3]uint32{
		{0, 1, 2}, {0, 63, 1}, {0, 0, 4}, // parallel + self-loop
		{7, 8, 1}, {7, 9, 3}, {7, 10, 1}, {7, 11, 1},
		{7, 12, 1}, {7, 13, 1}, {7, 14, 1}, {7, 15, 1}, {7, 16, 1}, // >BlockSize
	})
	g2, err := Parse(g.Marshal())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for u := uint32(0); u < 64; u++ {
		a, err := g.Neighbors(u)
		if err != nil {
			t.Fatal(err)
		}
		b, err := g2.Neighbors(u)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("node %d neighbors differ after round-trip", u)
		}
	}
	m, _ := g2.Multiplicity(0, 0)
	if m != 4 {
		t.Fatalf("Multiplicity(0,0)=%d after round-trip, want 4", m)
	}
	m, _ = g2.Multiplicity(7, 9)
	if m != 3 {
		t.Fatalf("Multiplicity(7,9)=%d after round-trip, want 3", m)
	}
	s1, s2 := g.Stats(), g2.Stats()
	if s1.UniqueEdges != s2.UniqueEdges || s1.EdgeInstances != s2.EdgeInstances ||
		s1.TotalBytes != s2.TotalBytes {
		t.Fatalf("stats differ: %+v vs %+v", s1, s2)
	}
}

func TestParseRejectsCorruptDelta(t *testing.T) {
	g := buildComplete(t, 16, [][3]uint32{{0, 3, 1}, {0, 5, 1}, {0, 8, 1}})
	buf := g.Marshal()
	// magic(4) + numNodes(1B) + dataLen(1B) + node0 header(4B) = first delta.
	buf[4+1+1+4] = 0x7f // id 3+127 = 130 >= 16
	if _, err := Parse(buf); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Parse err=%v, want ErrCorrupt", err)
	}
}

func TestParseRejectsTruncation(t *testing.T) {
	g := buildComplete(t, 4, [][3]uint32{{0, 1, 1}})
	buf := g.Marshal()
	if _, err := Parse(buf[:len(buf)-3]); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Parse truncated err=%v, want ErrCorrupt", err)
	}
	if _, err := Parse([]byte("nope")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Parse garbage err=%v, want ErrCorrupt", err)
	}
}
