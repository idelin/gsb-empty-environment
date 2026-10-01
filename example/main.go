// Command example simulates two distributed replicas of an AW-LWW-Map and
// demonstrates the acceptance scenario: a concurrent root-key remove vs. a
// nested field update, where the update wins.
package main

import (
	"fmt"

	"awlwwmap/awlwwmap"
)

func main() {
	// --- Common ancestor state, replicated to both nodes ---
	a := awlwwmap.New("A")
	a.Set("delin", "user_profile", "name")
	a.Set(30, "user_profile", "age")
	a.Set("dark", "settings", "theme")

	b := awlwwmap.New("B")
	b.Merge(a)

	fmt.Println("== replicated state on both nodes ==")
	fmt.Print(a)

	// --- Concurrent, causally-unaware operations ---
	fmt.Println("== concurrent ops ==")
	fmt.Println("A: Remove(user_profile)          // delete the root key")
	fmt.Println("B: Set(user_profile.age = 25)    // update a nested field")
	a.Remove("user_profile")
	b.Set(25, "user_profile", "age")

	fmt.Println("\n-- node A before merge --")
	fmt.Print(a)
	fmt.Println("-- node B before merge --")
	fmt.Print(b)

	// --- Merge both ways: replicas converge, update wins ---
	a.Merge(b)
	b.Merge(a)

	fmt.Println("== after merge (both nodes converge) ==")
	fmt.Print(a)

	age, _ := a.Get("user_profile", "age")
	name, _ := a.Get("user_profile", "name")
	fmt.Printf("user_profile survived: %v, age=%v name=%v\n",
		a.Has("user_profile"), age, name)
}
