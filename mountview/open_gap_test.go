package mountview

import "testing"

func TestOpenGapChmodEnumerationMatchesEngine(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 32}
	f.manifest("", "s", 1, "obj-s", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("s", 1, "obj-s", iv, ro(), Private, 0)}, nil, nil)
	f.openNextGap(map[OpType]bool{OpChmod: true},
		GapCandidates{Paths: []string{"s"}, Perms: []Perm{ro(), rw()}, MaxOps: 1})
	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests: manifestsOf(f), Checkpoints: []*Checkpoint{genesis},
		OpenGaps: f.openGaps(), Verify: VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := e.Enumerate("s", 256)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Truncated || rep.Histories != 2 {
		t.Fatalf("want 2 non-truncated histories, got %d truncated=%v", rep.Histories, rep.Truncated)
	}
	if len(rep.PermMay) != 2 || rep.Verdict != Unknown {
		t.Fatalf("open-gap chmod must enumerate both masks as Unknown, got %v %s", rep.PermMay, rep.Verdict)
	}
	engineRes, err := e.Resolve("s")
	if err != nil {
		t.Fatal(err)
	}
	if engineRes.Verdict != rep.Verdict {
		t.Fatalf("engine=%s enumerator=%s mismatch", engineRes.Verdict, rep.Verdict)
	}
}
