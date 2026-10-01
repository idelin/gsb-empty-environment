// Command evverify runs a self-contained verification demo: it synthesizes
// an evidence bundle locally, stages the chunks incrementally, commits the
// versioned manifest, and prints the report plus the locally synthesized
// telemetry. No external services are involved.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"evidence-verifier/verifier"
)

func main() {
	tamper := flag.String("tamper", "none", "none|reference|digest|missing|timerange|empty")
	state := flag.String("state", "", "verifier state dir (default: fresh temp dir)")
	tele := flag.String("telemetry", "", "also write telemetry JSONL to this file")
	flag.Parse()

	dir := *state
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "evverify-*")
		if err != nil {
			fatal(err)
		}
	}
	sink := &verifier.SliceEmitter{}
	var em verifier.Emitter = sink
	if *tele != "" {
		fe, err := verifier.NewFileEmitter(*tele)
		if err != nil {
			fatal(err)
		}
		defer fe.Close()
		em = &teeEmitter{a: em, b: fe}
	}

	v, err := verifier.Open(dir, em)
	if err != nil {
		fatal(err)
	}

	chunks, m := verifier.Synthesize(verifier.Tamper(*tamper))
	fmt.Printf("== staging %d chunk(s) for version %s ==\n", len(chunks), m.Version)
	for _, c := range chunks {
		if err := v.StageChunk(c); err != nil {
			fmt.Printf("chunk %s rejected: %v\n", c.Name, err)
		}
	}

	fmt.Printf("== committing manifest %s (digest %s) ==\n", m.Version, m.Digest()[:12])
	rep, err := v.Commit(m)
	if err != nil {
		fmt.Printf("commit rejected: %v\n", err)
		os.Exit(1)
	}
	out, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(out))

	fmt.Println("== telemetry (locally synthesized) ==")
	for _, e := range sink.Events() {
		fmt.Printf("#%02d %-15s %-4s %-14s %s\n", e.Seq, e.Type, e.Version, e.File, e.Detail)
	}
	fmt.Printf("state dir: %s\n", filepath.Clean(dir))
	if rep.Status != "sealed" {
		os.Exit(1)
	}
}

type teeEmitter struct{ a, b verifier.Emitter }

func (t *teeEmitter) Emit(e verifier.Event) { t.a.Emit(e); t.b.Emit(e) }

func fatal(err error) { fmt.Fprintln(os.Stderr, "error:", err); os.Exit(2) }
