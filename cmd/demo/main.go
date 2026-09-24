// Command demo exercises the enumkit library end to end.
package main

import (
	"errors"
	"fmt"

	"enumkit"
)

func main() {
	color := enumkit.New("color")
	must(color.Define(1, "RED"))
	must(color.Define(2, "GREEN"))
	must(color.Define(3, "BLUE"))
	must(color.AddVersion(enumkit.Version{
		Name:   "v1",
		Active: map[int64]string{1: "RED", 2: "GREEN", 3: "BLUE"},
	}))
	must(color.Rename(2, "EMERALD"))
	must(color.AddVersion(enumkit.Version{
		Name:    "v2",
		Active:  map[int64]string{1: "RED", 2: "EMERALD", 3: "BLUE"},
		Aliases: []enumkit.Alias{{Name: "GREEN", ID: 2}},
	}))
	must(color.Rename(1, "CRIMSON"))
	must(color.AddVersion(enumkit.Version{
		Name:    "v3",
		Active:  map[int64]string{1: "CRIMSON", 2: "EMERALD", 3: "BLUE"},
		Aliases: []enumkit.Alias{{Name: "RED", ID: 1}}, // GREEN alias retracted in v3
	}))

	fmt.Println("== decode: old alias, canonical output, passthrough ==")
	v, _ := color.Decode("v2", "GREEN")
	fmt.Printf("v2 \"GREEN\"      -> id=%d canonical=%q\n", v.ID, color.Canonical(v))
	v, _ = color.Decode("v3", "GREEN")
	fmt.Printf("v3 \"GREEN\"      -> known=%v raw=%q (alias retracted, lexical passthrough)\n", v.Known(), v.Raw)
	v, _ = color.Decode("v1", "99")
	fmt.Printf("v1 \"99\"         -> known=%v raw=%q (unknown number, not coerced)\n", v.Known(), v.Raw)

	fmt.Println("\n== snapshot survives alias retraction ==")
	snap, _ := color.Snapshot("v2")
	must(color.RetractAlias("v2", "GREEN"))
	id, ok := snap.Resolve("GREEN")
	fmt.Printf("snapshot(v2) GREEN -> id=%d ok=%v (frozen)\n", id, ok)
	live, _ := color.Decode("v2", "GREEN")
	fmt.Printf("live v2 GREEN      -> known=%v (retracted)\n", live.Known())

	fmt.Println("\n== merge + migrate across versions ==")
	g, err := color.Merge()
	must(err)
	mv, _ := g.Migrate("v1", "v3", "RED")
	fmt.Printf("migrate v1->v3 \"RED\"   -> id=%d canonical@v3=%q\n", mv.ID, g.CanonicalAt("v3", mv))
	mv, _ = g.Migrate("v1", "v3", "GREEN")
	fmt.Printf("migrate v1->v3 \"GREEN\" -> id=%d canonical@v3=%q\n", mv.ID, g.CanonicalAt("v3", mv))
	fmt.Printf("graph verify: %d problems\n", len(g.Verify()))

	fmt.Println("\n== ambiguous migration is reported, never guessed ==")
	lamp := enumkit.New("lamp")
	must(lamp.Define(4, "AMBER"))
	must(lamp.Define(5, "VIOLET"))
	must(lamp.AddVersion(enumkit.Version{
		Name:    "v1",
		Active:  map[int64]string{4: "AMBER", 5: "VIOLET"},
		Aliases: []enumkit.Alias{{Name: "LIGHT", ID: 4}},
	}))
	must(lamp.AddVersion(enumkit.Version{
		Name:    "v2",
		Active:  map[int64]string{4: "AMBER", 5: "VIOLET"},
		Aliases: []enumkit.Alias{{Name: "LIGHT", ID: 5}},
	}))
	lg, err := lamp.Merge()
	must(err)
	_, err = lg.Migrate("v1", "v2", "LIGHT")
	var ae *enumkit.AmbiguityError
	if errors.As(err, &ae) {
		fmt.Printf("migrate \"LIGHT\": %v\n", err)
	}
	fmt.Printf("lamp graph verify: %d problem(s), first: %v\n", len(lg.Verify()), lg.Verify()[0])

	fmt.Println("\n== merge conflict: two IDs share one active name ==")
	bad := enumkit.New("bad")
	must(bad.Define(1, "RED"))
	must(bad.Define(2, "GREEN"))
	must(bad.AddVersion(enumkit.Version{Name: "v1", Active: map[int64]string{1: "RED", 2: "GREEN"}}))
	must(bad.AddVersion(enumkit.Version{
		Name:    "v2",
		Active:  map[int64]string{1: "RED", 2: "EMERALD"},
		Aliases: []enumkit.Alias{{Name: "GREEN", ID: 1}},
	}))
	if _, err := bad.Merge(); err != nil {
		fmt.Printf("merge rejected: %v\n", err)
	}

	fmt.Println("\n== safe codec: round-trip, rename-stable identity ==")
	cv, _ := color.Decode("v1", "RED")
	wire, _ := cv.MarshalBinary()
	fmt.Printf("wire(v1 RED) = % x\n", wire)
	back, _ := enumkit.UnmarshalValue(wire)
	fmt.Printf("decoded id=%d canonical(now)=%q (rename did not change identity)\n", back.ID, color.Canonical(back))
	if _, err := enumkit.UnmarshalValue([]byte{0x02, 0xff, 0xff, 0xff, 0xff, 0x0f}); err != nil {
		fmt.Printf("oversized length prefix rejected before allocation: %v\n", err)
	}

	fmt.Println("\n== JSON document migration with original field paths ==")
	doc := []byte(`{"spec":{"mode":"RED","items":[{"mode":"GREEN"},{"mode":"LIGHT"}]}}`)
	fields := []enumkit.Field{{Path: "spec.mode", Reg: color}, {Path: "spec.items.*.mode", Reg: color}, {Path: "spec.items.*.mode", Reg: lamp}}
	out, errs := enumkit.MigrateJSON(doc, "v1", "v3", fields[:2])
	fmt.Printf("color migration: %s (errs=%v)\n", out, errs)
	_, errs = enumkit.MigrateJSON(doc, "v1", "v2", []enumkit.Field{{Path: "spec.items.*.mode", Reg: lamp}})
	for _, e := range errs {
		fmt.Printf("lamp migration error: %v\n", e)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
