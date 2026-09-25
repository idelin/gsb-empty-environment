// Command demo walks the segment state machine through normal commits, an
// injected crash with recovery, orphan collection, and corruption detection.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"segstore"
)

func must(err error, context string) {
	if err != nil {
		log.Fatalf("%s: %v", context, err)
	}
}

func show(s *segstore.Store) {
	data, err := s.Read()
	must(err, "read")
	fmt.Printf("  manifest version : %d\n", s.Manifest().Version)
	fmt.Printf("  current payload  : %q\n", string(data))
}

func ls(dir, title string) {
	fmt.Printf("%s\n", title)
	entries, err := os.ReadDir(dir)
	must(err, "readdir")
	for _, e := range entries {
		fmt.Printf("  %s\n", e.Name())
	}
}

func main() {
	dir := flag.String("dir", "./demo-data", "store directory (caller specified)")
	fault := flag.String("fault", "after_publish", "fault point for the simulated crash")
	flag.Parse()

	must(os.RemoveAll(*dir), "clean demo dir")
	must(os.MkdirAll(*dir, 0o755), "create demo dir")

	// 1) Two normal commits; old segments are reclaimed automatically.
	s, err := segstore.Open(*dir, nil)
	must(err, "open")
	must(s.Commit([]byte("generation-1")), "commit gen1")
	must(s.Commit([]byte("generation-2")), "commit gen2")
	fmt.Println("[1] after two commits:")
	show(s)
	ls(*dir, "  files:")

	// 2) Commit generation-3 with an injected crash. The fault is armed
	//    once, then the in-memory store is discarded (process death model).
	crashPoint := segstore.FaultPoint(*fault)
	armed := true
	crashed, err := segstore.Open(*dir, func(fp segstore.FaultPoint) error {
		if armed && fp == crashPoint {
			armed = false
			return segstore.ErrInjectedFault
		}
		return nil
	})
	must(err, "open for crashing commit")
	fmt.Printf("\n[2] committing gen-3 with crash injected at %q...\n", crashPoint)
	if err := crashed.Commit([]byte("generation-3")); err != nil {
		fmt.Printf("  commit returned error (expected): %v\n", err)
	}
	ls(*dir, "  on-disk state after crash:")

	// 3) Restart: Open rolls the journal forward automatically.
	fmt.Println("\n[3] new process opens the same directory:")
	restarted, err := segstore.Open(*dir, nil)
	if err != nil {
		// before_build aborts the unacknowledged txn instead; retry once.
		if errors.Is(err, segstore.ErrCorrupt) {
			log.Fatalf("unexpected corruption: %v", err)
		}
	}
	if restarted.Manifest().Version != 3 {
		fmt.Printf("  in-flight txn was aborted before publish (version=%d); retrying\n",
			restarted.Manifest().Version)
		must(restarted.Commit([]byte("generation-3")), "retry gen3")
	}
	show(restarted)
	ls(*dir, "  files:")

	// 4) Orphan files are reclaimed purely by reference.
	junk := filepath.Join(*dir, "seg-000777.dat")
	must(os.WriteFile(junk, []byte("orphan"), 0o644), "write orphan")
	tmp := filepath.Join(*dir, "stray.tmp")
	must(os.WriteFile(tmp, []byte("tmp"), 0o644), "write tmp")
	removed, err := restarted.CollectOrphans()
	must(err, "collect orphans")
	fmt.Printf("\n[4] CollectOrphans removed (unreferenced by manifest/journal): %v\n", removed)

	// 5) A manifest pointing at a missing segment is corruption, not a
	//    silent rollback.
	must(os.Remove(filepath.Join(*dir, "seg-000003.dat")), "delete current segment")
	_, err = segstore.Open(*dir, nil)
	fmt.Printf("\n[5] open after losing the referenced segment:\n  %v\n", err)
}
