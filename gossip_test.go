package noderouter

import (
	"fmt"
	"math/rand"
	"testing"
)

// buildCluster creates n nodes, all initially knowing only themselves,
// then gossips until every replica knows every node.
func buildCluster(t *testing.T, n int) (*Cluster, []*MemberList) {
	t.Helper()
	c := NewCluster(rand.New(rand.NewSource(42)))
	lists := make([]*MemberList, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("node-%d", i)
		ml := NewMemberList(id)
		zone := "us-east"
		if i%2 == 1 {
			zone = "us-west"
		}
		ml.Join(fmt.Sprintf("127.0.0.1:%d", 7000+i),
			Metadata{Zone: zone, Weight: 10, Role: RoleWorker, Healthy: true},
			map[string]string{"disk": "ssd"})
		c.Add(ml)
		lists[i] = ml
	}
	// Seed: node-0 learns about everyone (models a join introducer),
	// then gossip spreads the full membership.
	var all []NodeState
	for _, ml := range lists {
		self, _ := ml.Self()
		all = append(all, self)
	}
	lists[0].Merge(all)
	if rounds, ok := c.Converge(3, 20); !ok {
		t.Fatalf("membership did not converge in %d rounds", rounds)
	}
	for _, ml := range lists {
		if ml.Len() != n {
			t.Fatalf("node knows %d members, want %d", ml.Len(), n)
		}
	}
	return c, lists
}

// Acceptance scenario: node-2's load weight changes 10 -> 80 and every
// replica must observe weight 80 after gossip.
func TestGossipPropagatesWeightUpdate(t *testing.T) {
	c, lists := buildCluster(t, 8)

	if _, err := lists[2].UpdateLocal(func(meta *Metadata) {
		meta.Weight = 80
	}); err != nil {
		t.Fatalf("UpdateLocal: %v", err)
	}

	rounds, ok := c.Converge(3, 20)
	if !ok {
		t.Fatalf("weight update did not converge in %d rounds", rounds)
	}
	t.Logf("weight 10->80 converged across 8 nodes in %d gossip round(s)", rounds)

	for i, ml := range lists {
		s, ok := ml.Get("node-2")
		if !ok {
			t.Fatalf("replica %d lost node-2", i)
		}
		if s.Meta.Weight != 80 {
			t.Fatalf("replica %d sees weight %d, want 80", i, s.Meta.Weight)
		}
		if s.Version != 2 {
			t.Fatalf("replica %d sees version %d, want 2", i, s.Version)
		}
	}
}

// A stale replica must not overwrite a fresher state during gossip.
func TestGossipRejectsStaleState(t *testing.T) {
	c, lists := buildCluster(t, 4)

	lists[1].UpdateLocal(func(meta *Metadata) { meta.Weight = 80 })
	if _, ok := c.Converge(3, 20); !ok {
		t.Fatal("no convergence")
	}
	// Inject a stale copy of node-1 (weight 10, version 1) into node-3's
	// outbound view by hand and gossip again: nobody may regress.
	stale, _ := lists[1].Self()
	stale.Meta.Weight = 10
	stale.Version = 1
	for _, ml := range lists {
		if ml == lists[1] {
			continue
		}
		ml.Merge([]NodeState{stale})
	}
	c.GossipRound(3)
	for i, ml := range lists {
		s, _ := ml.Get("node-1")
		if s.Meta.Weight != 80 {
			t.Fatalf("replica %d regressed to weight %d", i, s.Meta.Weight)
		}
	}
}
