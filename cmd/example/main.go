// Example: versioned color enum with stable IDs, a rename, an unknown value,
// an ambiguity across versions, and a historical snapshot.
package main

import (
	"errors"
	"fmt"

	"verenum"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	r := verenum.New()
	check(r.Register(1, 1, "red"))
	check(r.Register(1, 2, "green"))
	// v2: stable id 1 renamed red -> crimson; "red" stays an old-name alias.
	check(r.Register(2, 1, "crimson", "red"))
	check(r.Register(2, 2, "green"))

	// Freeze the v2 historical schema before anything changes.
	snap, ok := r.Snapshot(2)
	fmt.Printf("snapshot v2 ready: %v\n", ok)

	fmt.Println("--- rename keeps identity, canonical output uses current name ---")
	v, err := r.Decode(1, []byte(`"red"`))
	check(err)
	fmt.Printf("decode v1 \"red\"   -> id=%d known=%v\n", v.ID, v.Known)
	out, err := r.Encode(2, v)
	check(err)
	fmt.Printf("encode at v2       -> %s\n", out)
	canon, err := r.Migrate(1, 2, "red", "payload.color")
	check(err)
	fmt.Printf("migrate v1->v2     -> %q\n", canon)

	fmt.Println("--- unknown values pass through, numbers are never coerced ---")
	u, err := r.Decode(2, []byte(`"fuchsia"`))
	check(err)
	uo, _ := r.Encode(2, u)
	fmt.Printf("unknown name       -> known=%v raw=%q re-encoded=%s\n", u.Known, u.Raw, uo)
	n, err := r.Decode(2, []byte(`4242`))
	check(err)
	no, _ := r.Encode(2, n)
	fmt.Printf("unknown number     -> id=%d known=%v re-encoded=%s\n", n.ID, n.Known, no)

	fmt.Println("--- alias withdrawal does not affect the historical snapshot ---")
	fmt.Printf("withdraw v2 alias \"red\": %v\n", r.WithdrawAlias(2, "red"))
	if _, still := r.Lookup(2, "red"); still {
		panic("withdrawn alias should be gone from live graph")
	}
	if id, ok := snap.Lookup("red"); !ok || id != 1 {
		panic("historical snapshot must keep the withdrawn alias")
	}
	fmt.Printf("live v2 \"red\"     -> gone; snapshot v2 \"red\" -> id=1 (frozen)\n")

	fmt.Println("--- v3 makes migration ambiguous instead of silently picking ---")
	check(r.Register(3, 1, "crimson"))
	check(r.Register(3, 2, "green"))
	check(r.Register(3, 3, "scarlet", "red")) // old alias "red" reused by a new ID
	_, err = r.Migrate(1, 3, "red", "items[2].status")
	var amb *verenum.AmbiguityError
	if errors.As(err, &amb) {
		fmt.Printf("migration refused:\n%v\n", err)
	} else {
		panic(fmt.Sprintf("expected ambiguity, got %v", err))
	}

	fmt.Println("--- full minor-version path check ---")
	err = r.VerifyPath([]verenum.Version{1, 2, 3}, "items[2].status")
	var pc *verenum.PathConflictError
	if errors.As(err, &pc) {
		fmt.Printf("path v1->v3 inconsistent: %v\n", err)
	}
	clean := verenum.New()
	check(clean.Register(1, 1, "red"))
	check(clean.Register(2, 1, "crimson", "red"))
	check(clean.VerifyPath([]verenum.Version{1, 2}, "color"))
	fmt.Println("path v1->v2 (clean) -> consistent")
}
