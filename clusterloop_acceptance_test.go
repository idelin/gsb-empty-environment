package bgpsim_test

import (
	"testing"

	bgpsim "prescreen.local/ps-b15ac20600"
)

// TestDualHomedClientTriggersClusterLoopFilter models a client c that has
// direct sessions to two reflectors (rr1 cluster "1" and rr2 cluster "2").
// A route originated at border b travels rr1 -> c -> rr2 and then returns
// to rr1 carrying rr1's own cluster id; rr1 must discard it. The echo from
// the dual-homed client itself is filtered as well.
func TestDualHomedClientTriggersClusterLoopFilter(t *testing.T) {
	cfg := &bgpsim.Config{
		Nodes: []bgpsim.Node{
			{ID: "rr1", RouterID: "10.1.0.1", Reflector: true},
			{ID: "rr2", RouterID: "10.1.0.2", Reflector: true},
			{ID: "b", RouterID: "10.1.1.1", Border: true},
			{ID: "c", RouterID: "10.1.2.1"},
		},
		Clusters: []bgpsim.Cluster{
			{ID: "1", Reflectors: []bgpsim.NodeID{"rr1"}, Clients: []bgpsim.NodeID{"b", "c"}},
			{ID: "2", Reflectors: []bgpsim.NodeID{"rr2"}, Clients: []bgpsim.NodeID{"c"}},
		},
		ExternalRoutes: []bgpsim.Route{
			{Prefix: "203.0.113.0/24", Egress: "b", RouterID: "9.9.9.9", NeighborAS: 65000, MED: 0, LocalPref: 100, ASPathLen: 1, Origin: bgpsim.OriginIGP},
		},
		IGP: map[bgpsim.NodeID]map[bgpsim.NodeID]int{
			"rr1": {"b": 1},
			"rr2": {"b": 2},
			"c":   {"b": 5},
		},
	}

	res, err := bgpsim.Simulate(cfg)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}

	// The client is directly peered with BOTH reflectors.
	if !containsNode(res.Peers["c"], "rr1") || !containsNode(res.Peers["c"], "rr2") {
		t.Fatalf("client peers = %v, want rr1 and rr2", res.Peers["c"])
	}

	t.Logf("status=%s rounds=%d discards=%d", res.Status, len(res.Rounds), len(res.ClusterLoopDiscards))
	for _, ev := range res.ClusterLoopDiscards {
		t.Logf("discard r%d node=%s from=%s cluster=%v", ev.Round, ev.Node, ev.From, ev.ClusterList)
	}

	// The dual-homed client re-advertises rr1's route straight back, so
	// rr1 discards an advertisement received FROM c carrying cluster "1".
	var clientEcho *bgpsim.DiscardEvent
	var viaSecondRR *bgpsim.DiscardEvent
	for i := range res.ClusterLoopDiscards {
		ev := res.ClusterLoopDiscards[i]
		if clientEcho == nil && ev.Node == "rr1" && ev.From == "c" && ev.Reason == bgpsim.DiscardClusterLoop &&
			hasCluster(ev.ClusterList, "1") {
			clientEcho = &ev
		}
		if viaSecondRR == nil && ev.Node == "rr1" && ev.From == "rr2" && hasCluster(ev.ClusterList, "1") && hasCluster(ev.ClusterList, "2") {
			viaSecondRR = &ev
		}
	}
	if clientEcho == nil {
		t.Fatalf("expected rr1 to discard cluster-looped echo from dual-homed client c; events=%+v",
			res.ClusterLoopDiscards)
	}
	if clientEcho.Round != 3 {
		t.Fatalf("client echo discarded at round %d, want synchronous round 3", clientEcho.Round)
	}
	if viaSecondRR == nil {
		t.Fatalf("expected rr1 to discard route reflected through rr2 with clusters [1 2]; events=%+v",
			res.ClusterLoopDiscards)
	}

	// Cluster filtering contains the loop and the prefix still settles on
	// the genuine egress path everywhere.
	if !res.Converged {
		t.Fatalf("dual-homing case should converge, status=%s osc=%+v", res.Status, res.Oscillation)
	}
	if got := res.BestRoute("rr2", "203.0.113.0/24"); got == nil || got.Egress != "b" {
		t.Fatalf("rr2 best = %v, want egress b", got)
	}
}

func containsNode(list []bgpsim.NodeID, want bgpsim.NodeID) bool {
	for _, n := range list {
		if n == want {
			return true
		}
	}
	return false
}

func hasCluster(list []bgpsim.ClusterID, want bgpsim.ClusterID) bool {
	for _, c := range list {
		if c == want {
			return true
		}
	}
	return false
}
