package fixture

import (
	"bytes"
	"testing"

	"depgraph/internal/pairing"
	"depgraph/internal/telemetry"
)

func TestSynthesizeIsDeterministic(t *testing.T) {
	a := telemetry.Synthesize(42)
	b := telemetry.Synthesize(42)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("synthesis broken: %d vs %d spans", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("span %d differs between runs: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestFixtureRoundTripReplayMatchesDirect(t *testing.T) {
	direct := pairing.Build(fixtureReplay(telemetry.Synthesize(7)))

	var buf bytes.Buffer
	if err := Write(&buf, telemetry.Synthesize(7)); err != nil {
		t.Fatal(err)
	}
	loaded, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	replayed := pairing.Build(Replay(loaded))

	if len(direct.Edges) != len(replayed.Edges) || len(direct.Uncertain) != len(replayed.Uncertain) {
		t.Fatalf("replay mismatch: %+v vs %+v", direct, replayed)
	}
	for i := range direct.Edges {
		if direct.Edges[i] != replayed.Edges[i] {
			t.Fatalf("edge %d differs: %+v vs %+v", i, direct.Edges[i], replayed.Edges[i])
		}
	}
}

func TestReplayOrdersByStartTime(t *testing.T) {
	spans := telemetry.Synthesize(1)
	// Reverse input; replay must restore start-time order.
	for i, j := 0, len(spans)-1; i < j; i, j = i+1, j-1 {
		spans[i], spans[j] = spans[j], spans[i]
	}
	out := Replay(spans)
	for i := 1; i < len(out); i++ {
		if out[i].StartUnixNano < out[i-1].StartUnixNano {
			t.Fatalf("replay not ordered at %d", i)
		}
	}
}

func fixtureReplay(spans []telemetry.Span) []telemetry.Span { return Replay(spans) }
