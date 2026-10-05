package main

import (
	"strings"
	"testing"
)

func notesContain(rep StepReport, sub string) bool {
	for _, n := range rep.Notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

func TestReplayDetectsDuplicateAndGap(t *testing.T) {
	entries := []Entry{
		{Seq: 1, Op: "create", Path: "/a", ID: 1, Dir: true},
		{Seq: 1, Op: "create", Path: "/b", ID: 2, Dir: true}, // duplicate seq
		{Seq: 5, Op: "create", Path: "/c", ID: 3, Dir: true}, // gap 2..4
	}
	r := NewReplayer()
	res := r.Replay(entries)
	if len(res.Steps) != 3 {
		t.Fatalf("steps = %d", len(res.Steps))
	}
	if !notesContain(res.Steps[1], "duplicate log entry: seq 1") {
		t.Fatalf("duplicate not flagged: %v", res.Steps[1].Notes)
	}
	if !notesContain(res.Steps[2], "sequence gap") {
		t.Fatalf("gap not flagged: %v", res.Steps[2].Notes)
	}
	if res.Failed {
		t.Fatal("all ops should apply")
	}
}

func TestReplayDetectsDeleteThenRecreate(t *testing.T) {
	entries := []Entry{
		{Seq: 1, Op: "create", Path: "/conf", ID: 10},
		{Seq: 2, Op: "unlink", Path: "/conf"},
		{Seq: 3, Op: "create", Path: "/conf", ID: 20},
	}
	r := NewReplayer()
	res := r.Replay(entries)
	if !notesContain(res.Steps[2], "recreated with new identity") {
		t.Fatalf("recreation not flagged: %v", res.Steps[2].Notes)
	}
	if res.Mapping["/conf"] != 20 {
		t.Fatalf("mapping = %v", res.Mapping)
	}
	// Rename onto an existing path is also a delete-then-recreate there.
	entries2 := []Entry{
		{Seq: 1, Op: "create", Path: "/x", ID: 1},
		{Seq: 2, Op: "create", Path: "/y", ID: 2},
		{Seq: 3, Op: "rename", Old: "/x", New: "/y"},
	}
	r2 := NewReplayer()
	res2 := r2.Replay(entries2)
	if !notesContain(res2.Steps[2], "recreated with new identity") {
		t.Fatalf("replace not flagged as recreation: %v", res2.Steps[2].Notes)
	}
	if res2.Mapping["/y"] != 1 {
		t.Fatalf("mapping = %v", res2.Mapping)
	}
}

func TestReplayFailedTransactionLeavesTreeUntouched(t *testing.T) {
	entries := []Entry{
		{Seq: 1, Op: "create", Path: "/a", ID: 1, Dir: true},
		{Seq: 2, Op: "create", Path: "/a/f", ID: 2},
		{Seq: 3, Op: "rename", Old: "/a", New: "/a/bad"}, // cycle: must fail
		{Seq: 4, Op: "create", Path: "/ok", ID: 3},
	}
	r := NewReplayer()
	res := r.Replay(entries)
	if !res.Failed {
		t.Fatal("expected failure flag")
	}
	if res.Steps[2].OK {
		t.Fatal("cyclic rename should have failed")
	}
	if !notesContain(res.Steps[2], "transaction aborted") {
		t.Fatalf("abort note missing: %v", res.Steps[2].Notes)
	}
	want := map[string]int64{"/": 0, "/a": 1, "/a/f": 2, "/ok": 3}
	for p, id := range want {
		if res.Mapping[p] != id {
			t.Fatalf("mapping = %v, want %v", res.Mapping, want)
		}
	}
}

func TestVerifyAgreementOnComplexLog(t *testing.T) {
	entries := []Entry{
		{Seq: 1, Op: "create", Path: "/a", ID: 1, Dir: true},
		{Seq: 2, Op: "create", Path: "/a/b", ID: 2, Dir: true},
		{Seq: 3, Op: "create", Path: "/a/b/f", ID: 3},
		{Seq: 4, Op: "create", Path: "/g", ID: 4, Dir: true},
		{Seq: 5, Op: "rename", Old: "/a", New: "/g/a2"},       // dir move
		{Seq: 6, Op: "create", Path: "/tmp", ID: 5},           // will be replaced
		{Seq: 7, Op: "rename", Old: "/g/a2/b/f", New: "/tmp"}, // replace
		{Seq: 8, Op: "unlink", Path: "/tmp"},
		{Seq: 9, Op: "create", Path: "/tmp", ID: 6},          // recreate same path
		{Seq: 10, Op: "rename", Old: "/g", New: "/g/a2/bad"}, // cycle: fails
		{Seq: 11, Op: "unlink", Path: "/nope"},               // fails
	}
	r := NewReplayer()
	res := r.Replay(entries)
	problems := Verify(entries, res.Mapping)
	if len(problems) != 0 {
		t.Fatalf("model mismatch: %v", problems)
	}
	want := map[string]int64{
		"/": 0, "/g": 4, "/g/a2": 1, "/g/a2/b": 2, "/tmp": 6,
	}
	if len(res.Mapping) != len(want) {
		t.Fatalf("mapping = %v, want %v", res.Mapping, want)
	}
	for p, id := range want {
		if res.Mapping[p] != id {
			t.Fatalf("mapping = %v, want %v", res.Mapping, want)
		}
	}
}

func TestParseLog(t *testing.T) {
	log := `
# comment
{"seq":1,"op":"create","path":"/a","id":1,"dir":true}

{"seq":2,"op":"rename","old":"/a","new":"/b"}
`
	entries, err := ParseLog(strings.NewReader(log))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 || entries[0].ID != 1 || !entries[0].Dir || entries[1].New != "/b" {
		t.Fatalf("entries = %+v", entries)
	}
	if _, err := ParseLog(strings.NewReader("{bad json")); err == nil {
		t.Fatal("invalid json should error")
	}
}
