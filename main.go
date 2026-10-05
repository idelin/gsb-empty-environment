package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	verbose := flag.Bool("v", false, "print full path->id mapping after every step")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: treereplay [-v] <logfile>\n")
		fmt.Fprintf(os.Stderr, "  logfile: JSON-Lines operation log (create/rename/unlink)\n")
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "treereplay: %v\n", err)
		os.Exit(2)
	}
	defer f.Close()

	entries, err := ParseLog(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "treereplay: %v\n", err)
		os.Exit(2)
	}

	replayer := NewReplayer()
	res := replayer.Replay(entries)

	anomalies := 0
	for _, step := range res.Steps {
		var mapping map[string]int64
		if *verbose {
			mapping = step.Mapping
		}
		fmt.Println(FormatStep(step, mapping))
		anomalies += len(step.Notes)
	}

	fmt.Println("---- final identity mapping (path -> stable id) ----")
	fmt.Println(FormatMapping(res.Mapping))

	problems := Verify(entries, res.Mapping)
	fmt.Println("---- independent model verification ----")
	if len(problems) == 0 {
		fmt.Println("tree model and flat model agree")
	} else {
		for _, p := range problems {
			fmt.Println("MISMATCH:", p)
		}
	}

	exit := 0
	if res.Failed {
		fmt.Println("result: some transactions failed (tree kept consistent, see notes)")
		exit = 1
	}
	if anomalies > 0 {
		fmt.Printf("result: %d log anomaly note(s) (duplicates / gaps / recreations)\n", anomalies)
		exit = 1
	}
	if len(problems) > 0 {
		fmt.Println("result: model verification FAILED")
		exit = 1
	}
	os.Exit(exit)
}
