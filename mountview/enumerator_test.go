package mountview

import "testing"

func TestEnumeratorCrossChecksEngine(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 32}
	f.manifest("", "s", 1, "obj-s", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("s", 1, "obj-s", iv, ro(), Private, 0)}, nil, nil)

	// A short gap that can create a symlink alias: enumeration must show the
	// same Unknown the abstract engine returns.
	g := f.nextGap(
		map[OpType]bool{OpSymlink: true},
		GapCandidates{Paths: []string{"s/ln"}, Targets: []string{"s"}, MaxOps: 1})
	c1 := f.checkpoint([]ViewBinding{binding("s", 1, "obj-s", iv, ro(), Private, 0)}, nil, []GapSpec{g})

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis, c1},
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}

	engineRes, err := e.Resolve("s/ln")
	if err != nil {
		t.Fatalf("engine resolve: %v", err)
	}
	report, err := e.Enumerate("s/ln", 512)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	if report.Truncated {
		t.Fatal("short-log enumeration must not truncate")
	}
	if report.Histories < 2 {
		t.Fatalf("want at least two concrete histories (alias present/absent), got %d", report.Histories)
	}
	if engineRes.Verdict != report.Verdict {
		t.Fatalf("engine=%s enumerator=%s mismatch", engineRes.Verdict, report.Verdict)
	}
	if report.Verdict != Unknown {
		t.Fatalf("in-gap alias must enumerate to Unknown, got %s", report.Verdict)
	}

	// The untouched content handle stays deterministic in every enumerated
	// history and agrees with the engine.
	stable, err := e.Resolve("s")
	if err != nil {
		t.Fatalf("engine resolve s: %v", err)
	}
	stableReport, err := e.Enumerate("s", 512)
	if err != nil {
		t.Fatalf("enumerate s: %v", err)
	}
	if stable.Verdict != Recoverable || stableReport.Verdict != Recoverable {
		t.Fatalf("stable handle must be Recoverable in both, engine=%s enum=%s",
			stable.Verdict, stableReport.Verdict)
	}
}

func TestEnumeratorChmodExhaustion(t *testing.T) {
	f := newFixture(t)
	iv := Interval{Offset: 0, Len: 4}
	f.manifest("", "h", 1, "obj-h", iv, ro(), Private, 0)
	genesis := f.checkpoint([]ViewBinding{binding("h", 1, "obj-h", iv, ro(), Private, 0)}, nil, nil)

	f.openNextGap(
		map[OpType]bool{OpChmod: true},
		GapCandidates{Paths: []string{"h"}, Perms: []Perm{ro(), rw(), rwx()}, MaxOps: 1})

	e := NewEngine()
	if err := e.Replay(&Input{
		Manifests:   manifestsOf(f),
		Checkpoints: []*Checkpoint{genesis},
		OpenGaps:    f.openGaps(),
		Verify:      VerifierFromKey(f.signer.Public()),
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	report, err := e.Enumerate("h", 512)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	wantPerms := map[uint8]bool{ro().Bits(): true, rw().Bits(): true, rwx().Bits(): true}
	for _, p := range report.PermMay {
		delete(wantPerms, p.Bits())
	}
	if len(wantPerms) != 0 {
		t.Fatalf("enumeration missed permission branches: %v", wantPerms)
	}
	if report.Verdict != Unknown {
		t.Fatalf("multiple effective masks must be Unknown, got %s", report.Verdict)
	}
}
