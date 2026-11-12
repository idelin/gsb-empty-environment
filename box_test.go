package netsec

import (
	"reflect"
	"testing"
)

// pointKey renders an integer point as a unique map key. Coordinates are
// separated by NUL so decimal concatenation can never collide.
func pointKey(p []int64) string {
	buf := make([]byte, 0, len(p)*9)
	for i, v := range p {
		if i > 0 {
			buf = append(buf, 0)
		}
		buf = appendSigned(buf, v)
	}
	return string(buf)
}

func appendSigned(buf []byte, v int64) []byte {
	if v < 0 {
		buf = append(buf, '-')
		v = -v
	}
	if v == 0 {
		return append(buf, '0')
	}
	var digits [20]byte
	n := 0
	for v > 0 {
		digits[n] = byte('0' + v%10)
		n++
		v /= 10
	}
	for i := n - 1; i >= 0; i-- {
		buf = append(buf, digits[i])
	}
	return buf
}

// enumeratePoints returns every integer point contained in b.
func enumeratePoints(b Box) [][]int64 {
	dim := b.Dim()
	sizes := make([]int64, dim)
	total := int64(1)
	for i := 0; i < dim; i++ {
		sizes[i] = b.Hi[i] - b.Lo[i]
		total *= sizes[i]
	}
	out := make([][]int64, 0, total)
	idx := make([]int64, dim)
	for {
		p := make([]int64, dim)
		for i := 0; i < dim; i++ {
			p[i] = b.Lo[i] + idx[i]
		}
		out = append(out, p)

		k := dim - 1
		for k >= 0 {
			idx[k]++
			if idx[k] < sizes[k] {
				break
			}
			idx[k] = 0
			k--
		}
		if k < 0 {
			return out
		}
	}
}

func pointSet(boxes []Box) map[string][]int64 {
	set := map[string][]int64{}
	for _, b := range boxes {
		for _, p := range enumeratePoints(b) {
			set[pointKey(p)] = p
		}
	}
	return set
}

func TestBoxContainsAndVolume(t *testing.T) {
	b := Box{Lo: []int64{0, 0}, Hi: []int64{3, 2}}
	wantVol := int64(6)
	if got, err := b.Volume(); err != nil || got != wantVol {
		t.Fatalf("Volume() = %d, %v; want %d", got, err, wantVol)
	}

	inside := [][]int64{{0, 0}, {2, 1}, {0, 1}}
	outside := [][]int64{{3, 0}, {2, 2}, {-1, 0}, {0}, {0, 0, 0}}
	for _, p := range inside {
		if !b.Contains(p) {
			t.Errorf("Contains(%v) = false; want true", p)
		}
	}
	for _, p := range outside {
		if b.Contains(p) {
			t.Errorf("Contains(%v) = true; want false", p)
		}
	}

	empty := Box{Lo: []int64{1, 2}, Hi: []int64{1, 5}}
	if !empty.Empty() {
		t.Error("degenerate box should be empty")
	}
}

func TestSubtractCentralHole2D(t *testing.T) {
	access := Box{Lo: []int64{0, 0}, Hi: []int64{5, 5}}
	denied := Box{Lo: []int64{2, 2}, Hi: []int64{4, 4}}

	parts, err := Subtract(access, denied)
	if err != nil {
		t.Fatalf("Subtract: %v", err)
	}

	got := pointSet(parts)
	for _, p := range enumeratePoints(access) {
		inDenied := denied.Contains(p)
		_, covered := got[pointKey(p)]
		if inDenied && covered {
			t.Errorf("denied point %v leaked into accessible partition", p)
		}
		if !inDenied && !covered {
			t.Errorf("accessible point %v missing (hole)", p)
		}
	}
	if len(got) != 25-4 {
		t.Fatalf("accessible point count = %d; want %d", len(got), 21)
	}

	// Parts must be pairwise disjoint boxes.
	seen := map[string]int{}
	for i, part := range parts {
		if part.Empty() {
			t.Fatalf("part %d is empty", i)
		}
		for _, p := range enumeratePoints(part) {
			key := pointKey(p)
			if prev, dup := seen[key]; dup {
				t.Fatalf("point %v covered by both part %d and part %d", p, prev, i)
			}
			seen[key] = i
		}
	}
}

func TestSubtractBoundarySlices(t *testing.T) {
	access := Box{Lo: []int64{0, 0}, Hi: []int64{4, 4}}
	cases := []struct {
		name   string
		denied Box
	}{
		{"touching origin corner", Box{Lo: []int64{0, 0}, Hi: []int64{2, 2}}},
		{"touching far corner", Box{Lo: []int64{3, 3}, Hi: []int64{4, 4}}},
		{"full-width strip", Box{Lo: []int64{1, 0}, Hi: []int64{3, 4}}},
		{"single boundary cell", Box{Lo: []int64{0, 3}, Hi: []int64{1, 4}}},
		{"disjoint outside", Box{Lo: []int64{10, 10}, Hi: []int64{12, 12}}},
		{"covering everything", Box{Lo: []int64{0, 0}, Hi: []int64{4, 4}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts, err := Subtract(access, tc.denied)
			if err != nil {
				t.Fatalf("Subtract: %v", err)
			}
			assertPartition(t, access, []Box{tc.denied}, parts)
		})
	}
}

func TestSubtractMultipleDenied3D(t *testing.T) {
	access := Box{Lo: []int64{-2, -2, -2}, Hi: []int64{3, 3, 3}}
	denied := []Box{
		{Lo: []int64{0, 0, 0}, Hi: []int64{2, 2, 2}},
		{Lo: []int64{-1, -1, -1}, Hi: []int64{1, 1, 1}}, // overlaps first
		{Lo: []int64{2, 2, 2}, Hi: []int64{3, 3, 3}},    // corner, touches first
		{Lo: []int64{-2, -2, -2}, Hi: []int64{-1, -1, -1}},
	}
	parts, err := Subtract(access, denied...)
	if err != nil {
		t.Fatalf("Subtract: %v", err)
	}
	assertPartition(t, access, denied, parts)
}

// assertPartition brute-forces every integer point and proves that parts is
// exactly access minus the union of denied, with no overlaps.
func assertPartition(t *testing.T, access Box, denied []Box, parts []Box) {
	t.Helper()

	coverage := map[string]int{}
	for i, part := range parts {
		if part.Empty() {
			t.Fatalf("part %d is empty", i)
		}
		for _, p := range enumeratePoints(part) {
			key := pointKey(p)
			if prev, dup := coverage[key]; dup {
				t.Fatalf("point %v duplicated in parts %d and %d", p, prev, i)
			}
			coverage[key] = i
			for _, d := range denied {
				if d.Contains(p) {
					t.Fatalf("part %d contains denied point %v", i, p)
				}
			}
		}
	}

	for _, p := range enumeratePoints(access) {
		isDenied := false
		for _, d := range denied {
			if d.Contains(p) {
				isDenied = true
				break
			}
		}
		_, covered := coverage[pointKey(p)]
		if isDenied && covered {
			t.Fatalf("denied point %v covered", p)
		}
		if !isDenied && !covered {
			t.Fatalf("accessible point %v is a hole", p)
		}
	}
}

func TestIntersect(t *testing.T) {
	a := Box{Lo: []int64{0, 0}, Hi: []int64{5, 5}}
	b := Box{Lo: []int64{3, -1}, Hi: []int64{8, 4}}
	got, ok := Intersect(a, b)
	if !ok {
		t.Fatal("expected intersection")
	}
	want := Box{Lo: []int64{3, 0}, Hi: []int64{5, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Intersect = %v; want %v", got, want)
	}

	if _, ok := Intersect(a, Box{Lo: []int64{5, 0}, Hi: []int64{6, 5}}); ok {
		t.Fatal("half-open boundary touch must not intersect")
	}
}
