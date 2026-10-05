package delaunay

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestIncrementalInsertion inserts points one at a time and validates the
// result after every single insertion, including duplicates, collinear
// points, and points landing exactly on existing edges.
func TestIncrementalInsertion(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	tri := New()
	var raw []RawPoint
	check := func(step int) {
		out := tri.Result()
		if err := Validate(raw, out); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
	for i := 0; i < 60; i++ {
		var x, y int64
		switch i % 4 {
		case 0: // random grid point
			x, y = int64(rng.Intn(8)), int64(rng.Intn(8))
		case 1: // collinear chain on y = 2x
			x, y = int64(i), int64(2*i)
		case 2: // point exactly on the segment between two earlier points
			if len(raw) >= 2 {
				x = (raw[0].X.Int64() + raw[len(raw)-1].X.Int64()) / 2
				y = (raw[0].Y.Int64() + raw[len(raw)-1].Y.Int64()) / 2
			}
		case 3: // duplicate of an earlier point with a new ID
			if len(raw) > 0 {
				j := rng.Intn(len(raw))
				x, y = raw[j].X.Int64(), raw[j].Y.Int64()
			}
		}
		raw = append(raw, raw_(fmt.Sprintf("p%d", i), x, y))
		tri.Insert(raw[len(raw)-1].X, raw[len(raw)-1].Y, raw[len(raw)-1].ID)
		check(i)
	}
}

func raw_(id string, x, y int64) RawPoint { return raw(id, x, y) }
