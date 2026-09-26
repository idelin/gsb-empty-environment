package main

import (
	"fmt"

	"datrie"
)

func printStats(tr *datrie.Trie) {
	st := tr.Stats()
	fmt.Printf("slots=%d used=%d free=%d words=%d\n", st.Slots, st.Used, st.Free, st.Words)
	fmt.Printf("  base[]  %6d B (%d slots x 4B)\n", st.BaseBytes, st.Slots)
	fmt.Printf("  check[] %6d B (%d slots x 4B)\n", st.CheckBytes, st.Slots)
	fmt.Printf("  value[] %6d B (%d slots x 8B)\n", st.ValueBytes, st.Slots)
	fmt.Printf("  term[]  %6d B (bitset)\n", st.TermBytes)
	fmt.Printf("  total   %6d B (%.1f B/slot)\n", st.TotalBytes, float64(st.TotalBytes)/float64(st.Slots))
}

func main() {
	tr := datrie.New()

	words := []struct {
		k string
		v int64
	}{
		{"he", 1}, {"hear", 2}, {"heard", 3}, {"hello", 4}, {"help", 5},
		{"world", 6}, {"word", 7}, {"中", 8}, {"中文", 9}, {"中国", 10},
	}
	for _, w := range words {
		tr.Insert(w.k, w.v)
	}
	fmt.Println("== inserted 10 words (prefixes he/中 are words themselves) ==")
	printStats(tr)

	fmt.Println("\n== search ==")
	for _, k := range []string{"he", "hello", "hel", "中文"} {
		v, ok := tr.Search(k)
		fmt.Printf("Search(%q) -> %d, %v\n", k, v, ok)
	}

	fmt.Println("\n== duplicate insert updates value in place ==")
	before := tr.Stats()
	tr.Insert("hello", 44)
	after := tr.Stats()
	v, _ := tr.Search("hello")
	fmt.Printf("hello: value=%d, slots %d->%d (no orphan slots)\n", v, before.Slots, after.Slots)

	fmt.Println("\n== prefix traversal ==")
	for _, e := range tr.PrefixSearch("he") {
		fmt.Printf("  %-8s %d\n", e.Key, e.Value)
	}
	for _, e := range tr.PrefixSearch("中") {
		fmt.Printf("  %-8s %d\n", e.Key, e.Value)
	}

	fmt.Println("\n== delete hello (prefix he and sibling help survive) ==")
	tr.Delete("hello")
	_, okHello := tr.Search("hello")
	vHe, okHe := tr.Search("he")
	vHelp, okHelp := tr.Search("help")
	fmt.Printf("hello found=%v, he=%d(%v), help=%d(%v)\n", okHello, vHe, okHe, vHelp, okHelp)
	printStats(tr)
}
