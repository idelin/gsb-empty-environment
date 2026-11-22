package fedpolicy

import "testing"

func TestStaticLabelerAndMapperImages(t *testing.T) {
	header := HeaderKey{Issuer: "old.example", Tenant: "t"}
	labeler := NewStaticLabeler(header, []Label{"x", "y"})

	inside := Name{Issuer: header.Issuer, Tenant: header.Tenant, Path: []string{"p"}}
	outside := Name{Issuer: "other.example", Tenant: "t"}
	if got := len(labeler.Labels(inside)); got != 2 {
		t.Fatalf("static labels = %d, want 2", got)
	}
	if labeler.Labels(outside) != nil {
		t.Fatal("outside authority must carry no static labels")
	}
	if labeler.MaxLookahead() != 0 {
		t.Fatal("static labeler lookahead must be zero")
	}
	if labeler.StateKey(inside) != true {
		t.Fatal("static labeler state key mismatch")
	}

	fst := NewFST(0)
	fst.AddArc(Arc{From: 0, In: "a", Out: "", To: 1})  // drop a (epsilon output)
	fst.AddArc(Arc{From: 0, In: "a", Out: "z", To: 1}) // nondeterministic second image
	fst.AddArc(Arc{From: 1, In: "b", Out: "b", To: 1})
	fst.MarkFinal(1)

	target := HeaderKey{Issuer: "new.example", Tenant: "t"}
	mapper := &Mapper{Channels: []*Channel{
		{Old: header, New: target, Alphabet: []string{"a", "b"}, Transducer: fst},
	}}
	mapper.Build()

	images := mapper.Images(Name{Issuer: header.Issuer, Tenant: header.Tenant, Path: []string{"a", "b"}})
	if len(images) != 2 {
		t.Fatalf("nondeterministic map produced %d images, want 2", len(images))
	}
	// One image drops the a, the other emits z; both keep b.
	outs := map[string]bool{}
	for _, img := range images {
		outs[joinPath(img.Image.Out)] = true
		if len(img.Image.Trace) != 2 {
			t.Fatal("each trace must have one step per consumed input segment")
		}
	}
	if !outs["b"] || !outs["z\x00b"] {
		t.Fatalf("unexpected image set: %v", outs)
	}
	if mapper.Images(Name{Issuer: "nobody.example", Tenant: "t"}) != nil {
		t.Fatal("an unmapped authority must produce no images")
	}
}

func TestBranchAndBoundMinimum(t *testing.T) {
	header := HeaderKey{Issuer: "h.example", Tenant: "t"}
	prefix := func(segs ...string) NamePrefix {
		return NamePrefix{Issuer: header.Issuer, Tenant: header.Tenant, Path: segs}
	}
	decision := func(segs ...string) *Decision {
		return &Decision{Candidate: Candidate{Prefix: prefix(segs...)}, Safe: true}
	}

	// 20 pairwise-incomparable rules force the branch-and-bound path
	// (the exhaustive path only handles <= 18 essential rules).
	var safe []*Decision
	for i := 0; i < 20; i++ {
		safe = append(safe, decision(string(rune('a'+i))))
	}
	got := minimumEquivalentSubset(safe)
	if len(got) != 20 {
		t.Fatalf("incomparable rules cannot be merged; got %d, want 20", len(got))
	}

	// With one broad rule containing all narrow rules, the minimum is one.
	broad := decision()
	withBroad := append([]*Decision{broad}, safe...)
	got = minimumEquivalentSubset(withBroad)
	if len(got) != 1 || got[0] != broad {
		t.Fatalf("broad rule must collapse the set to itself, got %d", len(got))
	}
}

func TestNoChannelGapWitness(t *testing.T) {
	c := Candidate{Prefix: NamePrefix{Issuer: "orphan.example", Tenant: "t", Path: []string{"x"}}}
	mapper := &Mapper{}
	mapper.Build()
	decision := analyzeCandidate(c, mapper, OldPolicy{Default: Deny},
		NewStaticLabeler(HeaderKey{}, nil), labelSet{})
	if decision.Reason != RejectNoPreimage || len(decision.Witnesses) != 1 {
		t.Fatalf("got reason=%q witnesses=%d, want no-preimage with 1 witness",
			decision.Reason, len(decision.Witnesses))
	}
	w := decision.Witnesses[0]
	if w.Old.Issuer != "" || !PathEquals(w.Target.Path, []string{"x"}) {
		t.Fatal("orphan authority gap witness malformed")
	}
}

func TestEffectAndPrefixHelpers(t *testing.T) {
	if Allow.String() != "allow" || Deny.String() != "deny" {
		t.Fatal("effect rendering changed")
	}
	if !HasPrefix(nil, nil) || HasPrefix([]string{"a"}, []string{"a", "b"}) {
		t.Fatal("prefix helper semantics broken")
	}
	p := NamePrefix{Issuer: "i", Tenant: "t", Path: nil}
	if p.String() != "i/t/" {
		t.Fatalf("empty-path prefix rendering = %q, want i/t/", p.String())
	}
}

func TestAddArcRejectsEmptyInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("empty input segment must panic to keep one-step-per-segment")
		}
	}()
	fst := NewFST(0)
	fst.AddArc(Arc{From: 0, In: "", Out: "x", To: 0})
}
