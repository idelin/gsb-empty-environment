package main

import (
	"fmt"

	"datrie"
)

func main() {
	tr := datrie.New()

	words := []struct {
		key string
		val int64
	}{
		{"app", 1}, {"apple", 2}, {"application", 3}, {"apply", 4},
		{"banana", 5}, {"band", 6}, {"bandana", 7},
		{"he", 8}, {"hello", 9},
	}
	for _, w := range words {
		tr.Put(w.key, w.val)
	}
	tr.Put("app", 100) // duplicate insert: updates value in place

	fmt.Println("== lookups ==")
	for _, k := range []string{"app", "apple", "he", "hello", "appl", "cat"} {
		if v, ok := tr.Get(k); ok {
			fmt.Printf("Get(%-12q) = %d\n", k, v)
		} else {
			fmt.Printf("Get(%-12q) = <missing>\n", k)
		}
	}

	fmt.Println("\n== prefix traversal Collect(\"app\") ==")
	for _, e := range tr.Collect("app") {
		fmt.Printf("%-12q -> %d\n", e.Key, e.Value)
	}

	fmt.Println("\n== delete ==")
	fmt.Println("Delete(\"hello\"):", tr.Delete("hello"))
	fmt.Println("Delete(\"hello\"):", tr.Delete("hello"))
	if _, ok := tr.Get("he"); ok {
		fmt.Println("prefix word \"he\" still present")
	}

	st := tr.Stats()
	fmt.Println("\n== memory layout ==")
	fmt.Printf("keys=%d usedSlots=%d capacity=%d slots\n", st.Keys, st.UsedSlots, st.Slots)
	fmt.Printf("base  []int32 : %d bytes\n", st.BaseBytes)
	fmt.Printf("check []int32 : %d bytes\n", st.CheckBytes)
	fmt.Printf("value []int64 : %d bytes\n", st.ValueBytes)
	fmt.Printf("hasVal []bool : %d bytes\n", st.FlagBytes)
	fmt.Printf("total         : %d bytes\n", st.TotalBytes)
}
