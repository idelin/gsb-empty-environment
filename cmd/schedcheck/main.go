// Command schedcheck reads synthetic scheduling data (positions, shifts,
// certificate/revocation events, queries) as JSON and prints, per query
// horizon, the schedulability verdicts, minimal gap intervals per missing
// qualification, and the affected shift IDs.
//
// Usage:
//
//	schedcheck -input data.json
//
// All times are integer minutes; intervals are [start, end).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	"schedcheck/sched"
)

type eventJSON struct {
	Kind          string `json:"kind"` // "issue" or "revoke"
	Employee      string `json:"employee"`
	Qualification string `json:"qualification"`
	Start         int64  `json:"start"`
	End           int64  `json:"end"`
}

type queryJSON struct {
	Name    string `json:"name"`
	Horizon int    `json:"horizon"`
}

type inputJSON struct {
	Positions []sched.Position `json:"positions"`
	Shifts    []sched.Shift    `json:"shifts"`
	Events    []eventJSON      `json:"events"`
	Queries   []queryJSON      `json:"queries"`
}

type queryResult struct {
	Name     string              `json:"name"`
	Horizon  int                 `json:"horizon"`
	Results  []sched.ShiftResult `json:"results"`
	Affected []string            `json:"affected"`
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "schedcheck: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	inputPath := flag.String("input", "", "path to input JSON file")
	flag.Parse()
	if *inputPath == "" {
		fail("missing -input flag")
	}
	raw, err := ioutil.ReadFile(*inputPath)
	if err != nil {
		fail("read input: %v", err)
	}
	var in inputJSON
	if err := json.Unmarshal(raw, &in); err != nil {
		fail("parse input: %v", err)
	}

	store := sched.NewStore(in.Positions, in.Shifts, nil)
	for _, ev := range in.Events {
		var kind sched.Kind
		switch ev.Kind {
		case "issue":
			kind = sched.Issue
		case "revoke":
			kind = sched.Revoke
		default:
			fail("unknown event kind %q", ev.Kind)
		}
		store.AddEvent(sched.Event{
			Kind:          kind,
			Employee:      ev.Employee,
			Qualification: ev.Qualification,
			Start:         ev.Start,
			End:           ev.End,
		})
	}

	out := struct {
		Queries []queryResult `json:"queries"`
	}{}
	for _, q := range in.Queries {
		affected := store.AffectedShifts(q.Horizon)
		if affected == nil {
			affected = []string{}
		}
		out.Queries = append(out.Queries, queryResult{
			Name:     q.Name,
			Horizon:  q.Horizon,
			Results:  store.Evaluate(q.Horizon),
			Affected: affected,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fail("encode output: %v", err)
	}
}
