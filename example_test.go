package arc_test

import (
	"fmt"

	"arc"
)

// ExampleCache walks a small workload through a capacity-2 cache and
// prints the state after every step: the target size p of the
// first-access queue and all four queues (MRU first).
func ExampleCache() {
	c := arc.New(2)
	show := func(op string) {
		fmt.Printf("%-9s %s\n", op, c.Snapshot())
	}

	c.Set("a", 1)
	show("Set a")
	c.Set("b", 2)
	show("Set b")
	c.Get("a")
	show("Get a")
	c.Set("c", 3) // b is evicted to ghost list B1
	show("Set c")
	c.Set("d", 4) // c follows b into B1
	show("Set d")
	c.Set("c", 5) // ghost hit in B1: target p grows
	show("Set c")
	c.Set("a", 6) // ghost hit in B2: target p shrinks
	show("Set a")
	c.Delete("d") // removes the ghost record left by d
	show("Delete d")

	// Output:
	// Set a     p=0 T1=[a] T2=[] B1=[] B2=[]
	// Set b     p=0 T1=[b a] T2=[] B1=[] B2=[]
	// Get a     p=0 T1=[b] T2=[a] B1=[] B2=[]
	// Set c     p=0 T1=[c] T2=[a] B1=[b] B2=[]
	// Set d     p=0 T1=[d] T2=[a] B1=[c] B2=[]
	// Set c     p=1 T1=[d] T2=[c] B1=[] B2=[a]
	// Set a     p=0 T1=[] T2=[a c] B1=[d] B2=[]
	// Delete d  p=0 T1=[] T2=[a c] B1=[] B2=[]
}
