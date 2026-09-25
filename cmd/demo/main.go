// Command demo exercises the slot store end to end:
//   go run ./cmd/demo [dir]   (default ./demo-data)
package main

import (
	"fmt"
	"os"

	"slotstore"
)

func main() {
	dir := "./demo-data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir)

	fmt.Printf("== open new store in %s ==\n", dir)
	s, _, err := slotstore.Open(dir, slotstore.Config{SlotCount: 8, PayloadCap: 64})
	must(err)

	h1, err := s.Put([]byte("alpha"))
	must(err)
	h2, err := s.Put([]byte("bravo"))
	must(err)
	h3, err := s.Put([]byte("charlie"))
	must(err)
	fmt.Printf("put: %+v %+v %+v\n", h1, h2, h3)

	fmt.Println("== delete bravo, reuse its slot ==")
	must(s.Delete(h2))
	h4, err := s.Put([]byte("delta"))
	must(err)
	fmt.Printf("old handle %+v -> new handle %+v (same slot, gen+1)\n", h2, h4)
	if _, err := s.Get(h2); err != nil {
		fmt.Printf("stale handle h2 rejected: %v\n", err)
	}
	if b, err := s.Get(h4); err == nil {
		fmt.Printf("new handle h4 reads: %q\n", b)
	}
	must(s.Close())

	fmt.Println("== restart: reopen and recover ==")
	s2, rep, err := slotstore.Open(dir, slotstore.Config{})
	must(err)
	if rep != nil && (len(rep.Repairs) > 0 || len(rep.ScrubbedSlots) > 0) {
		fmt.Printf("recovery repairs: %v scrubbed=%v\n", rep.Repairs, rep.ScrubbedSlots)
	} else {
		fmt.Println("clean shutdown: no repairs needed")
	}
	fmt.Println("live records after restart:")
	s2.ForEach(func(h slotstore.Handle, p []byte) {
		fmt.Printf("  slot=%d gen=%d payload=%q\n", h.Slot, h.Gen, p)
	})
	if _, err := s2.Get(h2); err != nil {
		fmt.Printf("stale handle h2 still rejected after restart: %v\n", err)
	}
	must(s2.Close())
	fmt.Println("== done ==")
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
