package hexgrid

import "testing"

func TestAssertCellValid(t *testing.T) {
	c, _ := LatLngToCell(39.9042, 116.4074, 8)
	if v := AssertCellValid(c); len(v) > 0 {
		t.Fatalf("valid cell flagged: %v", v)
	}
	if v := AssertCellValid(Cell(0)); len(v) == 0 {
		t.Fatal("zero cell must be flagged")
	}
}

func TestAssertHierarchyLevels(t *testing.T) {
	c, _ := LatLngToCell(51.5074, -0.1278, MaxRes) // London, finest res
	cur := c
	for {
		if v := AssertHierarchy(cur); len(v) > 0 {
			t.Fatalf("res %d: %v", cur.Resolution(), v)
		}
		p, ok := cur.Parent()
		if !ok {
			break
		}
		cur = p
	}
	if cur.Resolution() != 0 {
		t.Fatal("ancestor chain did not reach res 0")
	}
}

func TestAssertEncodeRoundTrip(t *testing.T) {
	coords := [][2]float64{
		{39.9042, 116.4074}, {90, 0}, {-90, 0}, {0, 180}, {0, -180},
		{0, 0}, {-33.8568, 151.2153}, {64.1466, -21.9426},
	}
	for _, ll := range coords {
		for _, res := range []int{0, 1, 4, 8, MaxRes} {
			if v := AssertEncodeRoundTrip(ll[0], ll[1], res); len(v) > 0 {
				t.Fatalf("coord %v res %d: %v", ll, res, v)
			}
		}
	}
}

func TestExpectedKRingSize(t *testing.T) {
	want := map[int]int{0: 1, 1: 7, 2: 19, 3: 37, 4: 61, 5: 91}
	for k, w := range want {
		if got := ExpectedKRingSize(k); got != w {
			t.Fatalf("k=%d: got %d, want %d", k, got, w)
		}
	}
}
