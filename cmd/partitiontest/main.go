// Command partitiontest verifies the orset library under a three-replica
// network partition scenario:
//
//  1. A, B, C start connected; A adds "x" and everyone syncs.
//  2. C is partitioned away (offline).
//  3. A removes "x" while B concurrently re-adds it; A and B sync.
//     The re-add must NOT be swallowed by the remove.
//  4. While C is offline, GC must NOT drop the delete record, because C
//     has not acknowledged it.
//  5. The partition heals; all replicas sync and converge on "x" present,
//     with B's concurrent re-add tag as the surviving tag.
//  6. Only after every replica has acknowledged the remove may GC compact
//     the tombstone.
package main

import (
	"fmt"
	"os"

	"orset"
)

var failed bool

func check(ok bool, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if ok {
		fmt.Printf("  PASS: %s\n", msg)
	} else {
		fmt.Printf("  FAIL: %s\n", msg)
		failed = true
	}
}

func syncAll(replicas ...*orset.Replica) {
	// Two full-mesh rounds so acknowledgements (matrix clock) propagate
	// to every replica.
	for round := 0; round < 2; round++ {
		for i := 0; i < len(replicas); i++ {
			for j := i + 1; j < len(replicas); j++ {
				orset.Sync(replicas[i], replicas[j])
			}
		}
	}
}

func main() {
	cluster := []string{"A", "B", "C"}
	A := orset.New("A", cluster...)
	B := orset.New("B", cluster...)
	C := orset.New("C", cluster...)

	fmt.Println("step 1: A adds \"x\"; full sync across A, B, C")
	A.Add("x")
	syncAll(A, B, C)
	check(A.Contains("x") && B.Contains("x") && C.Contains("x"),
		"all replicas contain \"x\"")

	fmt.Println("step 2: C is partitioned offline")
	fmt.Println("step 3: A removes \"x\" while B concurrently re-adds it; A<->B sync")
	A.Remove("x")
	reAddTag := B.Add("x")
	orset.Sync(A, B)
	check(A.Contains("x"), "A still contains \"x\" (concurrent re-add not swallowed)")
	check(B.Contains("x"), "B contains \"x\"")
	check(len(A.LiveTags("x")) == 1 && A.LiveTags("x")[0] == reAddTag,
		"the surviving tag on A is B's concurrent re-add tag %v", reAddTag)

	fmt.Println("step 4: GC while C is offline must retain the delete record")
	check(A.GC() == 0, "A's GC drops nothing (C has not acknowledged)")
	check(B.GC() == 0, "B's GC drops nothing (C has not acknowledged)")
	check(A.TombstoneCount() == 1, "A retains 1 tombstone, has %d", A.TombstoneCount())
	check(B.TombstoneCount() == 1, "B retains 1 tombstone, has %d", B.TombstoneCount())

	fmt.Println("step 5: partition heals; full sync")
	syncAll(A, B, C)
	check(C.Contains("x"), "C contains \"x\" after healing")
	check(len(C.LiveTags("x")) == 1 && C.LiveTags("x")[0] == reAddTag,
		"C's surviving tag is B's re-add tag, not the stale pre-partition tag")
	check(C.TombstoneCount() == 1, "C received the delete record (1 tombstone)")
	converged := fmt.Sprint(A.Elements()) == fmt.Sprint(B.Elements()) &&
		fmt.Sprint(B.Elements()) == fmt.Sprint(C.Elements())
	check(converged, "all replicas converge on elements %v", A.Elements())

	fmt.Println("step 6: after full acknowledgement, GC compacts history")
	check(A.GC() == 1, "A collects the acknowledged tombstone")
	check(B.GC() == 1, "B collects the acknowledged tombstone")
	check(C.GC() == 1, "C collects the acknowledged tombstone")
	check(A.Contains("x") && B.Contains("x") && C.Contains("x"),
		"\"x\" is still present everywhere after compaction")

	fmt.Println("step 7: post-GC re-sync keeps convergence (no resurrection)")
	syncAll(A, B, C)
	stillConverged := fmt.Sprint(A.Elements()) == fmt.Sprint(B.Elements()) &&
		fmt.Sprint(B.Elements()) == fmt.Sprint(C.Elements())
	check(stillConverged && A.Contains("x"),
		"replicas remain converged with \"x\" present, tombstones=%d/%d/%d",
		A.TombstoneCount(), B.TombstoneCount(), C.TombstoneCount())

	if failed {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS")
}
