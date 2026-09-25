// Command demo exercises the idedup store end to end against a
// caller-specified directory, including an injected crash.
//
// Usage: go run ./cmd/demo [dir]   (default ./demo-data)
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	"idedup"
)

func main() {
	dir := "./demo-data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	if err := os.RemoveAll(dir); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("store directory: %s\n\n", dir)

	s, err := idedup.Open(dir, idedup.Options{MinRetain: 2})
	must(err)

	biz := func(id string) func() []byte {
		return func() []byte {
			fmt.Printf("  [business] executing %s\n", id)
			return []byte("result-of-" + id)
		}
	}

	fmt.Println("1) first submit of op-001")
	r, err := s.Execute("op-001", []byte(`{"amount":100}`), biz("op-001"))
	must(err)
	fmt.Printf("   result: %s\n", r)

	fmt.Println("2) retry op-001 with identical content (timeout retry)")
	r, err = s.Execute("op-001", []byte(`{"amount":100}`), biz("op-001"))
	must(err)
	fmt.Printf("   result: %s (business function did NOT re-run)\n", r)

	fmt.Println("3) same ID, different bytes -> conflict")
	_, err = s.Execute("op-001", []byte(`{"amount":200}`), biz("op-001"))
	fmt.Printf("   err: %v\n", err)

	fmt.Println("4) two clients race the same ID concurrently")
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.Execute("op-002", []byte("payload-2"), biz("op-002"))
			if err != nil {
				panic(err)
			}
			fmt.Printf("   client %d got: %s\n", i, r)
		}(i)
	}
	wg.Wait()
	fmt.Printf("   business records so far: %d\n", len(s.BusinessLog()))

	fmt.Println("5) inject crash after commit of op-003, before the response")
	s.SetFaultHook(func(point string) error {
		if point == idedup.PointBeforeRespond {
			return errors.New("power loss")
		}
		return nil
	})
	_, err = s.Execute("op-003", []byte("payload-3"), biz("op-003"))
	fmt.Printf("   caller saw: %v\n", err)
	s.SetFaultHook(nil)

	fmt.Println("6) recover (reopen) and retry op-003 -> original result")
	s, err = idedup.Open(dir, idedup.Options{MinRetain: 2})
	must(err)
	r, err = s.Execute("op-003", []byte("payload-3"), biz("op-003"))
	must(err)
	fmt.Printf("   result: %s, business records: %d (still exactly one per ID)\n", r, len(s.BusinessLog()))

	fmt.Println("7) more traffic, then caller confirms a watermark")
	for _, id := range []string{"op-004", "op-005", "op-006"} {
		if _, err := s.Execute(id, []byte("p-"+id), biz(id)); err != nil {
			log.Fatal(err)
		}
	}
	must(s.ConfirmWatermark("op-004"))
	fmt.Println("   confirmed watermark: op-004")

	fmt.Println("8) reclaim: needs BOTH the watermark and the retention rule")
	n, err := s.Reclaim()
	must(err)
	fmt.Printf("   reclaimed %d entries (MinRetain=2 protects the newest 2)\n", n)
	fmt.Printf("   reclaimed watermark: %s, survivors: %d\n", s.ReclaimedWatermark(), len(s.BusinessLog()))

	fmt.Println("9) retry of an expired ID -> explicit ErrExpired, never re-executed")
	_, err = s.Execute("op-001", []byte(`{"amount":100}`), biz("op-001"))
	fmt.Printf("   err: %v\n", err)

	fmt.Println("10) restart: recovery replays without re-appending")
	must(s.Close())
	s, err = idedup.Open(dir, idedup.Options{MinRetain: 2})
	must(err)
	defer s.Close()
	fmt.Printf("   business log after restart (%d records):\n", len(s.BusinessLog()))
	for _, e := range s.BusinessLog() {
		fmt.Printf("     %s -> %s\n", e.ID, e.Result)
	}
	fmt.Printf("   reclaimed watermark: %s, reclaimed total: %d\n", s.ReclaimedWatermark(), s.ReclaimedCount())
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
