// Example simulates two replicas of an AW-LWW-Map and demonstrates the
// acceptance scenario: a concurrent key removal vs. field update.
package main

import (
	"fmt"
	"sort"

	"awlwwmap"
)

func show(label string, m *awlwwmap.Map) {
	fields, ok := m.Get("user_profile")
	if !ok {
		fmt.Printf("  %-12s user_profile = <deleted>\n", label)
		return
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("  %-12s user_profile = {", label)
	for i, k := range keys {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%s: %v", k, fields[k])
	}
	fmt.Println("}")
}

func main() {
	fmt.Println("AW-LWW-Map demo: concurrent remove vs. update")

	a := awlwwmap.New("A")
	b := awlwwmap.New("B")

	fmt.Println("1. Node A writes user_profile, replicates to B:")
	a.Set("user_profile", "name", "alice")
	a.Set("user_profile", "city", "shanghai")
	b.Merge(a)
	show("node A:", a)
	show("node B:", b)

	fmt.Println("2. Concurrent ops (no sync in between):")
	fmt.Println("   node A removes user_profile")
	fmt.Println("   node B sets user_profile.age = 25")
	a.RemoveKey("user_profile")
	b.Set("user_profile", "age", 25)
	show("node A:", a)
	show("node B:", b)

	fmt.Println("3. Causal merge (both directions) -- update wins:")
	a.Merge(b)
	b.Merge(a)
	show("node A:", a)
	show("node B:", b)

	if v, ok := a.GetField("user_profile", "age"); ok && v == 25 {
		fmt.Println("OK: user_profile preserved with age=25 (add-wins)")
	} else {
		fmt.Println("FAIL: concurrent update was lost")
	}
}
