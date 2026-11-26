package bgpsim_test

import (
	"encoding/json"
	"strings"
	"testing"

	bgpsim "prescreen.local/ps-b15ac20600"
)

func TestSimulateValidation(t *testing.T) {
	if _, err := bgpsim.Simulate(nil); err == nil {
		t.Fatal("nil config should error")
	}
	if _, err := bgpsim.Simulate(&bgpsim.Config{}); err == nil {
		t.Fatal("empty config should error")
	}

	bad := &bgpsim.Config{
		Nodes: []bgpsim.Node{
			{ID: "rr", Reflector: true, ClusterID: "1"},
			{ID: "b", Border: true},
		},
		ExternalRoutes: []bgpsim.Route{{Prefix: "p", Egress: "ghost"}},
	}
	if _, err := bgpsim.Simulate(bad); err == nil ||
		!strings.Contains(err.Error(), "unknown egress") {
		t.Fatalf("want unknown egress error, got %v", err)
	}

	missingIGP := &bgpsim.Config{
		Nodes: []bgpsim.Node{
			{ID: "rr", Reflector: true, ClusterID: "1"},
			{ID: "b", Border: true},
		},
		Clusters:       []bgpsim.Cluster{{ID: "1", Reflectors: []bgpsim.NodeID{"rr"}, Clients: []bgpsim.NodeID{"b"}}},
		ExternalRoutes: []bgpsim.Route{{Prefix: "p", Egress: "b", RouterID: "1.1.1.1"}},
	}
	if _, err := bgpsim.Simulate(missingIGP); err == nil ||
		!strings.Contains(err.Error(), "missing IGP") {
		t.Fatalf("want missing IGP error, got %v", err)
	}
}

func TestResultJSONIsServiceFriendly(t *testing.T) {
	res, err := bgpsim.Simulate(triangleConfig(false))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	s := string(raw)
	for _, want := range []string{
		`"status":"oscillating"`,
		`"period":2`,
		`"egress":"b1"`,
		`"incomparability_eliminated":true`,
		`"cluster_loop_discards"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("JSON missing %s\n%s", want, truncate(s, 400))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
