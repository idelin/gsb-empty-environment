package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0167"
)

func ExampleExecute() {
	f := netsec.NewFunction("example")
	entry := f.NewBlock("entry")
	obj := entry.Alloc("obj", 16, "allocate")
	p := entry.Copy(obj, "root")
	sp := entry.Safepoint("safe point")
	entry.Dereference(p, "use root")
	entry.Return(netsec.NoValue, "return")

	maps := []netsec.StackMap{{
		At: sp,
		Entries: []netsec.StackMapEntry{{
			Location: netsec.Slot(0),
			Value:    p,
		}},
	}}

	fmt.Println(netsec.CheckStackMaps(f, maps).Valid)
	run := netsec.Execute(f, maps, netsec.DefaultRunConfig())
	fmt.Println(run.Status)
	// Output:
	// true
	// safe
}
