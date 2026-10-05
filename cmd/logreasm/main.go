// Command logreasm reads fragments as JSON lines from stdin, one per line:
//
//	{"source":"web-1","epoch":1,"seq":0,"data":"2026-01-01T00:00:00Z started\n"}
//
// It prints every event (upsert/retract/warning) as a JSON line, and after
// EOF prints the final ordered record set.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"logreasm"
)

type fragmentIn struct {
	Source string `json:"source"`
	Epoch  uint64 `json:"epoch"`
	Seq    uint64 `json:"seq"`
	Data   string `json:"data"`
}

type gapOut struct {
	MissingAfter  uint64 `json:"missing_after"`
	MissingBefore uint64 `json:"missing_before"`
}

type recordOut struct {
	ID        string   `json:"id"`
	Source    string   `json:"source"`
	Epoch     uint64   `json:"epoch"`
	Version   int      `json:"version"`
	Timestamp string   `json:"timestamp,omitempty"`
	StartLine int      `json:"start_line"`
	EndLine   int      `json:"end_line"`
	Lines     []string `json:"lines"`
	Orphan    bool     `json:"orphan,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
	Gap       *gapOut  `json:"gap_before,omitempty"`
	GapAfter  bool     `json:"gap_after,omitempty"`
}

type eventOut struct {
	Kind   string     `json:"kind"`
	Record *recordOut `json:"record,omitempty"`
	Source string     `json:"source,omitempty"`
	Epoch  uint64     `json:"epoch,omitempty"`
	Reason string     `json:"reason,omitempty"`
}

func toRecordOut(r logreasm.Record) recordOut {
	out := recordOut{
		ID:        r.ID,
		Source:    r.Source,
		Epoch:     r.Epoch,
		Version:   r.Version,
		StartLine: r.StartLine,
		EndLine:   r.EndLine,
		Lines:     r.Lines,
		Orphan:    r.Orphan,
		Truncated: r.Truncated,
		GapAfter:  r.GapAfter,
	}
	if !r.Timestamp.IsZero() {
		out.Timestamp = r.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	if r.Gap != nil {
		out.Gap = &gapOut{MissingAfter: r.Gap.MissingAfter, MissingBefore: r.Gap.MissingBefore}
	}
	return out
}

func main() {
	eng := logreasm.NewEngine()
	enc := json.NewEncoder(os.Stdout)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		var in fragmentIn
		if err := json.Unmarshal(sc.Bytes(), &in); err != nil {
			fmt.Fprintf(os.Stderr, "line %d: %v\n", lineNo, err)
			os.Exit(1)
		}
		events := eng.Ingest(logreasm.Fragment{
			Source: in.Source,
			Epoch:  in.Epoch,
			Seq:    in.Seq,
			Data:   []byte(in.Data),
		})
		for _, ev := range events {
			out := eventOut{Kind: ev.Kind.String(), Source: ev.Source, Epoch: ev.Epoch, Reason: ev.Reason}
			if ev.Kind != logreasm.EventWarning {
				rec := toRecordOut(ev.Record)
				out.Record = &rec
			}
			if err := enc.Encode(out); err != nil {
				fmt.Fprintf(os.Stderr, "encode: %v\n", err)
				os.Exit(1)
			}
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "read: %v\n", err)
		os.Exit(1)
	}
	final := eng.Records()
	out := make([]recordOut, 0, len(final))
	for _, r := range final {
		out = append(out, toRecordOut(r))
	}
	if err := enc.Encode(map[string]any{"final": out}); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}
