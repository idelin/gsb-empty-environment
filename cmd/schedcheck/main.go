// Command schedcheck reads a scheduling scenario as JSON from stdin (or a
// file given as the first argument) and writes the evaluation as JSON.
//
// Input:
//
//	{
//	  "positions": [{"id": "guard", "requiredQuals": ["license"]}],
//	  "shifts":    [{"id": "s1", "employee": "amy", "position": "guard", "start": 1320, "end": 1800}],
//	  "events":    [{"seq": 1, "kind": "issue", "cert": "c1", "employee": "amy", "qual": "license", "start": 0, "end": 1440}],
//	  "queries":   [{"name": "after-hire", "asOfSeq": 2, "shifts": ["s1"]}]
//	}
//
// Events are applied in the given order; for each one the command reports
// the affected historical shifts computed via the interval index and via
// the full-scan reference. Each query then evaluates shifts using only
// events with seq < asOfSeq. An empty "shifts" list means all shifts.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"schedcheck/sched"
)

type query struct {
	Name    string   `json:"name"`
	AsOfSeq int      `json:"asOfSeq"`
	Shifts  []string `json:"shifts"`
}

type input struct {
	Positions []sched.Position `json:"positions"`
	Shifts    []sched.Shift    `json:"shifts"`
	Events    []sched.Event    `json:"events"`
	Queries   []query          `json:"queries"`
}

type eventEffect struct {
	Seq             int      `json:"seq"`
	Kind            string   `json:"kind"`
	Affected        []string `json:"affected"`
	AffectedByScan  []string `json:"affectedFullScan"`
	IndexMatchesRef bool     `json:"indexMatchesFullScan"`
}

type queryResult struct {
	Name    string             `json:"name"`
	AsOfSeq int                `json:"asOfSeq"`
	Reports []sched.Report     `json:"reports"`
	ByQual  []sched.QualImpact `json:"missingByQual"`
}

type output struct {
	EventEffects []eventEffect `json:"eventEffects"`
	Queries      []queryResult `json:"queries"`
}

func run(r io.Reader, w io.Writer) error {
	var in input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return fmt.Errorf("decoding input: %w", err)
	}
	engine, err := sched.NewEngine(in.Positions, in.Shifts)
	if err != nil {
		return err
	}

	var out output
	for _, ev := range in.Events {
		scan, err := engine.AffectedByFullScan(ev)
		if err != nil {
			return err
		}
		affected, err := engine.AddEvent(ev)
		if err != nil {
			return err
		}
		out.EventEffects = append(out.EventEffects, eventEffect{
			Seq:             ev.Seq,
			Kind:            string(ev.Kind),
			Affected:        affected,
			AffectedByScan:  scan,
			IndexMatchesRef: equalStrings(affected, scan),
		})
	}
	for _, q := range in.Queries {
		ids := q.Shifts
		if len(ids) == 0 {
			ids = engine.ShiftIDs()
		}
		res := queryResult{Name: q.Name, AsOfSeq: q.AsOfSeq}
		for _, id := range ids {
			rep, err := engine.Check(id, q.AsOfSeq)
			if err != nil {
				return err
			}
			res.Reports = append(res.Reports, rep)
		}
		res.ByQual = sched.AggregateByQual(res.Reports)
		out.Queries = append(out.Queries, res)
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	var r io.Reader = os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "schedcheck:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	}
	if err := run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "schedcheck:", err)
		os.Exit(1)
	}
}
