package mountview

import "testing"

func TestPolicyIssueAndFineGrainedRevocation(t *testing.T) {
	f := newFixture(t)
	iv1 := Interval{Offset: 0, Len: 16}
	iv2 := Interval{Offset: 16, Len: 16}
	f.manifest("", "k1", 1, "obj", iv1, ro(), Private, 0)
	f.manifest("", "k2", 2, "obj", iv2, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{
		binding("k1", 1, "obj", iv1, ro(), Private, 0),
		binding("k2", 2, "obj", iv2, ro(), Private, 0),
	}, nil, nil)

	e := NewEngine()
	policy := NewPolicy()
	if err := policy.SetCeiling(PolicyKey{Object: "obj", Interval: iv1}, ro()); err != nil {
		t.Fatal(err)
	}
	if err := policy.SetCeiling(PolicyKey{Object: "obj", Interval: iv2}, ro()); err != nil {
		t.Fatal(err)
	}
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	store := NewCredentialStore(policy)
	r1, _ := e.Resolve("k1")
	r2, _ := e.Resolve("k2")
	c1, err := store.Issue(r1)
	if err != nil {
		t.Fatalf("issue k1: %v", err)
	}
	c2, err := store.Issue(r2)
	if err != nil {
		t.Fatalf("issue k2: %v", err)
	}

	if err := policy.SetCeiling(PolicyKey{Object: "obj", Interval: iv1}, Perm{}); err != nil {
		t.Fatalf("tighten: %v", err)
	}
	n := store.RevokeKey(PolicyKey{Object: "obj", Interval: iv1})
	if n != 1 {
		t.Fatalf("want 1 revoked credential, got %d", n)
	}
	if got, _ := store.Get(c1.ID); !got.Revoked {
		t.Fatal("c1 must be revoked")
	}
	if got, _ := store.Get(c2.ID); got.Revoked {
		t.Fatal("c2 must remain valid: no whole-tree clear")
	}
	if active := store.Active(); len(active) != 1 || active[0].ID != c2.ID {
		t.Fatalf("unexpected active set: %v", active)
	}
	if err := policy.SetCeiling(PolicyKey{Object: "obj", Interval: iv1}, ro()); err == nil {
		t.Fatal("relaxing a tightened policy must be rejected")
	}
}

func TestOverPrivilegeIsRejectedNotUnknown(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 8}
	f.manifest("", "p", 1, "obj-p", iv, rw(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("p", 1, "obj-p", iv, rw(), Private, 0)}, nil, nil)
	e := NewEngine()
	policy := NewPolicy()
	if err := policy.SetCeiling(PolicyKey{Object: "obj-p", Interval: iv}, ro()); err != nil {
		t.Fatal(err)
	}
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	res, err := e.Evaluate("p", policy)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if res.Verdict != Rejected {
		t.Fatalf("deterministic write above read-only ceiling must be Rejected, got %s", res.Verdict)
	}
	if _, err := NewCredentialStore(policy).Issue(res); err == nil {
		t.Fatal("issuing an over-privileged credential must fail")
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 8}
	f.manifest("", "z", 1, "obj-z", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("z", 1, "obj-z", iv, ro(), Private, 0)}, nil, nil)
	genesis.Signature[0] ^= 0xFF

	e := NewEngine()
	err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		Verify:      VerifierFromKey(f.signer.Public()),
	})
	if err == nil {
		t.Fatal("tampered checkpoint must fail verification")
	}
}

func TestPathEscapeRejected(t *testing.T) {
	if _, err := CleanPath("../outside"); err == nil {
		t.Fatal("path escaping the view root must be rejected")
	}
	if _, err := CleanPath("a/../../b"); err == nil {
		t.Fatal("cross-boundary relative escape must be rejected")
	}
	cp, err := CleanPath("./a//b/../c")
	if err != nil || cp != "a/c" {
		t.Fatalf("normalisation wrong: %q %v", cp, err)
	}
}
