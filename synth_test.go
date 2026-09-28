package spandep

import (
	"bytes"
	"strings"
	"testing"
)

func TestSynthesizeIsDeterministic(t *testing.T) {
	a := Synthesize(42)
	b := Synthesize(42)
	if len(a) != len(b) {
		t.Fatalf("same seed must give same span count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("span %d differs between runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestSynthesizedScenarioSemantics(t *testing.T) {
	g := Build(Synthesize(1))

	// web->api: one ok pair + one error pair.
	st := g.Edges[EdgeKey{Caller: "web", Callee: "api"}]
	if st == nil || st.Calls != 2 || st.OK != 1 || st.Err != 1 {
		t.Fatalf("web->api edge wrong: %+v", st)
	}
	// web->billing: retried call, 1 error attempt + 1 ok attempt.
	st = g.Edges[EdgeKey{Caller: "web", Callee: "billing"}]
	if st == nil || st.Calls != 2 || st.OK != 1 || st.Err != 1 {
		t.Fatalf("web->billing retry edge wrong: %+v", st)
	}
	// Duplicate export of one span dropped.
	if g.DuplicatesDropped != 1 {
		t.Fatalf("expected 1 duplicate dropped, got %d", g.DuplicatesDropped)
	}
	// Uncertain: lonely client, lonely server, missing-service pair.
	if len(g.Uncertain) != 3 {
		t.Fatalf("expected 3 uncertain evidence entries, got %d: %v", len(g.Uncertain), g.Uncertain)
	}
}

func TestFixtureRoundTrip(t *testing.T) {
	spans := Synthesize(7)
	var buf bytes.Buffer
	if err := WriteFixture(&buf, 7, spans); err != nil {
		t.Fatal(err)
	}
	fx, err := LoadFixture(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if fx.Seed != 7 || len(fx.Spans) != len(spans) {
		t.Fatalf("round trip mismatch: seed=%d spans=%d", fx.Seed, len(fx.Spans))
	}
	for i := range spans {
		if fx.Spans[i] != spans[i] {
			t.Fatalf("span %d changed in round trip", i)
		}
	}
}

func TestLoadFixtureRejectsInvalid(t *testing.T) {
	cases := []string{
		`{"spans":[{"traceId":"","spanId":"a","kind":"client","status":"ok"}]}`,
		`{"spans":[{"traceId":"t","spanId":"a","kind":"weird","status":"ok"}]}`,
		`{"spans":[{"traceId":"t","spanId":"a","kind":"client","status":"bogus"}]}`,
		`not json`,
	}
	for _, c := range cases {
		if _, err := LoadFixture(strings.NewReader(c)); err == nil {
			t.Fatalf("expected error for %s", c)
		}
	}
}
