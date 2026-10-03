// Command demo walks through the life cycle of an Interval Tree Clock:
// one node holding the full interval forks into two, both sides evolve
// concurrently, new nodes join and leave dynamically, and merges restore
// a single clock that preserves everyone's causal history.
package main

import (
	"fmt"

	"itc"
)

func relation(a, b *itc.Stamp) string {
	switch {
	case a.Equal(b):
		return "equal"
	case a.Leq(b):
		return "a happens-before b"
	case b.Leq(a):
		return "b happens-before a"
	default:
		return "CONCURRENT"
	}
}

func main() {
	fmt.Println("=== Interval Tree Clock demo ===")
	fmt.Println()

	fmt.Println("-- 1. One node owns the whole interval [0, 1]")
	root := itc.Seed()
	fmt.Printf("   seed:        %s\n", root)
	root = root.Event()
	fmt.Printf("   root ticks:  %s\n", root)
	fmt.Println()

	fmt.Println("-- 2. Fork: split [0, 1] into two halves, no coordination needed")
	a, b := root.Fork()
	fmt.Printf("   a:           %s\n", a)
	fmt.Printf("   b:           %s\n", b)
	fmt.Println()

	fmt.Println("-- 3. Both sides evolve independently (concurrently)")
	a = a.Event()
	b = b.Event().Event()
	fmt.Printf("   a ticks 1x:  %s\n", a)
	fmt.Printf("   b ticks 2x:  %s\n", b)
	fmt.Printf("   relation:    %s\n", relation(a, b))
	fmt.Println()

	fmt.Println("-- 4. Join: merge both halves, preserving causal history")
	j := itc.Join(a, b)
	fmt.Printf("   join(a, b):  %s  (full interval restored)\n", j)
	fmt.Printf("   a <= join? %v, b <= join? %v\n", a.Leq(j), b.Leq(j))
	j1 := j.Event()
	fmt.Printf("   join ticks:  %s  -> now dominates all prior history: %v, %v\n",
		j1, a.Leq(j1), b.Leq(j1))
	fmt.Println()

	fmt.Println("-- 5. Dynamic topology: a new node c joins by forking a")
	a2, c := a.Fork()
	fmt.Printf("   a forks:     %s\n", a2)
	fmt.Printf("   c (new):     %s\n", c)
	a2 = a2.Event()
	c = c.Event()
	fmt.Printf("   a2 ticks:    %s\n", a2)
	fmt.Printf("   c ticks:     %s\n", c)
	fmt.Printf("   a2 vs c:     %s\n", relation(a2, c))
	fmt.Println()

	fmt.Println("-- 6. c leaves silently, then rejoins later: causality intact")
	m := itc.Join(a2, c)
	fmt.Printf("   join(a2, c): %s\n", m)
	fmt.Printf("   a2 <= m? %v, c <= m? %v\n", a2.Leq(m), c.Leq(m))
	fmt.Printf("   m vs b:      %s (b never heard from a2 or c)\n", relation(m, b))
	full := itc.Join(m, b)
	fmt.Printf("   join all:    %s\n", full)
	fmt.Printf("   every node <= full? a2:%v b:%v c:%v\n",
		a2.Leq(full), b.Leq(full), c.Leq(full))
}
