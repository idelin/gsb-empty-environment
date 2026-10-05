// Command repro is a reproducible experiment: it builds a fixed point set
// (integer grid subset, cocircular points, a collinear chain, and large
// coordinates), triangulates it under many different input orders, and
// verifies that every run produces byte-identical canonical JSON output
// that passes the independent validator.
//
// Usage: go run ./cmd/repro [-trials N] [-seed S]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"

	"delaunay"
)

func dataset() []delaunay.RawPoint {
	var raw []delaunay.RawPoint
	add := func(id string, x, y int64) {
		raw = append(raw, delaunay.RawPoint{ID: id, X: jsonNum(x), Y: jsonNum(y)})
	}
	// 4x4 integer grid subset.
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if (i+j)%2 == 0 || i == j {
				add(fmt.Sprintf("g%d_%d", i, j), int64(i), int64(j))
			}
		}
	}
	// Cocircular points on a radius-5 circle.
	for i, p := range [][2]int64{{5, 0}, {4, 3}, {3, 4}, {0, 5}, {-3, 4}, {-4, 3}, {-5, 0}, {-4, -3}, {-3, -4}, {0, -5}, {3, -4}, {4, -3}} {
		add(fmt.Sprintf("c%d", i), p[0], p[1])
	}
	// Collinear chain on y = 2x + 1000.
	for i := 0; i < 20; i++ {
		add(fmt.Sprintf("l%d", i), int64(50+i), int64(2*(50+i)+1000))
	}
	// Large coordinates.
	add("big0", 1<<60, 1)
	add("big1", 1, 1<<60)
	add("big2", 1<<60, 1<<60)
	// Duplicates with different source IDs.
	add("dupA", 0, 0)
	add("dupB", 0, 0)
	return raw
}

func main() {
	trials := flag.Int("trials", 50, "number of shuffled runs")
	seed := flag.Int64("seed", 20261005, "base RNG seed")
	flag.Parse()

	base := dataset()
	var want string
	for t := 0; t < *trials; t++ {
		raw := append([]delaunay.RawPoint(nil), base...)
		rand.New(rand.NewSource(*seed+int64(t))).Shuffle(len(raw), func(i, j int) {
			raw[i], raw[j] = raw[j], raw[i]
		})
		out := delaunay.Triangulate(raw)
		if err := delaunay.Validate(raw, out); err != nil {
			fmt.Fprintf(os.Stderr, "trial %d: validation failed: %v\n", t, err)
			os.Exit(1)
		}
		bs, err := json.Marshal(out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "marshal:", err)
			os.Exit(2)
		}
		if t == 0 {
			want = string(bs)
			continue
		}
		if string(bs) != want {
			fmt.Fprintf(os.Stderr, "trial %d: non-canonical output differs from trial 0\n", t)
			os.Exit(1)
		}
	}
	fmt.Printf("PASS: %d shuffled trials, %d distinct points, identical canonical output, validator OK\n",
		*trials, len(delaunay.Triangulate(dataset()).Points))
}
