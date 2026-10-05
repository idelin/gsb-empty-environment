// Command logreasm reads fragments as JSON lines from stdin and prints
// record change events, followed by a final ordered dump.
//
// Input line:  {"source":"app1","epoch":1,"seq":0,"data":"2026-01-01T00:00:00Z boot\n"}
// Output line: {"kind":"upsert","record":{...}} or {"kind":"retract","record":{...}}
// Final line:  {"kind":"final","records":[...],"dups":N,"conflicts":[...]}
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"logreasm"
)

type wireFrag struct {
	Source string `json:"source"`
	Epoch  uint64 `json:"epoch"`
	Seq    uint64 `json:"seq"`
	Data   string `json:"data"`
}

type finalEvent struct {
	Kind      string              `json:"kind"`
	Records   []logreasm.Record   `json:"records"`
	Dups      int                 `json:"dups"`
	Conflicts []logreasm.Conflict `json:"conflicts"`
}

func main() {
	maxLine := flag.Int("max-line", logreasm.DefaultMaxLineLen, "maximum line length in bytes")
	flag.Parse()

	r := logreasm.NewReassembler(logreasm.WithMaxLineLen(*maxLine))
	enc := json.NewEncoder(os.Stdout)
	in := bufio.NewReader(os.Stdin)
	lineNo := 0
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			lineNo++
			var wf wireFrag
			if jerr := json.Unmarshal(line, &wf); jerr != nil {
				fmt.Fprintf(os.Stderr, "line %d: %v\n", lineNo, jerr)
				os.Exit(2)
			}
			events := r.Ingest(logreasm.Fragment{
				Source: wf.Source,
				Epoch:  wf.Epoch,
				Seq:    wf.Seq,
				Data:   []byte(wf.Data),
			})
			for _, ev := range events {
				if eerr := enc.Encode(ev); eerr != nil {
					fmt.Fprintf(os.Stderr, "encode: %v\n", eerr)
					os.Exit(1)
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "read: %v\n", err)
			os.Exit(1)
		}
	}
	conflicts := r.Conflicts
	if conflicts == nil {
		conflicts = []logreasm.Conflict{}
	}
	records := r.Records()
	if records == nil {
		records = []logreasm.Record{}
	}
	if err := enc.Encode(finalEvent{
		Kind:      "final",
		Records:   records,
		Dups:      r.Dups,
		Conflicts: conflicts,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}
