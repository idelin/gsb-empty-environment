// Demo of the segment lifecycle state machine: a normal publish, a crash
// injected mid-publish, crash recovery, orphan reclamation, and corruption
// detection. Run with: go run ./cmd/demo -dir /tmp/segdemo
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	segmentfsm "example.com/segmentfsm"
)

func listFiles(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func show(f *segmentfsm.FSM, label string) {
	records, err := f.ReadAll()
	if err != nil {
		log.Fatalf("read: %v", err)
	}
	fmt.Printf("[%s] generation=%d segments=%v records=%v\n", label, f.Generation(), f.Segments(), records)
	fmt.Printf("           files on disk: %v\n", listFiles(f.Dir()))
}

func main() {
	dir := flag.String("dir", filepath.Join(os.TempDir(), "segmentfsm-demo"), "data directory")
	crashAt := flag.String("crash-at", string(segmentfsm.FaultPublishRename), "fault point to crash at")
	flag.Parse()
	os.RemoveAll(*dir)

	fmt.Printf("== data dir: %s\n\n", *dir)

	// 1. Normal publish of generation 1.
	f, err := segmentfsm.Open(*dir)
	if err != nil {
		log.Fatal(err)
	}
	if err := f.Ingest([]string{"apple", "banana"}); err != nil {
		log.Fatal(err)
	}
	show(f, "gen 1 published")

	// 2. Start generation 2 but crash (panic, skips all cleanup) mid-way.
	crashed := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(segmentfsm.SimulatedCrash); ok {
					crashed = true
					fmt.Printf("\n*** process crashed at %q ***\n", *crashAt)
					fmt.Printf("    files left behind: %v\n", listFiles(*dir))
					return
				}
				panic(r)
			}
		}()
		f2, err := segmentfsm.Open(*dir, segmentfsm.WithInjector(crashOnceAt(segmentfsm.FaultPoint(*crashAt))))
		if err != nil {
			log.Fatal(err)
		}
		if err := f2.Ingest([]string{"cherry"}); err != nil {
			log.Fatal(err)
		}
	}()
	if !crashed {
		log.Fatal("expected a simulated crash")
	}

	// 3. Reopen: recovery finishes the interrupted operation from the journal.
	f3, err := segmentfsm.Open(*dir)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println()
	show(f3, "after recovery")

	// 4. Orphan reclamation: plant garbage, reopen, watch it disappear.
	os.WriteFile(filepath.Join(*dir, "seg-000999.dat"), []byte("orphan\n"), 0o644)
	os.WriteFile(filepath.Join(*dir, "seg-000003.dat.tmp"), []byte("torn"), 0o644)
	fmt.Printf("\nplanted orphans:    %v\n", listFiles(*dir))
	f4, err := segmentfsm.Open(*dir)
	if err != nil {
		log.Fatal(err)
	}
	show(f4, "orphans reclaimed")

	// 5. Corruption: delete a segment the manifest references.
	corruptDir := *dir + "-corrupt"
	os.RemoveAll(corruptDir)
	fc, err := segmentfsm.Open(corruptDir)
	if err != nil {
		log.Fatal(err)
	}
	if err := fc.Ingest([]string{"precious"}); err != nil {
		log.Fatal(err)
	}
	os.Remove(filepath.Join(corruptDir, "seg-000001.dat"))
	if _, err := segmentfsm.Open(corruptDir); err != nil {
		fmt.Printf("\nmissing referenced segment -> Open reports: %v\n", err)
		fmt.Println("(no silent fallback to an empty/older state: acknowledged data is never dropped quietly)")
	}
}

func crashOnceAt(p segmentfsm.FaultPoint) segmentfsm.Injector {
	fired := false
	return func(pt segmentfsm.FaultPoint) error {
		if pt == p && !fired {
			fired = true
			panic(segmentfsm.SimulatedCrash{Point: pt})
		}
		return nil
	}
}
