package repairplan

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderText(t *testing.T) {
	topo, err := buildTestTopology(topologySpec{
		rackCount:     4,
		perRackEgress: map[string]int64{rackName(1): 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	stripes := []Stripe{
		NewReplicatedStripe("pg-a", 3, 100, LevelRack, []ShardPlacement{
			{0, osdName(1, 1, 1)}, {1, osdName(2, 1, 1)},
		}),
		NewReplicatedStripe("pg-b", 3, 100, LevelRack, []ShardPlacement{
			{0, osdName(1, 2, 1)}, {1, osdName(2, 2, 1)},
		}),
	}
	plan := mustPlan(t, topo, stripes, Options{GlobalConcurrent: 10})

	var buf bytes.Buffer
	if err := RenderText(&buf, plan); err != nil {
		t.Fatalf("RenderText failed: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Repair plan:", "DEGRADED", "Round 1", "pg-a", "node=rack1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered plan missing %q:\n%s", want, out)
		}
	}
}
