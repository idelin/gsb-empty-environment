package mountview

import "testing"

// A closed checkpoint pins the content layer, but a symlink alias rebuilt in
// the gap remains open. Both the abstract engine and the full enumerator must
// return Unknown for the alias while the signed anchor stays Recoverable.
func TestClosedGapContentPinnedButAliasOpen(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 24}
	f.manifest("", "mnt", 1, "obj-1", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{
		binding("mnt", 1, "obj-1", iv, ro(), Private, 0),
	}, nil, nil)

	g := f.nextGap(map[OpType]bool{OpSymlink: true, OpHardlink: true}, GapCandidates{
		Paths:   []string{"mnt/alias"},
		Targets: []string{"mnt"},
		MaxOps:  1,
	})
	c1 := f.checkpoint([]ViewBinding{
		binding("mnt", 1, "obj-1", iv, ro(), Private, 0),
	}, nil, []GapSpec{g})

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis, c1},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}

	anchor, err := e.Resolve("mnt")
	if err != nil {
		t.Fatalf("anchor: %v", err)
	}
	if anchor.Verdict != Recoverable {
		t.Fatalf("signed anchor must be Recoverable after reconciliation, got %s", anchor.Verdict)
	}

	alias, err := e.Resolve("mnt/alias")
	if err != nil {
		t.Fatalf("alias: %v", err)
	}
	report, err := e.Enumerate("mnt/alias", 512)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if alias.Verdict != Unknown {
		t.Fatalf("in-gap alias must be Unknown, got %s", alias.Verdict)
	}
	if report.Verdict != Unknown {
		t.Fatalf("enumerator must agree on Unknown, got %s", report.Verdict)
	}

	anchorReport, err := e.Enumerate("mnt", 512)
	if err != nil {
		t.Fatalf("enumerate anchor: %v", err)
	}
	if anchorReport.Verdict != Recoverable {
		t.Fatalf("enumerated anchor must be Recoverable, got %s", anchorReport.Verdict)
	}
}
