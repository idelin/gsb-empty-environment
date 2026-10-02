package logtmpl

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestFixtureRoundTrip(t *testing.T) {
	tel := &Telemetry{}
	c := New(Config{Deterministic: true}, tel)
	for _, l := range determinismLines {
		c.Add(l)
	}
	c.Finalize()

	var buf bytes.Buffer
	if err := WriteFixture(&buf, tel.Events()); err != nil {
		t.Fatal(err)
	}
	events, err := ReadFixture(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, tel.Events()) {
		t.Fatal("fixture round-trip changed the event stream")
	}
	// Replaying the fixture must reconstruct the live template set.
	got := ReplayTemplates(events)
	want := Signatures(c.Templates())
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed templates %v, want %v", got, want)
	}
}

// TestGoldenFixture replays the committed fixture and checks that a fresh
// deterministic run over testdata/sample.log reproduces it exactly.
func TestGoldenFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/sample.log")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		lines = append(lines, strings.TrimRight(l, "\r"))
	}

	tel := &Telemetry{}
	c := New(Config{Deterministic: true}, tel)
	for _, l := range lines {
		c.Add(l)
	}
	c.Finalize()

	f, err := os.Open("testdata/telemetry.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	golden, err := ReadFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tel.Events(), golden) {
		t.Fatalf("telemetry drifted from fixture: got %d events, fixture has %d\n"+
			"regenerate with: go run ./cmd/logtmpl -fixture-out testdata/telemetry.jsonl testdata/sample.log",
			len(tel.Events()), len(golden))
	}
	want := Signatures(c.Templates())
	if got := ReplayTemplates(golden); !reflect.DeepEqual(got, want) {
		t.Fatalf("fixture replay %v, want %v", got, want)
	}
}
