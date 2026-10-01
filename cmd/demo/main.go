// Command demo shows the chronos type system: distinct instant / wall /
// duration / offset types, evidence-carrying migrations, and safe decoding.
package main

import (
	"fmt"

	"chronos"
)

func main() {
	reg := chronos.SyntheticRegistry()
	v1, _ := reg.Lookup("X/Island", "2024a")
	v2, _ := reg.Lookup("X/Island", "2024b")

	fmt.Println("== 1. Ambiguous wall clock keeps its selected offset ==")
	w, err := chronos.NewWallClock(2021, 10, 31, 2, 30, 0, 0, chronos.PrecSecond, v1, 1)
	must(err)
	fmt.Println("wall:", w)
	cands, err := w.Candidates(v1)
	must(err)
	for i, c := range cands {
		fmt.Printf("  candidate %d -> %v\n", i, c)
	}
	picked, err := w.Instant(v1)
	must(err)
	fmt.Println("selected (fold=1, +1h) ->", picked)

	fmt.Println("\n== 2. TZ table update: offset preserved or lossy rebind ==")
	mid, st, err := chronos.RebindWall(w, v2)
	must(err)
	fmt.Printf("rebind 2024a->2024b: lossy=%v  %s\n", st.Lossy, st.Detail)
	fmt.Println("result:", mid)

	summer, err := chronos.NewWallClock(2022, 6, 15, 12, 0, 0, 0, chronos.PrecSecond, v1, 0)
	must(err)
	moved, st2, err := chronos.RebindWall(summer, v2)
	must(err)
	fmt.Printf("rebind 2024a->2024b: lossy=%v  %s\n", st2.Lossy, st2.Detail)
	fmt.Println("result:", moved)
	back, _, err := chronos.ReverseWall([]chronos.Step{st2}, moved, reg)
	must(err)
	fmt.Println("reverse candidates under 2024a:")
	for _, c := range back {
		fmt.Println("  ", c)
	}

	fmt.Println("\n== 3. Precision scaling with evidence ==")
	in, err := chronos.NewInstant(1_700_000_000, 123_456_789, chronos.PrecNanosecond)
	must(err)
	coarse, st3, err := chronos.ScaleInstant(in, chronos.PrecMillisecond)
	must(err)
	fmt.Printf("scale ns->ms: lossy=%v  %s\n", st3.Lossy, st3.Detail)
	_, iv, err := chronos.ReverseInstant([]chronos.Step{st3}, coarse)
	must(err)
	fmt.Printf("reverse interval: [%v, %v)\n", iv.Lo, iv.Hi)
	us, st4, err := chronos.ScaleInstant(in, chronos.PrecMicrosecond)
	must(err)
	fmt.Printf("scale ns->us (no ms truncation): %v lossy=%v\n", us, st4.Lossy)

	fmt.Println("\n== 4. Checked duration arithmetic ==")
	d1, _ := chronos.NewDuration(1_500_000_000, chronos.PrecNanosecond)
	d2, _ := chronos.NewDuration(2_000_000, chronos.PrecMillisecond)
	sum, err := d1.Add(d2)
	must(err)
	fmt.Println("1.5s + 2ms =", sum.Nanos(), "ns")
	big, _ := chronos.NewDuration(chronos.MaxDurationNanos, chronos.PrecNanosecond)
	if _, err := big.Mul(3); err != nil {
		fmt.Println("overflow rejected:", err)
	}

	fmt.Println("\n== 5. Binary round trip and safe decoding ==")
	blob, err := chronos.Encode(w)
	must(err)
	fmt.Printf("encoded %d bytes: %x...\n", len(blob), blob[:12])
	got, err := chronos.Decode(blob)
	must(err)
	fmt.Println("decoded:", got)
	corrupt := append([]byte(nil), blob...)
	corrupt[7] = 200 // absurd tz-version length
	if _, err := chronos.Decode(corrupt); err != nil {
		fmt.Println("corrupt length rejected:", err)
	}
	unknown := []byte(string(blob))
	copy(unknown[8:], "1999z")
	got2, err := chronos.Decode(unknown)
	must(err)
	fmt.Println("unknown tz version resolvable?", got2.(chronos.WallClock).Resolvable(reg))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
