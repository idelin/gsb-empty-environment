package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// event builds one [txnId, action, args...] JSON array.
func event(tid string, parts ...string) []json.RawMessage {
	raw := []json.RawMessage{mustRaw(tid), mustRaw(parts[0])}
	for _, p := range parts[1:] {
		raw = append(raw, mustRaw(p))
	}
	return raw
}

func mustRaw(s string) json.RawMessage {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}

func replayEvents(t *testing.T, events ...[]json.RawMessage) map[string]map[string]string {
	t.Helper()
	b, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("marshal events: %v", err)
	}
	got, err := Replay(strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("Replay returned error: %v", err)
	}
	return got
}

func expectTable(t *testing.T, got, want map[string]map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("table mismatch:\n got: %v\nwant: %v", got, want)
	}
}

// The later same-named savepoint shadows the earlier one: undo rolls back to
// the latest "sp" and drops only the savepoints created after it.
func TestSavepointShadowing(t *testing.T) {
	got := replayEvents(t,
		event("t1", "set", "r1", "a", "1"),
		event("t1", "save", "sp"),
		event("t1", "set", "r1", "b", "2"),
		event("t1", "save", "sp"), // shadows the first sp at a=1
		event("t1", "set", "r1", "c", "3"),
		event("t1", "undo", "sp"), // back to a=1,b=2; c dropped
		event("t1", "set", "r1", "d", "4"),
		event("t1", "commit"),
	)
	expectTable(t, got, map[string]map[string]string{
		"r1": {"a": "1", "b": "2", "d": "4"},
	})
}

// An undo keeps the savepoint, so a second undo with the same name still
// truncates everything appended after it.
func TestRepeatedUndoSameSavepoint(t *testing.T) {
	got := replayEvents(t,
		event("t1", "save", "sp"),
		event("t1", "set", "r1", "a", "1"),
		event("t1", "undo", "sp"),
		event("t1", "set", "r1", "b", "2"),
		event("t1", "undo", "sp"), // sp survives the first undo
		event("t1", "set", "r1", "c", "3"),
		event("t1", "commit"),
	)
	expectTable(t, got, map[string]map[string]string{
		"r1": {"c": "3"},
	})
}

// Interleaved transactions: t1 deletes a committed row and rebuilds it, while
// t2 commits an extra field first. Replaying ops (not whole-row snapshots)
// keeps t2's concurrent write and yields the rebuilt row.
func TestDeleteThenRebuildAcrossTransactions(t *testing.T) {
	got := replayEvents(t,
		event("t0", "set", "r1", "a", "base"),
		event("t0", "set", "r1", "b", "keep"),
		event("t0", "commit"),

		event("t1", "del", "r1"),
		event("t2", "set", "r1", "c", "concurrent"), // interleaved, commits first
		event("t2", "commit"),
		event("t1", "set", "r1", "a", "rebuilt"),
		event("t1", "set", "r1", "c", "rebuilt-c"), // rebuild over the concurrent field
		event("t1", "commit"),
	)
	expectTable(t, got, map[string]map[string]string{
		"r1": {"a": "rebuilt", "c": "rebuilt-c"},
	})
}

// Concurrent field-level commits must not be clobbered by a stale row copy
// taken when the later transaction started.
func TestConcurrentFieldsAreMerged(t *testing.T) {
	got := replayEvents(t,
		event("t0", "set", "r1", "seed", "0"),
		event("t0", "commit"),

		event("t1", "set", "r1", "f1", "from-t1"), // starts while r1 exists
		event("t2", "set", "r1", "f2", "from-t2"),
		event("t2", "commit"), // f2 visible first
		event("t1", "commit"), // must not overwrite r1 with a t1-start snapshot
	)
	expectTable(t, got, map[string]map[string]string{
		"r1": {"seed": "0", "f1": "from-t1", "f2": "from-t2"},
	})
}

// Aborted work leaves no trace, and savepoints are scoped per transaction.
func TestAbortAndSavepointIsolation(t *testing.T) {
	got := replayEvents(t,
		event("t1", "set", "r1", "a", "1"),
		event("t1", "save", "sp"),
		event("t1", "set", "r1", "b", "2"),
		event("t2", "save", "sp"), // distinct transaction, same name is fine
		event("t2", "set", "r2", "x", "9"),
		event("t1", "undo", "sp"),
		event("t1", "abort"),
		event("t2", "commit"),
	)
	expectTable(t, got, map[string]map[string]string{
		"r2": {"x": "9"},
	})
}

// Undo only truncates savepoints of the same transaction; ops committed by
// other transactions stay untouched.
func TestUndoDoesNotTouchCommittedData(t *testing.T) {
	got := replayEvents(t,
		event("t1", "set", "r1", "a", "1"),
		event("t1", "save", "sp"),
		event("t2", "set", "r2", "a", "2"),
		event("t2", "commit"),
		event("t1", "set", "r1", "b", "3"),
		event("t1", "undo", "sp"),
		event("t1", "commit"),
	)
	expectTable(t, got, map[string]map[string]string{
		"r1": {"a": "1"},
		"r2": {"a": "2"},
	})
}

func TestUnknownActionAndMissingSavepointError(t *testing.T) {
	good, err := json.Marshal([][]json.RawMessage{
		event("t1", "frobnicate", "r1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(strings.NewReader(string(good))); err == nil {
		t.Fatal("expected error for unknown action")
	}

	missing, err := json.Marshal([][]json.RawMessage{
		event("t1", "undo", "nope"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(strings.NewReader(string(missing))); err == nil {
		t.Fatal("expected error for missing savepoint")
	}
}

func TestMainEndToEnd(t *testing.T) {
	if os.Getenv("CDC_REPLAY_CHILD") == "1" {
		main()
		os.Exit(0)
	}

	input := `[
		["t1","set","r1","a","1"],
		["t1","save","sp"],
		["t2","set","r1","b","2"],
		["t2","commit"],
		["t1","set","r1","c","3"],
		["t1","undo","sp"],
		["t1","commit"]
	]`

	cmd := exec.Command(os.Args[0], "-test.run=TestMainEndToEnd")
	cmd.Env = append(os.Environ(), "CDC_REPLAY_CHILD=1")
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary run failed: %v\n%s", err, out)
	}

	var got map[string]map[string]string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not nested JSON: %v\n%s", err, out)
	}
	expectTable(t, got, map[string]map[string]string{
		"r1": {"a": "1", "b": "2"},
	})
}
