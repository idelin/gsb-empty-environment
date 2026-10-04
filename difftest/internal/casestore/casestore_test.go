package casestore

import (
	"os"
	"testing"

	"difftest/internal/judge"
	"difftest/internal/normalize"
	"difftest/internal/runner"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	input := []byte("1 2 3\n")
	rules := normalize.Rules{NumericTokens: true, FloatTolerance: 1e-6}
	outA := runner.Outcome{Kind: runner.KindValue, Stdout: "6"}
	outB := runner.Outcome{Kind: runner.KindValue, Stdout: "7"}
	verdict := judge.Judge(outA, outB, rules)
	if verdict.Verdict != judge.SemanticDiff {
		t.Fatalf("sanity: want semantic_diff, got %s", verdict.Verdict)
	}
	kase := New("prog-a", "prog-b", rules, input, outA, outB, verdict)
	if err := Save(dir, kase, input); err != nil {
		t.Fatal(err)
	}
	loaded, gotInput, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotInput) != string(input) {
		t.Fatalf("input mismatch: %q", gotInput)
	}
	if loaded.Verdict.Verdict != judge.SemanticDiff {
		t.Fatalf("verdict mismatch: %s", loaded.Verdict.Verdict)
	}
	if loaded.Env.OS == "" || loaded.Env.GoVersion == "" {
		t.Fatal("environment summary not populated")
	}
}

func TestLoadDetectsCorruptedInput(t *testing.T) {
	dir := t.TempDir()
	input := []byte("original")
	kase := New("a", "b", normalize.Rules{}, input,
		runner.Outcome{Kind: runner.KindValue}, runner.Outcome{Kind: runner.KindValue},
		judge.Result{Verdict: judge.Equal})
	if err := Save(dir, kase, input); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/"+InputFile, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir); err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}
