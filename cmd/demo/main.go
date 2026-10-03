// Command demo runs the acceptance flow end to end:
//
//  1. encode a GPS coordinate as a resolution-8 hexadecimal cell;
//  2. traverse its radius-3 K-ring and verify continuous closed
//     coverage with no holes;
//  3. demonstrate parent/child scaling;
//  4. run the offline integrity inspector over a clean store and over
//     a store with planted dangling references, an isolated loop and
//     data skew.
package main

import (
	"fmt"
	"os"

	"hexgrid"
)

func check(err error) {
	if err != nil {
		fmt.Println("FATAL:", err)
		os.Exit(1)
	}
}

func main() {
	// --- 1. GPS -> resolution-8 hex cell -----------------------------
	const lat, lng = 39.9042, 116.4074 // Tiananmen, Beijing
	cell, err := hexgrid.LatLngToCell(lat, lng, 8)
	check(err)
	center := hexgrid.CellToLatLng(cell)
	fmt.Println("== 1. Encode GPS at resolution 8 ==")
	fmt.Printf("gps=(%.4f, %.4f) -> cell=%s (base=%d res=%d digits=%v)\n",
		lat, lng, cell, cell.BaseCell(), cell.Res(), cell.Digits())
	fmt.Printf("cell center = (%.6f, %.6f)\n\n", center.Lat, center.Lng)

	// --- 2. K-ring radius 3 + closed coverage ------------------------
	ring, err := hexgrid.KRing(cell, 3)
	check(err)
	fmt.Println("== 2. K-ring radius 3 ==")
	fmt.Printf("k-ring(3) cells = %d (want 1+3*3*4 = 37)\n", len(ring))
	hist := map[int]int{}
	for _, c := range ring {
		d, err := hexgrid.RingDistance(cell, c)
		check(err)
		hist[d]++
	}
	fmt.Printf("distance histogram: ring0=%d ring1=%d ring2=%d ring3=%d\n",
		hist[0], hist[1], hist[2], hist[3])
	check(hexgrid.AssertKRing(cell, 3))
	check(hexgrid.VerifyClosedCoverage(ring))
	fmt.Println("invariants: cardinality/uniqueness/completeness/contiguity/symmetry OK")
	fmt.Println("coverage: continuous, closed, single boundary loop, no holes")
	fmt.Printf("first ring-1 neighbors: %v\n\n", ring[1:4])

	// --- 3. Parent/child scaling -------------------------------------
	fmt.Println("== 3. Parent/child scaling ==")
	p, err := cell.Parent()
	check(err)
	kids, err := p.Children()
	check(err)
	fmt.Printf("cell   %s (res %d)\n", cell, cell.Res())
	fmt.Printf("parent %s (res %d), %d children, contains cell: %v\n\n",
		p, p.Res(), len(kids), contains(kids, cell))

	// --- 4a. Inspector on a clean store ------------------------------
	fmt.Println("== 4a. Inspector: clean store ==")
	clean := hexgrid.NewStore()
	set := map[hexgrid.Cell]bool{}
	for _, c := range ring {
		set[c] = true
	}
	for _, c := range ring {
		var nbs []hexgrid.Cell
		all, err := hexgrid.Neighbors(c)
		check(err)
		for _, nb := range all {
			if set[nb] {
				nbs = append(nbs, nb)
			}
		}
		clean.Upsert(hexgrid.Record{Cell: c, Neighbors: nbs, Payload: 1})
	}
	rep := hexgrid.Inspector{SkewThreshold: 0}.Inspect(clean)
	fmt.Println(rep.Summary())
	fmt.Printf("verdict: OK=%v\n\n", rep.OK())

	// --- 4b. Inspector on a store with planted anomalies -------------
	fmt.Println("== 4b. Inspector: planted anomalies ==")
	dirty := hexgrid.NewStore()
	for b := 0; b < hexgrid.NumBaseCells; b++ { // balanced global records
		ll := hexgrid.FaceCenter(b)
		c0, err := hexgrid.LatLngToCell(ll.Lat, ll.Lng, 8)
		check(err)
		dirty.Upsert(hexgrid.Record{Cell: c0, Payload: 1})
	}
	// Plant 1: dangling parent reference.
	ghost := hexgrid.MustCell(2, 7, []uint8{3, 3, 3, 3, 3, 3, 3})
	dirty.Upsert(hexgrid.Record{
		Cell:   hexgrid.MustCell(9, 8, []uint8{1, 1, 1, 1, 1, 1, 1, 1}),
		Parent: ghost,
	})
	// Plant 2: isolated closed loop (triangle) detached from everything.
	far, err := hexgrid.LatLngToCell(0, 20.9052, 8)
	check(err)
	fn, err := hexgrid.Neighbors(far)
	check(err)
	a, b2, c2 := far, fn[0], fn[1]
	dirty.Upsert(hexgrid.Record{Cell: a, Neighbors: []hexgrid.Cell{b2, c2}})
	dirty.Upsert(hexgrid.Record{Cell: b2, Neighbors: []hexgrid.Cell{a, c2}})
	dirty.Upsert(hexgrid.Record{Cell: c2, Neighbors: []hexgrid.Cell{a, b2}})
	// Plant 3: data skew - flood one base cell with a linked disk.
	flood, err := hexgrid.KRing(cell, 6)
	check(err)
	floodSet := map[hexgrid.Cell]bool{}
	for _, c := range flood {
		floodSet[c] = true
	}
	for _, c := range flood {
		var nbs []hexgrid.Cell
		all, err := hexgrid.Neighbors(c)
		check(err)
		for _, nb := range all {
			if floodSet[nb] {
				nbs = append(nbs, nb)
			}
		}
		dirty.Upsert(hexgrid.Record{Cell: c, Neighbors: nbs})
	}
	rep = hexgrid.DefaultInspector().Inspect(dirty)
	fmt.Println(rep.Summary())
	fmt.Printf("verdict: OK=%v (false expected: anomalies were planted)\n", rep.OK())
	if rep.OK() {
		fmt.Println("FATAL: inspector failed to detect planted anomalies")
		os.Exit(1)
	}
	fmt.Println("\nALL CHECKS PASSED")
}

func contains(cells []hexgrid.Cell, c hexgrid.Cell) bool {
	for _, x := range cells {
		if x == c {
			return true
		}
	}
	return false
}
