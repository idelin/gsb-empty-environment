// Command demo walks through the acceptance scenario: item X with
// versions v1(t=1), v2(t=5), v3(t=10), a global minimum active read
// view at t=8, and a GC pass that prunes v1 while reads keep working.
package main

import (
	"fmt"
	"strings"

	"mvcc"
)

func printChain(s *mvcc.Store, key string) {
	var parts []string
	for _, ts := range s.Versions(key) {
		val, _ := s.GetAt(key, ts)
		parts = append(parts, fmt.Sprintf("[t=%-2d %s]", ts, val))
	}
	fmt.Printf("chain %s (newest -> oldest): %s\n", key, strings.Join(parts, " -> "))
}

func mustGet(rv *mvcc.ReadView, key string) {
	val, ok := rv.Get(key)
	if !ok {
		fmt.Printf("read %s @t=%-2d -> <not found>\n", key, rv.TS())
		return
	}
	fmt.Printf("read %s @t=%-2d -> %s\n", key, rv.TS(), val)
}

func main() {
	s := mvcc.NewStore()

	fmt.Println("=== writes ===")
	s.WriteAt("X", "v1", 1)
	s.WriteAt("X", "v2", 5)
	s.WriteAt("X", "v3", 10)
	printChain(s, "X")

	fmt.Println("\n=== active read views ===")
	rvOld := s.BeginReadAt(8) // long-running reader: global minimum
	rvNew := s.BeginRead()    // snapshot at latest commit, t=10
	min, _ := s.MinActiveRead()
	fmt.Printf("ReadView A @ t=%d (long-running reader)\n", rvOld.TS())
	fmt.Printf("ReadView B @ t=%d\n", rvNew.TS())
	fmt.Printf("global minimum active read view: t=%d\n", min)

	fmt.Println("\n=== reads before GC ===")
	mustGet(rvOld, "X")
	mustGet(rvNew, "X")

	fmt.Println("\n=== GC ===")
	fmt.Printf("pruned %d obsolete version(s)\n", s.GC())
	printChain(s, "X")

	fmt.Println("\n=== reads after GC ===")
	mustGet(rvOld, "X")
	mustGet(rvNew, "X")

	rvOld.Close()
	rvNew.Close()
}
