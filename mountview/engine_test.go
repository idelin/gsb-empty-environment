package mountview

import "testing"

func TestBaselineDeterministicRecovery(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 128}
	f.manifest("", "a", 1, "obj-a", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("a", 1, "obj-a", iv, ro(), Private, 0)}, nil, nil)

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	res, err := e.Resolve("a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Verdict != Recoverable {
		t.Fatalf("want Recoverable, got %s (%s)", res.Verdict, res.Reason)
	}
	if len(res.Objects) != 1 || res.Objects[0] != "obj-a" {
		t.Fatalf("object may-set wrong: %v", res.Objects)
	}
	if len(res.Intervals) != 1 || res.Intervals[0] != iv {
		t.Fatalf("interval may-set wrong: %v", res.Intervals)
	}
}

func TestTwoDisconnectedGapsAndSiblingIsolation(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 64}
	f.manifest("", "a", 1, "obj-a", iv, ro(), Private, 0)
	f.manifest("", "b", 2, "obj-b", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{
		binding("a", 1, "obj-a", iv, ro(), Private, 0),
		binding("b", 2, "obj-b", iv, ro(), Private, 0),
	}, nil, nil)

	g1 := f.nextGap(
		map[OpType]bool{OpSymlink: true},
		GapCandidates{Paths: []string{"a/link"}, Targets: []string{"a", "b"}})
	c1 := f.checkpoint([]ViewBinding{
		binding("a", 1, "obj-a", iv, ro(), Private, 0),
		binding("b", 2, "obj-b", iv, ro(), Private, 0),
	}, nil, []GapSpec{g1})

	g2 := f.nextGap(
		map[OpType]bool{OpBindMount: true},
		GapCandidates{Paths: []string{"a"}, Objects: []ObjectID{"obj-a", "obj-evil"},
			Intervals: []Interval{iv}, Perms: []Perm{ro()}, Domains: []DomainID{0}})
	c2 := f.checkpoint([]ViewBinding{
		binding("a", 1, "obj-a", iv, ro(), Private, 0),
		binding("b", 2, "obj-b", iv, ro(), Private, 0),
	}, nil, []GapSpec{g2})

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis, c1, c2},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if e.gapCount != 2 {
		t.Fatalf("want 2 applied gaps, got %d", e.gapCount)
	}

	link, err := e.Resolve("a/link")
	if err != nil {
		t.Fatalf("resolve link: %v", err)
	}
	if link.Verdict != Unknown {
		t.Fatalf("in-gap symlink alias must be Unknown, got %s", link.Verdict)
	}

	sib, err := e.Resolve("b")
	if err != nil {
		t.Fatalf("resolve sibling: %v", err)
	}
	if sib.Verdict != Recoverable {
		t.Fatalf("private sibling must stay Recoverable, got %s (%s)", sib.Verdict, sib.Reason)
	}
}

func TestGapAliasNeverTreatedAsSafeInheritance(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 10, Len: 32}
	f.manifest("", "mnt", 1, "obj-1", iv, ro(), Shared, 7)
	f.manifest("mnt", "mnt/link", 2, "obj-2", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{
		binding("mnt", 1, "obj-1", iv, ro(), Shared, 7),
		binding("mnt/link", 2, "obj-2", iv, ro(), Private, 0),
	}, nil, nil)

	g := f.nextGap(
		map[OpType]bool{OpSymlink: true, OpHardlink: true},
		GapCandidates{Paths: []string{"mnt/link"}, Targets: []string{"mnt", "x"}})
	c1 := f.checkpoint([]ViewBinding{
		binding("mnt", 1, "obj-1", iv, ro(), Shared, 7),
	}, nil, []GapSpec{g})

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis, c1},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	res, err := e.Resolve("mnt/link")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Verdict != Unknown {
		t.Fatalf("alias repointed inside a gap must be Unknown, got %s", res.Verdict)
	}
}

func TestReadOnlySliceRelaxationStaysUnknown(t *testing.T) {
	f := newFixture(t)
	small := Interval{Offset: 0, Len: 16}
	wide := Interval{Offset: 0, Len: 64}
	f.manifest("", "vol", 1, "obj-v", small, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("vol", 1, "obj-v", small, ro(), Private, 0)}, nil, nil)

	f.openNextGap(
		map[OpType]bool{OpSlice: true, OpChmod: true},
		GapCandidates{Paths: []string{"vol"}, Intervals: []Interval{small, wide}, Perms: []Perm{ro(), rw()}})

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		OpenGaps:    f.openGaps(),
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	res, err := e.Resolve("vol")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Verdict != Unknown {
		t.Fatalf("possible slice/chmod change must be Unknown, got %s", res.Verdict)
	}
}
