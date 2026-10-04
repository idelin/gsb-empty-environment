// Command verify simulates a three-replica deployment with a network
// partition and checks that a delete followed by a concurrent re-add on
// the partitioned replica is never swallowed, and that history
// compaction never drops deletion records before the offline replica
// has acknowledged them.
package main

import (
	"fmt"
	"os"

	"orset"
)

var failures int

func check(cond bool, msg string) {
	if cond {
		fmt.Printf("  PASS  %s\n", msg)
	} else {
		fmt.Printf("  FAIL  %s\n", msg)
		failures++
	}
}

func sync(a, b *orset.Set) {
	sa, sb := a.Clone(), b.Clone()
	a.Merge(sb)
	b.Merge(sa)
}

func gossipAll(sets ...*orset.Set) {
	for round := 0; round < 2; round++ {
		for i := 0; i < len(sets); i++ {
			for j := i + 1; j < len(sets); j++ {
				sync(sets[i], sets[j])
			}
		}
	}
}

func newCluster() (a, b, c *orset.Set) {
	return orset.NewSet("A", "B", "C"),
		orset.NewSet("B", "A", "C"),
		orset.NewSet("C", "A", "B")
}

func main() {
	fmt.Println("== Scenario: safe compaction (library default) ==")
	safeRun()

	fmt.Println()
	fmt.Println("== Control: unsafe tombstone drop (must resurrect) ==")
	unsafeRun()

	fmt.Println()
	if failures > 0 {
		fmt.Printf("RESULT: %d check(s) FAILED\n", failures)
		os.Exit(1)
	}
	fmt.Println("RESULT: all checks passed")
}

func safeRun() {
	a, b, c := newCluster()

	fmt.Println("-- A adds x, everyone syncs")
	a.Add("x")
	gossipAll(a, b, c)
	check(a.Contains("x") && b.Contains("x") && c.Contains("x"), "x visible on A, B, C")

	fmt.Println("-- C partitioned; A removes x; A and B sync and try to compact")
	a.Remove("x")
	sync(a, b)
	check(!a.Contains("x") && !b.Contains("x"), "x removed on A and B")
	droppedA, droppedB := a.Compact(), b.Compact()
	check(droppedA == 0 && droppedB == 0, "compaction drops nothing while C is offline")
	check(a.TombstoneCount() == 1 && b.TombstoneCount() == 1, "deletion records retained pending C's ack")

	fmt.Println("-- C concurrently re-adds x behind the partition")
	c.Add("x")

	fmt.Println("-- partition heals; everyone syncs")
	gossipAll(a, b, c)
	check(a.Contains("x") && b.Contains("x") && c.Contains("x"), "concurrent re-add not swallowed: x present on A, B, C")
	check(len(a.LiveTags("x")) == 1 && len(b.LiveTags("x")) == 1 && len(c.LiveTags("x")) == 1,
		"exactly one live tag (C's re-add), old tag not resurrected")
	tags := a.LiveTags("x")
	check(tags[0].Replica == "C", "surviving tag is C's concurrent add")

	fmt.Println("-- acks propagated; compaction now safe")
	check(a.Compact() == 1, "A collects the acknowledged tombstone")
	check(b.Compact() == 1, "B collects the acknowledged tombstone")
	gossipAll(a, b, c)
	c.Compact()
	check(a.Contains("x") && b.Contains("x") && c.Contains("x"), "x still present after compaction")
	check(len(a.LiveTags("x")) == 1, "still exactly one live tag after compaction")
}

func unsafeRun() {
	a, b, c := newCluster()

	a.Add("x")
	gossipAll(a, b, c)

	a.Remove("x")
	sync(a, b)
	fmt.Println("-- A and B unsafely drop deletion records while C is offline")
	a.DropAllTombstones()
	b.DropAllTombstones()

	c.Add("x")
	gossipAll(a, b, c)
	got := len(a.LiveTags("x"))
	fmt.Printf("  live tags for x after heal: %d (%v)\n", got, a.LiveTags("x"))
	check(got == 2, "control run resurrects the deleted tag, proving tombstones are required")
}
