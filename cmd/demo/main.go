// Command demo exercises the chrono package end to end: overlap resolution,
// multi-rollback migration with evidence, precision scaling with reverse
// intervals, and a codec round trip.
package main

import (
	"fmt"

	"chrono/chrono"
)

func main() {
	reg := chrono.LabRegistry()

	fmt.Println("== overlap: one wall reading, two instants ==")
	early := mustWall(2024, 10, 27, 2, 30, 0, 0, 7200, chrono.ChoiceEarlier, "v1")
	late := mustWall(2024, 10, 27, 2, 30, 0, 0, 3600, chrono.ChoiceLater, "v1")
	ie := mustResolve(early, reg)
	il := mustResolve(late, reg)
	fmt.Println("earlier choice ->", ie)
	fmt.Println("later   choice ->", il)

	fmt.Println("\n== multi-rollback migration v1 -> v2 -> v3 -> v1 ==")
	w := late
	fmt.Println("start:", w)
	for _, v := range []string{"v2", "v3", "v2", "v1"} {
		next, m, err := chrono.MigrateWallClock(w, reg, v)
		if err != nil {
			fmt.Println("migration failed:", err)
			return
		}
		fmt.Println(m)
		w = next
	}
	fmt.Println("after round trip:", w)
	fmt.Println("round trip restored original:", w == late)

	fmt.Println("\n== reverse migration returns all candidates ==")
	cands, m, err := chrono.ReverseWallClock(w, reg, "v1")
	if err != nil {
		fmt.Println("reverse failed:", err)
		return
	}
	fmt.Println(m)
	for _, c := range cands {
		fmt.Println("  candidate:", c)
	}

	fmt.Println("\n== precision scaling with evidence ==")
	i := mustInstant(123456789, chrono.PrecNanosecond)
	fmt.Println("original:", i)
	micro, m, _ := chrono.ScaleInstant(i, chrono.PrecMicrosecond)
	fmt.Println(m, "->", micro)
	milli, m, _ := chrono.ScaleInstant(i, chrono.PrecMillisecond)
	fmt.Println(m, "->", milli)
	lo, hi := chrono.ReverseScaleInterval(milli)
	fmt.Printf("reverse interval for %s: [%d, %d] ns\n", milli, lo, hi)

	fmt.Println("\n== codec round trip ==")
	for _, v := range []chrono.Value{i, late} {
		b, err := chrono.Encode(v)
		if err != nil {
			fmt.Println("encode failed:", err)
			return
		}
		back, err := chrono.Decode(b)
		if err != nil {
			fmt.Println("decode failed:", err)
			return
		}
		fmt.Printf("% x -> %v\n", b, back)
	}

	fmt.Println("\n== unknown tz version is unresolvable ==")
	ghost := mustWall(2024, 10, 27, 2, 30, 0, 0, 3600, chrono.ChoiceLater, "v99")
	if _, err := chrono.Resolve(ghost, reg); err != nil {
		fmt.Println("resolve v99:", err)
	}
}

func mustWall(y, mo, d, h, mi, s int, ns int64, off int32, c chrono.OffsetChoice, ver string) chrono.WallClock {
	w, err := chrono.NewWallClock(y, mo, d, h, mi, s, ns, off, c, chrono.PrecNanosecond, "Lab/Zone", ver)
	if err != nil {
		panic(err)
	}
	return w
}

func mustResolve(w chrono.WallClock, reg *chrono.Registry) chrono.Instant {
	i, err := chrono.Resolve(w, reg)
	if err != nil {
		panic(err)
	}
	return i
}

func mustInstant(n int64, p chrono.Precision) chrono.Instant {
	i, err := chrono.NewInstant(n, p)
	if err != nil {
		panic(err)
	}
	return i
}
