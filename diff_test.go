package netsec

import (
	"fmt"
	"math/rand"
	"testing"
)

func testDomain() Domain {
	return Domain{
		{Name: "subject", Values: []string{"alice", "bob"}},
		{Name: "action", Values: []string{"read", "write"}},
		{Name: "resource", Values: []string{"doc1", "doc2"}},
		{Name: "tag:env", Values: []string{"prod", "dev"}},
	}
}

func rect(pairs ...string) Rect {
	r := Rect{Values: map[string]map[string]bool{}}
	for i := 0; i < len(pairs); i += 2 {
		if r.Values[pairs[i]] == nil {
			r.Values[pairs[i]] = map[string]bool{}
		}
		r.Values[pairs[i]][pairs[i+1]] = true
	}
	return r
}

func TestDenyPriority(t *testing.T) {
	p := &Policy{Rules: []Rule{
		{Name: "allow-all", Effect: Allow, When: FullRect()},
		{Name: "deny-bob", Effect: Deny, When: rect("subject", "bob")},
	}}
	dom := testDomain()
	for _, pt := range dom.Points() {
		got := p.Allows(pt)
		want := pt["subject"] != "bob"
		if got != want {
			t.Fatalf("Allows(%v) = %v, want %v", pt, got, want)
		}
	}
}

func TestDiffBasic(t *testing.T) {
	dom := testDomain()
	oldPol := &Policy{Rules: []Rule{
		{Name: "alice-read-doc1", Effect: Allow, When: rect("subject", "alice", "action", "read", "resource", "doc1")},
	}}
	newPol := &Policy{Rules: []Rule{
		{Name: "alice-read-doc1", Effect: Allow, When: rect("subject", "alice", "action", "read", "resource", "doc1")},
		{Name: "alice-read-doc2", Effect: Allow, When: rect("subject", "alice", "action", "read", "resource", "doc2")},
		{Name: "deny-prod-write", Effect: Deny, When: rect("action", "write", "tag:env", "prod")},
	}}
	res := Diff(newPol, oldPol, dom, 0)
	if !res.Complete {
		t.Fatalf("expected complete result, got remainder %v", res.Remainder)
	}
	if err := Verify(res, newPol, oldPol, dom); err != nil {
		t.Fatal(err)
	}
	// The only newly allowed points are alice/read/doc2/{prod,dev}.
	if len(res.Covered) != 1 {
		t.Fatalf("expected 1 covered rect, got %d: %+v", len(res.Covered), res.Covered)
	}
	got := res.Covered[0]
	want := rect("subject", "alice", "action", "read", "resource", "doc2")
	for _, pt := range dom.Points() {
		if got.Rect.Contains(pt) != want.Contains(pt) {
			t.Fatalf("covered rect mismatch at %v: got %v want %v", pt, got.Rect.Values, want.Values)
		}
	}
	if len(got.GrantedBy) != 1 || got.GrantedBy[0] != "alice-read-doc2" {
		t.Fatalf("GrantedBy = %v, want [alice-read-doc2]", got.GrantedBy)
	}
	if len(got.PreviouslyDeniedBy) != 0 {
		t.Fatalf("PreviouslyDeniedBy = %v, want empty (default deny)", got.PreviouslyDeniedBy)
	}
}

func TestDiffReportsExplicitOldDeny(t *testing.T) {
	dom := testDomain()
	oldPol := &Policy{Rules: []Rule{
		{Name: "deny-dev", Effect: Deny, When: rect("tag:env", "dev")},
	}}
	newPol := &Policy{Rules: []Rule{
		{Name: "open-dev-reads", Effect: Allow, When: rect("action", "read", "tag:env", "dev")},
	}}
	res := Diff(newPol, oldPol, dom, 0)
	if err := Verify(res, newPol, oldPol, dom); err != nil {
		t.Fatal(err)
	}
	if len(res.Covered) == 0 {
		t.Fatal("expected non-empty diff")
	}
	for _, cr := range res.Covered {
		if len(cr.PreviouslyDeniedBy) != 1 || cr.PreviouslyDeniedBy[0] != "deny-dev" {
			t.Fatalf("PreviouslyDeniedBy = %v, want [deny-dev]", cr.PreviouslyDeniedBy)
		}
	}
}

func randomPolicy(rng *rand.Rand, dom Domain, prefix string) *Policy {
	p := &Policy{}
	n := rng.Intn(5)
	for i := 0; i < n; i++ {
		rule := Rule{
			Name:   fmt.Sprintf("%s-r%d", prefix, i),
			Effect: Effect(rng.Intn(2)),
			When:   Rect{Values: map[string]map[string]bool{}},
		}
		for _, dim := range dom {
			if rng.Intn(2) == 0 {
				continue // wildcard
			}
			vals := map[string]bool{}
			for _, v := range dim.Values {
				if rng.Intn(2) == 0 {
					vals[v] = true
				}
			}
			if len(vals) == 0 {
				vals[dim.Values[rng.Intn(len(dim.Values))]] = true
			}
			rule.When.Values[dim.Name] = vals
		}
		p.Rules = append(p.Rules, rule)
	}
	return p
}

func randomDomain(rng *rand.Rand) Domain {
	names := []string{"subject", "action", "resource", "tag:env"}
	dom := Domain{}
	for _, name := range names[:2+rng.Intn(3)] {
		n := 2 + rng.Intn(3)
		vals := make([]string, n)
		for i := range vals {
			vals[i] = fmt.Sprintf("%s-%d", name, i)
		}
		dom = append(dom, Dimension{Name: name, Values: vals})
	}
	return dom
}

func TestDiffExhaustiveRandom(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		dom := randomDomain(rng)
		oldPol := randomPolicy(rng, dom, "old")
		newPol := randomPolicy(rng, dom, "new")
		res := Diff(newPol, oldPol, dom, 0)
		if !res.Complete {
			t.Fatalf("seed %d: unlimited budget must complete", seed)
		}
		if err := Verify(res, newPol, oldPol, dom); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
	}
}

func TestDiffBudgetThenShard(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		rng := rand.New(rand.NewSource(seed))
		dom := randomDomain(rng)
		oldPol := randomPolicy(rng, dom, "old")
		newPol := randomPolicy(rng, dom, "new")

		full := Diff(newPol, oldPol, dom, 0)
		if len(full.Covered) <= 2 {
			continue // nothing interesting to shard
		}
		res := Diff(newPol, oldPol, dom, 2)
		if res.Complete {
			continue // budget happened to suffice
		}
		if err := Verify(res, newPol, oldPol, dom); err != nil {
			t.Fatalf("seed %d: partial: %v", seed, err)
		}
		if len(res.Covered) > 2 {
			t.Fatalf("seed %d: budget exceeded: covered=%d", seed, len(res.Covered))
		}
		if len(res.Remainder) == 0 {
			t.Fatalf("seed %d: incomplete result must carry a remainder", seed)
		}

		// Re-shard: run Diff on each remainder rectangle with the
		// policies clipped to it, then merge.
		merged := Result{Complete: true, Covered: res.Covered}
		for _, shard := range res.Remainder {
			sub := Diff(Clip(newPol, shard), Clip(oldPol, shard), dom, 0)
			if !sub.Complete {
				t.Fatalf("seed %d: shard run incomplete", seed)
			}
			merged.Covered = append(merged.Covered, sub.Covered...)
		}
		if err := Verify(merged, newPol, oldPol, dom); err != nil {
			t.Fatalf("seed %d: merged: %v", seed, err)
		}
		return
	}
	t.Fatal("no seed produced a shardable diff")
}

func TestDiffIdenticalPolicies(t *testing.T) {
	dom := testDomain()
	pol := &Policy{Rules: []Rule{
		{Name: "allow-all", Effect: Allow, When: FullRect()},
		{Name: "deny-bob", Effect: Deny, When: rect("subject", "bob")},
	}}
	res := Diff(pol, pol, dom, 0)
	if !res.Complete || len(res.Covered) != 0 {
		t.Fatalf("identical policies must yield empty diff, got %+v", res)
	}
	if err := Verify(res, pol, pol, dom); err != nil {
		t.Fatal(err)
	}
}
