// Command demo exercises the pathindex store: building a tree,
// atomically moving subtrees, rejecting cycles, detecting sibling
// conflicts, surviving injected crashes, and keeping IDs stable
// across renames.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"pathindex"
)

func main() {
	dir := flag.String("dir", filepath.Join(os.TempDir(), "pathindex-demo"),
		"directory where the index files are stored")
	flag.Parse()
	os.RemoveAll(*dir) // fresh demo run

	s, err := pathindex.Open(*dir)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("store dir: %s (version %d)\n\n", *dir, s.Version())

	must := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}
	show := func(ids ...string) {
		for _, id := range ids {
			p, err := s.Path(id)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Printf("  %-4s -> %s\n", id, p)
		}
	}

	fmt.Println("== create tree: /proj/{src,docs}, /proj/src/main ==")
	must(s.Create("PROJ", "", "proj"))
	must(s.Create("SRC", "PROJ", "src"))
	must(s.Create("DOCS", "PROJ", "docs"))
	must(s.Create("MAIN", "SRC", "main"))
	must(s.Create("ARCHIVE", "", "archive"))
	show("PROJ", "SRC", "DOCS", "MAIN", "ARCHIVE")

	fmt.Println("\n== atomic subtree move: PROJ under ARCHIVE ==")
	must(s.Move("PROJ", "ARCHIVE"))
	show("PROJ", "SRC", "MAIN")

	fmt.Println("\n== reject moving A into its own subdirectory B ==")
	if err := s.Move("PROJ", "SRC"); err != nil {
		fmt.Printf("  rejected as expected: %v\n", err)
	}
	show("PROJ", "SRC")

	fmt.Println("\n== batch move with sibling conflict ==")
	must(s.Create("OTHER", "", "other"))
	must(s.Create("DUP", "OTHER", "src")) // same name as SRC, different parent: fine
	err = s.MoveBatch([]pathindex.MoveOp{
		{ID: "DUP", NewParent: "PROJ"}, // would collide with SRC
		{ID: "MAIN", NewParent: "DOCS"},
	})
	fmt.Printf("  batch rejected: %v\n", err)
	fmt.Println("  all paths unchanged after failed batch:")
	show("DUP", "MAIN")

	fmt.Println("\n== injected crash before commit ==")
	s.FailAt = pathindex.FailAfterTmpWrite
	err = s.Move("MAIN", "DOCS")
	fmt.Printf("  move aborted: %v\n", err)
	s.FailAt = pathindex.FailOff
	show("MAIN")

	fmt.Println("\n== reopen after crash: recovery keeps last committed state ==")
	s2, err := pathindex.Open(*dir)
	if err != nil {
		log.Fatal(err)
	}
	s = s2
	fmt.Printf("  recovered version %d\n", s.Version())
	show("PROJ", "MAIN")

	fmt.Println("\n== rename keeps the ID stable ==")
	must(s.Rename("PROJ", "project"))
	show("PROJ", "SRC", "MAIN")

	fmt.Println("\n== final state persisted ==")
	fmt.Printf("  version %d, state file: %s\n", s.Version(),
		filepath.Join(*dir, "state.json"))
}
