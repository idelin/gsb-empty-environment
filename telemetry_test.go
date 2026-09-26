package main

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestSynthesizeDeterministic(t *testing.T) {
	a := Synthesize(42, 9)
	b := Synthesize(42, 9)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed must produce identical events")
	}
	c := Synthesize(43, 9)
	if reflect.DeepEqual(a, c) {
		t.Fatal("different seeds should produce different events")
	}
}

func TestEventsRoundTrip(t *testing.T) {
	events := Synthesize(7, 6)
	var buf bytes.Buffer
	if err := WriteEvents(&buf, events); err != nil {
		t.Fatal(err)
	}
	got, err := ReadEvents(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, events) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, events)
	}
}

func TestReplayMergesCompatibleAndReportsLimits(t *testing.T) {
	events := []Event{
		{Name: "latency_ms", Bounds: []float64{5, 10, 20, 50, 100}, Counts: []int64{3, 8, 15, 30, 40}},
		{Name: "latency_ms", Bounds: []float64{10, 50, 100}, Counts: []int64{5, 20, 35}},
		{Name: "payload_bytes", Bounds: []float64{64, 128, 256, 512}, Counts: []int64{2, 6, 9, 12}},
		{Name: "payload_bytes", Bounds: []float64{100, 200, 300}, Counts: []int64{1, 4, 7}},
	}
	res := Replay(events)

	lat := res.Merged["latency_ms"]
	want := Histogram{Bounds: []float64{10, 50, 100}, Counts: []int64{13, 50, 75}}
	if !reflect.DeepEqual(lat, want) {
		t.Fatalf("latency_ms: got %+v, want %+v", lat, want)
	}

	if !errors.Is(res.Errors["payload_bytes"], ErrIncompatible) {
		t.Fatalf("payload_bytes should be refused, got %v", res.Errors["payload_bytes"])
	}
	r, ok := res.Ranges["payload_bytes"]
	if !ok {
		t.Fatal("payload_bytes should have limits recorded")
	}
	// Totals are exact: 12 + 7 = 19.
	last := len(r.Max) - 1
	if r.Min[last] != 19 || r.Max[last] != 19 {
		t.Fatalf("total limits should be [19,19], got [%d,%d]", r.Min[last], r.Max[last])
	}
}

func TestReplayFlagsDecreasingCounts(t *testing.T) {
	events := []Event{
		{Name: "bad", Bounds: []float64{1, 2}, Counts: []int64{5, 4}},
	}
	res := Replay(events)
	if !errors.Is(res.Errors["bad"], ErrCountDecrease) {
		t.Fatalf("expected ErrCountDecrease, got %v", res.Errors["bad"])
	}
	if _, ok := res.Merged["bad"]; ok {
		t.Fatal("invalid event must not be merged")
	}
}

func TestReplayFixture(t *testing.T) {
	events, err := LoadFixture("fixtures/events.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	res := Replay(events)

	lat := res.Merged["latency_ms"]
	want := Histogram{Bounds: []float64{10, 50, 100}, Counts: []int64{13, 50, 75}}
	if !reflect.DeepEqual(lat, want) {
		t.Fatalf("latency_ms: got %+v, want %+v", lat, want)
	}
	if !errors.Is(res.Errors["payload_bytes"], ErrIncompatible) {
		t.Fatalf("payload_bytes should be refused, got %v", res.Errors["payload_bytes"])
	}
}
