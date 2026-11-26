package mountview

import "testing"

func TestConcreteMoveHardlinkSliceChmod(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 32}
	f.manifest("", "d", 1, "obj-d", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("d", 1, "obj-d", iv, ro(), Private, 0)}, nil, nil)

	mv := f.entry(OpMove, func(e *LogEntry) { e.Path = "d"; e.NewPath = "d2" })
	hl := f.entry(OpHardlink, func(e *LogEntry) { e.Path = "d2"; e.NewPath = "alias" })
	sl := f.entry(OpSlice, func(e *LogEntry) {
		e.Path = "alias"
		e.Interval = Interval{Offset: 8, Len: 8}
	})
	cm := f.entry(OpChmod, func(e *LogEntry) { e.Path = "d2"; e.Perm = rw() })

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		Entries:     []*LogEntry{mv, hl, sl, cm},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	// Both handles share one hardlink group, so slice and chmod are visible
	// through either alias.
	for _, target := range []string{"d2", "alias"} {
		res, err := e.Resolve(target)
		if err != nil {
			t.Fatalf("resolve %s: %v", target, err)
		}
		if res.Verdict != Recoverable {
			t.Fatalf("%s: want Recoverable, got %s (%s)", target, res.Verdict, res.Reason)
		}
		if res.Intervals[0] != (Interval{Offset: 8, Len: 8}) {
			t.Fatalf("%s: shared slice not observed: %v", target, res.Intervals)
		}
		if !res.Perms[0].Write {
			t.Fatalf("%s: shared chmod not observed: %v", target, res.Perms)
		}
	}
	if _, err := e.Resolve("d"); err != nil {
		t.Fatalf("resolve old path: %v", err)
	}
}

func TestConcreteSymlinkResolution(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 16}
	f.manifest("", "real", 1, "obj-real", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("real", 1, "obj-real", iv, ro(), Private, 0)}, nil, nil)
	ln := f.entry(OpSymlink, func(e *LogEntry) { e.Path = "link"; e.Target = "real" })

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		Entries:     []*LogEntry{ln},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	res, err := e.Resolve("link")
	if err != nil {
		t.Fatalf("resolve link: %v", err)
	}
	if res.Verdict != Recoverable || res.Objects[0] != "obj-real" {
		t.Fatalf("symlink must resolve deterministically: %+v", res)
	}
}

func TestSharedPropagationDomainWidensBlastRadius(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 16}
	f.manifest("", "a", 1, "obj-a", iv, ro(), Shared, 9)
	f.manifest("", "b", 2, "obj-b", iv, ro(), Shared, 9)
	f.manifest("", "c", 3, "obj-c", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{
		binding("a", 1, "obj-a", iv, ro(), Shared, 9),
		binding("b", 2, "obj-b", iv, ro(), Shared, 9),
		binding("c", 3, "obj-c", iv, ro(), Private, 0),
	}, nil, nil)

	// A bind-mount gap rooted at a/; because a is shared in domain 9, b (same
	// domain) is causally inside the blast radius. Private c is not.
	f.openNextGap(map[OpType]bool{OpBindMount: true}, GapCandidates{
		Paths:     []string{"a"},
		Objects:   []ObjectID{"obj-a", "obj-evil"},
		Intervals: []Interval{iv},
		Perms:     []Perm{ro()},
		Domains:   []DomainID{9},
	})
	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		OpenGaps:    f.openGaps(),
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if b, err := e.Resolve("b"); err != nil || b.Verdict != Unknown {
		t.Fatalf("shared-domain sibling must be Unknown: verdict=%v err=%v", b, err)
	}
	if c, err := e.Resolve("c"); err != nil || c.Verdict != Recoverable {
		t.Fatalf("private unrelated branch must stay Recoverable: verdict=%v err=%v", c, err)
	}
}

func TestPolicyOverlappingCeilingAndRevoke(t *testing.T) {
	policy := NewPolicy()
	big := Interval{Offset: 0, Len: 100}
	sub := Interval{Offset: 10, Len: 10}
	if err := policy.SetCeiling(PolicyKey{Object: "o", Interval: big}, ro()); err != nil {
		t.Fatal(err)
	}
	if ceiling := policy.Ceiling("o", sub); ceiling.Write || !ceiling.Read {
		t.Fatalf("overlapping ceiling should propagate read-only, got %+v", ceiling)
	}
	if ceiling := policy.Ceiling("o", Interval{Offset: 200, Len: 4}); ceiling.Read {
		t.Fatal("non-overlapping interval must default to deny-all")
	}

	store := NewCredentialStore(policy)
	cred := &Credential{Object: "o", Interval: sub, Perm: ro()}
	issued, err := store.Issue(&TargetResult{
		Verdict: Recoverable, Path: "x",
		Objects: []ObjectID{"o"}, Intervals: []Interval{sub}, Perms: []Perm{ro()},
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	_ = cred
	if n := store.RevokeOverlapping(PolicyKey{Object: "o", Interval: big}); n != 1 || !issued.Revoked {
		t.Fatalf("overlapping revoke must hit the sub-interval credential: n=%d", n)
	}
}

func TestManifestParentDigestChainEnforced(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 4}
	root := f.manifest("", "r", 1, "obj-r", iv, ro(), Private, 0)
	child := f.manifest("r", "r/c", 2, "obj-c", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{
		binding("r", 1, "obj-r", iv, ro(), Private, 0),
		binding("r/c", 2, "obj-c", iv, ro(), Private, 0),
	}, nil, nil)
	_ = root

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("valid chain should replay: %v", err)
	}

	child.ParentDigest[0] ^= 0xFF
	e2 := NewEngine()
	if err := e2.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err == nil {
		t.Fatal("broken manifest parent digest must fail")
	}
}
