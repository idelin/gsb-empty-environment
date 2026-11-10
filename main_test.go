package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestReplayShadowedSavepointNames(t *testing.T) {
	events := [][]string{
		{"t1", "set", "r1", "a", "1"},
		{"t1", "save", "sp"},
		{"t1", "set", "r1", "b", "2"},
		{"t1", "save", "sp"}, // shadows the earlier savepoint of the same name
		{"t1", "set", "r1", "c", "3"},
		{"t1", "undo", "sp"}, // nearest sp is the inner one: keeps a,b, drops c
		{"t1", "set", "r1", "d", "4"},
		{"t1", "undo", "sp"}, // inner sp is retained, so it shadows the outer one again
		{"t1", "commit"},
	}
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := publicTable{"r1": {"a": "1", "b": "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shadowed savepoints: got %v want %v", got, want)
	}
}

func TestReplayEarlierSavepointClearsShadowedLaterOnes(t *testing.T) {
	events := [][]string{
		{"t1", "set", "r1", "a", "1"},
		{"t1", "save", "base"},
		{"t1", "save", "sp"}, // outer sp
		{"t1", "set", "r1", "b", "2"},
		{"t1", "save", "sp"}, // inner sp shadows the outer one
		{"t1", "set", "r1", "c", "3"},
		{"t1", "undo", "sp"},   // resolves the inner sp, which is retained
		{"t1", "undo", "base"}, // every savepoint after base (both sp) is discarded
		{"t1", "undo", "base"}, // base itself is kept and can be reused
		{"t1", "commit"},
	}
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := publicTable{"r1": {"a": "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("clearing shadowed savepoints: got %v want %v", got, want)
	}
}

func TestReplayUndoKeepsSavepointAndDropsLaterOnes(t *testing.T) {
	events := [][]string{
		{"t1", "save", "keep"},
		{"t1", "set", "r1", "a", "1"},
		{"t1", "save", "drop"},
		{"t1", "set", "r1", "b", "2"},
		{"t1", "undo", "keep"},
		// "keep" still exists and "drop" is gone.
		{"t1", "undo", "keep"},
		{"t1", "commit"},
	}
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := publicTable{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestReplayDeleteThenRebuild(t *testing.T) {
	events := [][]string{
		// Seed a committed row.
		{"seed", "set", "r1", "a", "old"},
		{"seed", "commit"},

		// Transaction deletes the row, then builds a fresh one field by field.
		{"t1", "del", "r1"},
		{"t1", "set", "r1", "a", "new"},
		{"t1", "set", "r1", "b", "2"},
		{"t1", "save", "sp"},
		{"t1", "set", "r1", "c", "3"},
		{"t1", "undo", "sp"}, // c must disappear; the rebuilt row survives
		{"t1", "commit"},
	}
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := publicTable{"r1": {"a": "new", "b": "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delete-then-rebuild: got %v want %v", got, want)
	}
}

func TestReplayUndoOfDeleteRestoresCommittedRow(t *testing.T) {
	events := [][]string{
		{"seed", "set", "r1", "a", "old"},
		{"seed", "commit"},
		{"t1", "save", "sp"},
		{"t1", "del", "r1"},
		{"t1", "undo", "sp"}, // buffered delete is dropped; public row untouched
		{"t1", "commit"},
	}
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := publicTable{"r1": {"a": "old"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("undo of delete: got %v want %v", got, want)
	}
}

func TestReplayInterleavedTransactionsNoWholeRowOverwrite(t *testing.T) {
	events := [][]string{
		// t1 only ever touches r1.a and never snapshots r1.b.
		{"t1", "set", "r1", "a", "from-t1"},
		// t2 concurrently commits a different field of the same row.
		{"t2", "set", "r1", "b", "from-t2"},
		{"t2", "commit"},
		{"t1", "commit"},

		// t3 deletes r1 while t4 is rebuilding r2; r2 must survive.
		{"t4", "set", "r2", "x", "1"},
		{"t3", "del", "r1"},
		{"t3", "commit"},
		{"t4", "commit"},
	}
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := publicTable{"r2": {"x": "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interleaved replay: got %v want %v", got, want)
	}
}

func TestReplayAbortDiscardsEverything(t *testing.T) {
	events := [][]string{
		{"good", "set", "r1", "a", "1"},
		{"good", "commit"},
		{"bad", "set", "r1", "a", "dirty"},
		{"bad", "set", "r1", "z", "9"},
		{"bad", "save", "sp"},
		{"bad", "del", "r1"},
		{"bad", "abort"},
	}
	got, err := replay(events)
	if err != nil {
		t.Fatal(err)
	}
	want := publicTable{"r1": {"a": "1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("abort: got %v want %v", got, want)
	}
}

func TestRunEmitsNestedJSON(t *testing.T) {
	input := strings.NewReader(strings.Join([]string{
		`[["t1","set","r1","a","1"],`,
		`["t1","set","r1","b","2"],`,
		`["t2","set","r2","k","v"],`,
		`["t1","abort"],`,
		`["t2","commit"]]`,
	}, ""))
	var out bytes.Buffer
	if err := run(input, &out); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]map[string]string
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	want := map[string]map[string]string{"r2": {"k": "v"}}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("nested JSON output: got %v want %v", decoded, want)
	}
}

func TestReplayUnknownSavepointAndAction(t *testing.T) {
	if _, err := replay([][]string{{"t1", "undo", "missing"}}); err == nil {
		t.Fatal("expected error for unknown savepoint")
	}
	if _, err := replay([][]string{{"t1", "frobnicate"}}); err == nil {
		t.Fatal("expected error for unknown action")
	}
}
