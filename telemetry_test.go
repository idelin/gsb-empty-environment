package main

import (
	"bytes"
	"os"
	"testing"
)

func runScriptedEngine(t *testing.T, tele *Telemetry) {
	t.Helper()
	e := NewEngine(true, nil, tele)
	if err := e.UpdateRules([]Rule{DeleteLabel{RuleID: "r1", Label: "instance"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.AddSeries(Series{Name: "m", Labels: LabelSet{"instance": "a"}, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if err := e.AddSeries(Series{Name: "m", Labels: LabelSet{"instance": "b"}, Value: 2}); err != nil {
		t.Fatal(err)
	}
	if err := e.RetractRule("r1"); err != nil {
		t.Fatal(err)
	}
}

func TestTelemetryRoundTripReplay(t *testing.T) {
	tele := NewTelemetry()
	runScriptedEngine(t, tele)

	var buf bytes.Buffer
	if err := tele.EncodeJSONL(&buf); err != nil {
		t.Fatal(err)
	}
	events, err := DecodeEvents(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(tele.Events()) {
		t.Fatalf("round trip lost events: %d != %d", len(events), len(tele.Events()))
	}
	for i, e := range events {
		if e.Seq != uint64(i+1) {
			t.Fatalf("event %d seq = %d", i, e.Seq)
		}
	}
	p := Replay(events)
	if p.Added != 1 || p.Merged != 1 || p.Split != 1 || p.Published != 1 || p.Retracted != 1 {
		t.Fatalf("projector = %+v", p)
	}
	if p.CacheStore == 0 {
		t.Fatalf("expected cache stores, projector = %+v", p)
	}
}

func TestFixtureReplay(t *testing.T) {
	f, err := os.Open("testdata/telemetry_fixture.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	events, err := DecodeEvents(f)
	if err != nil {
		t.Fatal(err)
	}
	p := Replay(events)
	if p.Added != 1 || p.Merged != 1 || p.Split != 1 || p.Published != 1 || p.Retracted != 1 {
		t.Fatalf("fixture projector = %+v", p)
	}
}

func TestConflictRejectedEvent(t *testing.T) {
	tele := NewTelemetry()
	e := NewEngine(false, nil, tele)
	if err := e.UpdateRules([]Rule{DeleteLabel{RuleID: "r1", Label: "instance"}}); err != nil {
		t.Fatal(err)
	}
	if err := e.AddSeries(Series{Name: "m", Labels: LabelSet{"instance": "a"}, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if err := e.AddSeries(Series{Name: "m", Labels: LabelSet{"instance": "b"}, Value: 2}); err == nil {
		t.Fatal("want conflict")
	}
	p := Replay(tele.Events())
	if p.Conflicts != 1 {
		t.Fatalf("conflicts = %d", p.Conflicts)
	}
}
