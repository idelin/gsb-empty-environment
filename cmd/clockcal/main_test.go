package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func solveJSON(t *testing.T, doc string) *Report {
	t.Helper()
	var in Input
	if err := json.NewDecoder(strings.NewReader(doc)).Decode(&in); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rep, err := Solve(&in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return rep
}

func TestEndToEnd(t *testing.T) {
	rep := solveJSON(t, `{
	  "channels": [
	    {
	      "name": "A",
	      "calibration": [
	        {"id": "a0", "local": "0", "global": {"min": "10", "max": "12"}},
	        {"id": "a1", "local": "100", "global": {"min": "110", "max": "114"}}
	      ],
	      "events": [
	        {"id": "evA1", "local": "50"},
	        {"id": "evA2", "local": "150"}
	      ]
	    },
	    {
	      "name": "B",
	      "calibration": [
	        {"id": "b0", "local": "0", "global": {"min": "60", "max": "61"}},
	        {"id": "b2", "local": "0", "global": {"min": "121/2", "max": "62"}},
	        {"id": "b1", "local": "10", "global": {"min": "70", "max": "71"}},
	        {"id": "bad", "local": "0", "global": {"min": "1000", "max": "1001"}}
	      ],
	      "events": [
	        {"id": "evB1", "local": "5"}
	      ]
	    }
	  ]
	}`)

	if len(rep.Channels) != 2 {
		t.Fatalf("channels: %+v", rep.Channels)
	}
	chA := rep.Channels[0]
	if chA.Status != "ok" {
		t.Fatalf("channel A status %q", chA.Status)
	}
	if chA.Drift.Min != "49/50" || chA.Drift.Max != "26/25" {
		t.Fatalf("channel A drift %+v", chA.Drift)
	}
	if chA.OffsetIntMin != "10" || chA.OffsetIntMax != "12" {
		t.Fatalf("channel A int offsets %v..%v", chA.OffsetIntMin, chA.OffsetIntMax)
	}

	chB := rep.Channels[1]
	if chB.Status != "resolved_by_dropping" {
		t.Fatalf("channel B status %q", chB.Status)
	}
	if len(chB.Dropped) != 1 || chB.Dropped[0].ID != "bad" {
		t.Fatalf("channel B dropped %+v", chB.Dropped)
	}
	if len(chB.Used) != 3 {
		t.Fatalf("channel B used %v", chB.Used)
	}

	// Events: evA1 in [60,63], evB1 in [131/2,66], evA2 in [159,166].
	var evA1, evB1, evA2 *EventReport
	for i := range rep.Events {
		switch rep.Events[i].ID {
		case "evA1":
			evA1 = &rep.Events[i]
		case "evB1":
			evB1 = &rep.Events[i]
		case "evA2":
			evA2 = &rep.Events[i]
		}
	}
	if evA1.Global.Min != "60" || evA1.Global.Max != "63" {
		t.Fatalf("evA1 %+v", evA1.Global)
	}
	if evB1.Global.Min != "131/2" || evB1.Global.Max != "66" {
		t.Fatalf("evB1 %+v", evB1.Global)
	}
	if evA2.Global.Min != "159" || evA2.Global.Max != "166" {
		t.Fatalf("evA2 %+v", evA2.Global)
	}

	// Partial order: evA1 (max 63) < evB1 (min 65); both < evA2 (min 160).
	// evA1 vs evB1 intervals do not overlap, so the edge must exist.
	found := map[string]bool{}
	for _, e := range rep.PartialOrder.Edges {
		found[e.Before+"->"+e.After] = true
	}
	if !found["evA1->evB1"] || !found["evB1->evA2"] {
		t.Fatalf("edges %+v", rep.PartialOrder.Edges)
	}
	if found["evA1->evA2"] {
		t.Fatalf("transitive edge must be reduced: %+v", rep.PartialOrder.Edges)
	}
	if len(rep.PartialOrder.Incomparable) != 0 {
		t.Fatalf("unexpected incomparable %+v", rep.PartialOrder.Incomparable)
	}
}

func TestIncomparablePreserved(t *testing.T) {
	rep := solveJSON(t, `{
	  "channels": [
	    {
	      "name": "A",
	      "calibration": [
	        {"id": "a0", "local": "0", "global": {"min": "0", "max": "100"}}
	      ],
	      "events": [{"id": "x", "local": "0"}]
	    },
	    {
	      "name": "B",
	      "calibration": [
	        {"id": "b0", "local": "0", "global": {"min": "40", "max": "60"}}
	      ],
	      "events": [{"id": "y", "local": "0"}]
	    }
	  ]
	}`)
	// x in [0,100], y in [40,60]: overlapping, must remain incomparable and
	// must NOT be ordered by midpoints.
	if len(rep.PartialOrder.Edges) != 0 {
		t.Fatalf("no order may be inferred: %+v", rep.PartialOrder.Edges)
	}
	if len(rep.PartialOrder.Incomparable) != 1 {
		t.Fatalf("expected one incomparable pair: %+v", rep.PartialOrder.Incomparable)
	}
}
