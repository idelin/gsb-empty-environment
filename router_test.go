package noderouter

import (
	"math/rand"
	"testing"
)

// Acceptance scenario: after the weight update 10 -> 80 has been gossiped,
// routing with "zone=us-east" must return the healthy node with the
// highest (latest) weight.
func TestRouteZoneUSEastReturnsHighestWeight(t *testing.T) {
	c := NewCluster(rand.New(rand.NewSource(7)))
	type spec struct {
		id      string
		zone    string
		weight  int
		role    Role
		healthy bool
	}
	specs := []spec{
		{"a", "us-east", 10, RoleWorker, true},
		{"b", "us-east", 50, RoleWorker, true},
		{"c", "us-east", 90, RoleWorker, false}, // unhealthy: must be excluded
		{"d", "us-west", 95, RoleWorker, true},  // wrong zone: must be excluded
		{"e", "us-east", 30, RoleMaster, true},
	}
	lists := map[string]*MemberList{}
	var all []NodeState
	for _, sp := range specs {
		ml := NewMemberList(sp.id)
		ml.Join("127.0.0.1:8000", Metadata{
			Zone: sp.zone, Weight: sp.weight, Role: sp.role, Healthy: sp.healthy,
		}, map[string]string{"disk": "ssd"})
		c.Add(ml)
		lists[sp.id] = ml
		self, _ := ml.Self()
		all = append(all, self)
	}
	lists["a"].Merge(all)
	if _, ok := c.Converge(3, 20); !ok {
		t.Fatal("membership did not converge")
	}

	// Dynamic update: b's weight 50 -> 80, disseminated via gossip.
	lists["b"].UpdateLocal(func(meta *Metadata) { meta.Weight = 80 })
	if _, ok := c.Converge(3, 20); !ok {
		t.Fatal("weight update did not converge")
	}

	// Every node's local router must agree on the answer.
	for id, ml := range lists {
		r := NewRouter(ml)
		best, err := r.Route("zone=us-east")
		if err != nil {
			t.Fatalf("router on %s: %v", id, err)
		}
		if best.ID != "b" {
			t.Fatalf("router on %s picked %s, want b (weight 80)", id, best.ID)
		}
		if best.Meta.Weight != 80 {
			t.Fatalf("router on %s sees stale weight %d, want 80", id, best.Meta.Weight)
		}
	}
}

func TestRouteFiltersByRoleAndLabel(t *testing.T) {
	ml := NewMemberList("self")
	ml.Join("127.0.0.1:9000", Metadata{Zone: "us-east", Weight: 5, Role: RoleMaster, Healthy: true}, nil)
	ml.Merge([]NodeState{
		{ID: "w1", Meta: Metadata{Zone: "us-east", Weight: 40, Role: RoleWorker, Healthy: true}, Labels: map[string]string{"disk": "hdd"}, Version: 1},
		{ID: "w2", Meta: Metadata{Zone: "us-east", Weight: 60, Role: RoleWorker, Healthy: true}, Labels: map[string]string{"disk": "ssd"}, Version: 1},
		{ID: "w3", Meta: Metadata{Zone: "us-east", Weight: 70, Role: RoleWorker, Healthy: true}, Labels: map[string]string{"disk": "ssd"}, Version: 1},
	})
	r := NewRouter(ml)

	best, err := r.Route("zone=us-east,role=worker,disk=ssd")
	if err != nil {
		t.Fatal(err)
	}
	if best.ID != "w3" {
		t.Fatalf("picked %s, want w3 (highest weight among ssd workers)", best.ID)
	}

	if _, err := r.Route("zone=eu-central"); err != ErrNoRoute {
		t.Fatalf("empty subset: got err=%v, want ErrNoRoute", err)
	}
	if _, err := r.Route("zone"); err == nil {
		t.Fatal("malformed selector accepted")
	}
}

func TestSubsetOrderingAndHealth(t *testing.T) {
	ml := NewMemberList("self")
	ml.Join("127.0.0.1:9000", Metadata{Zone: "us-east", Weight: 10, Role: RoleWorker, Healthy: true}, nil)
	ml.Merge([]NodeState{
		{ID: "x", Meta: Metadata{Zone: "us-east", Weight: 30, Healthy: true}, Version: 1},
		{ID: "y", Meta: Metadata{Zone: "us-east", Weight: 20, Healthy: true}, Version: 1},
		{ID: "z", Meta: Metadata{Zone: "us-east", Weight: 99, Healthy: false}, Version: 1},
	})
	r := NewRouter(ml)
	sub, err := r.Subset("zone=us-east")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 3 {
		t.Fatalf("subset size = %d, want 3 (unhealthy z excluded)", len(sub))
	}
	if sub[0].ID != "x" || sub[1].ID != "y" || sub[2].ID != "self" {
		t.Fatalf("subset not ordered by weight desc: %v", []string{sub[0].ID, sub[1].ID, sub[2].ID})
	}
}
