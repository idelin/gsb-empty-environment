// Command kvimport is a demo CLI for the kvimport package.
//
//	kvimport -dir OUTDIR [-in FILE] [-batch N] [-fail-at POINT -fail-after N]
//
// -fail-at injects a simulated crash at a named fault point (see the
// kvimport package docs), which is useful to demonstrate crash recovery:
// run once with -fail-at, then re-run the same command without it to resume.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"kvimport"
)

func main() {
	dir := flag.String("dir", "", "output directory holding all state and the published store.json (required)")
	in := flag.String("in", "", "input file of JSON lines (default: stdin)")
	batch := flag.Int("batch", 1000, "good records per confirmed batch")
	failAt := flag.String("fail-at", "", "inject a crash at this fault point (afterDataSync, afterCheckpoint, beforePublish, afterPublishRename)")
	failAfter := flag.Int("fail-after", 1, "fail on the Nth occurrence of the fault point")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "error: -dir is required")
		flag.Usage()
		os.Exit(2)
	}

	var input io.Reader = os.Stdin
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		input = f
	}

	calls := 0
	opts := kvimport.Options{Dir: *dir, BatchSize: *batch}
	if *failAt != "" {
		opts.Inject = func(point string, b int) error {
			if point == *failAt {
				calls++
				if calls == *failAfter {
					return errors.New("simulated crash")
				}
			}
			return nil
		}
	}

	res, err := kvimport.Import(input, opts)
	if err != nil {
		log.Fatalf("import failed: %v", err)
	}
	fmt.Printf("import ok (resumed=%v): records=%d rejects=%d keys=%d offset=%d\n",
		res.Resumed, res.Records, res.Rejects, res.Keys, res.Offset)
	fmt.Printf("digest=%s\npublished=%s\n", res.Digest, res.StorePath)
}
