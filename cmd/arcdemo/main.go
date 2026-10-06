// Command arcdemo runs a scripted ARC session and prints the cache state
// after every step.
package main

import (
	"fmt"

	"arccache"
)

func main() {
	c := arc.New(3)
	step := 0
	show := func(op string) {
		step++
		fmt.Printf("step %2d  %-16s -> %s\n", step, op, c.State())
	}
	set := func(k string, v int) { c.Set(k, v); show(fmt.Sprintf("Set %s=%d", k, v)) }
	get := func(k string) {
		v, ok := c.Get(k)
		show(fmt.Sprintf("Get %s=(%v,%v)", k, v, ok))
	}
	del := func(k string) { show(fmt.Sprintf("Delete %s=%v", k, c.Delete(k))) }

	fmt.Println("ARC demo: capacity=3, entry-count accounting, target p clamped to [0,3]")
	show("init")

	set("a", 1)
	set("b", 2)
	set("c", 3)
	get("a")     // second touch: a promoted T1 -> T2
	set("d", 4)  // replace: LRU of T1 (b) becomes a ghost in B1
	set("b", 20) // ghost hit in B1: target p rises, b reinstated into T2
	set("e", 5)  // replace: LRU of T2 (a) becomes a ghost in B2
	set("a", 10) // ghost hit in B2: target p falls, a reinstated into T2
	set("f", 6)  // T1+B1 full: oldest ghost c dropped, e demoted to B1
	set("d", 40) // ghost hit in B1: target p rises again
	get("zz")    // miss, no state change
	del("d")
	del("d")  // repeated delete is a safe no-op
	del("zz") // deleting an unknown key is a no-op too
}
