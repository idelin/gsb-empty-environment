package jsonpatch

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func raw(t *testing.T, value string) *json.RawMessage {
	t.Helper()
	raw := json.RawMessage(value)
	return &raw
}

func decode(t *testing.T, input string) any {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(input), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return doc
}

func encode(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(raw)
}

func TestApplyDoesNotMutateInput(t *testing.T) {
	doc := decode(t, `{"a":[1,2,3],"b":{"c":1}}`)
	before := encode(t, doc)

	_, err := Apply(doc, []Op{
		{Op: "add", Path: "/a/-", Value: raw(t, `4`)},
		{Op: "replace", Path: "/b/c", Value: raw(t, `99`)},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := encode(t, doc); got != before {
		t.Fatalf("input mutated: before %s, after %s", before, got)
	}
}

func TestArrayIndexesUsePostPreviousOpView(t *testing.T) {
	doc := decode(t, `["a","b","c"]`)

	// After the first add the array is [x,a,b,c]; index 2 then means b, and
	// after removing it the final replace index 1 addresses a (not b).
	got, err := Apply(doc, []Op{
		{Op: "add", Path: "/0", Value: raw(t, `"x"`)},
		{Op: "remove", Path: "/2"},
		{Op: "replace", Path: "/1", Value: raw(t, `"A"`)},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := decode(t, `["x","A","c"]`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s, want %s", encode(t, got), encode(t, want))
	}
}

func TestMapAddRemoveReplace(t *testing.T) {
	doc := decode(t, `{"a":1}`)
	got, err := Apply(doc, []Op{
		{Op: "add", Path: "/b", Value: raw(t, `[1,2]`)},
		{Op: "replace", Path: "/a", Value: raw(t, `{"nested":true}`)},
		{Op: "remove", Path: "/b/1"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := decode(t, `{"a":{"nested":true},"b":[1]}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s, want %s", encode(t, got), encode(t, want))
	}
}

func TestMoveAndCopy(t *testing.T) {
	doc := decode(t, `{"src":{"k":"v"},"list":[10,20]}`)
	got, err := Apply(doc, []Op{
		{Op: "copy", Path: "/list/0", From: "/src"},
		{Op: "move", Path: "/moved", From: "/src"},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := decode(t, `{"list":[{"k":"v"},10,20],"moved":{"k":"v"}}`)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s, want %s", encode(t, got), encode(t, want))
	}
}

func TestMoveIntoOwnSubtreeRejected(t *testing.T) {
	doc := decode(t, `{"a":{"b":1}}`)
	if _, err := Apply(doc, []Op{{Op: "move", Path: "/a/b/x", From: "/a/b"}}); err == nil {
		t.Fatal("expected error for move into own subtree")
	}
}

func TestTestSuccessAndFailure(t *testing.T) {
	doc := decode(t, `{"count":2,"list":[1,2,3]}`)

	if _, err := Apply(doc, []Op{
		{Op: "test", Path: "/count", Value: raw(t, `2`)},
		{Op: "test", Path: "/list/2", Value: raw(t, `3`)},
		{Op: "replace", Path: "/count", Value: raw(t, `3`)},
	}); err != nil {
		t.Fatalf("expected success, got %v", err)
	}

	_, err := Apply(doc, []Op{
		{Op: "replace", Path: "/count", Value: raw(t, `5`)},
		{Op: "test", Path: "/count", Value: raw(t, `2`)},
	})
	var testErr *TestFailureError
	if !errors.As(err, &testErr) {
		t.Fatalf("expected TestFailureError, got %T: %v", err, err)
	}
	if !errors.Is(err, ErrTestFailed) {
		t.Fatalf("error must wrap ErrTestFailed, got %v", err)
	}
	if testErr.Path != "/count" || testErr.Index != 1 {
		t.Fatalf("unexpected failure detail: %+v", testErr)
	}
}

func TestFailingTestRollsBackWholePatch(t *testing.T) {
	doc := decode(t, `{"a":1,"b":2}`)
	before := encode(t, doc)

	_, err := Apply(doc, []Op{
		{Op: "replace", Path: "/a", Value: raw(t, `10`)},
		{Op: "add", Path: "/c", Value: raw(t, `3`)},
		{Op: "test", Path: "/b", Value: raw(t, `999`)}, // fails
		{Op: "replace", Path: "/b", Value: raw(t, `20`)},
	})
	if err == nil {
		t.Fatal("expected test failure")
	}
	if got := encode(t, doc); got != before {
		t.Fatalf("rollback failed: before %s, after %s", before, got)
	}
}

func TestTestSeesViewAfterEarlierOps(t *testing.T) {
	doc := decode(t, `{"items":[]}`)
	_, err := Apply(doc, []Op{
		{Op: "add", Path: "/items/-", Value: raw(t, `"pod-1"`)},
		{Op: "test", Path: "/items/0", Value: raw(t, `"pod-1"`)},
	})
	if err != nil {
		t.Fatalf("test should observe the added value: %v", err)
	}
}

func TestUnsupportedAndMalformedOps(t *testing.T) {
	doc := decode(t, `{"a":1}`)
	if _, err := Apply(doc, []Op{{Op: "frob", Path: "/a"}}); err == nil {
		t.Fatal("expected unsupported-op error")
	}
	if _, err := Apply(doc, []Op{{Op: "add", Path: "no-leading-slash", Value: raw(t, `1`)}}); err == nil {
		t.Fatal("expected pointer parse error")
	}
	if _, err := Apply(doc, []Op{{Op: "add", Path: "/a", Value: nil}}); err == nil {
		t.Fatal("expected missing-value error")
	}
	if _, err := Apply(decode(t, `[1,2]`), []Op{{Op: "add", Path: "/99", Value: raw(t, `1`)}}); err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestPointerEscapes(t *testing.T) {
	doc := decode(t, `{"a/b":1,"c~d":2}`)
	got, err := GetValue(doc, mustPointer(t, "/a~1b"))
	if err != nil || got != float64(1) {
		t.Fatalf("escaped slash lookup failed: %v %v", got, err)
	}
	got, err = GetValue(doc, mustPointer(t, "/c~0d"))
	if err != nil || got != float64(2) {
		t.Fatalf("escaped tilde lookup failed: %v %v", got, err)
	}
}

func mustPointer(t *testing.T, pointer string) []string {
	t.Helper()
	tokens, err := ParsePointer(pointer)
	if err != nil {
		t.Fatalf("parse pointer: %v", err)
	}
	return tokens
}
