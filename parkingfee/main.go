package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

type eventJSON struct {
	Time string `json:"time"`
	Type string `json:"type"`
}

type inputJSON struct {
	Events         []eventJSON `json:"events"`
	FreeMinutes    int64       `json:"free_minutes"`
	UnitMinutes    int64       `json:"unit_minutes"`
	UnitPriceCents int64       `json:"unit_price_cents"`
	DailyCapCents  int64       `json:"daily_cap_cents"` // 0 or omitted: no cap
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "parkingfee: "+format+"\n", args...)
	os.Exit(2)
}

func main() {
	in := flag.String("in", "", "input JSON file (default: stdin)")
	flag.Parse()

	var data []byte
	var err error
	if *in == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(*in)
	}
	if err != nil {
		fatal("read input: %v", err)
	}

	var inp inputJSON
	if err := json.Unmarshal(data, &inp); err != nil {
		fatal("parse input JSON: %v", err)
	}

	events := make([]Event, len(inp.Events))
	for i, e := range inp.Events {
		t, err := time.Parse(time.RFC3339, e.Time)
		if err != nil {
			fatal("event %d: bad time %q (want RFC3339): %v", i, e.Time, err)
		}
		events[i] = Event{Time: t, Type: EventType(e.Type)}
	}
	rules := Rules{
		Free:      time.Duration(inp.FreeMinutes) * time.Minute,
		Unit:      time.Duration(inp.UnitMinutes) * time.Minute,
		UnitPrice: inp.UnitPriceCents,
		DailyCap:  inp.DailyCapCents,
	}

	res := Solve(events, rules)
	out, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fatal("encode result: %v", err)
	}
	fmt.Println(string(out))
}
