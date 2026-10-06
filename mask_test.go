package mask

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func constantDigest(string) string { return "CONST" }

func mustMask(t *testing.T, m *Masker, doc string) []byte {
	t.Helper()
	out, err := m.Mask([]byte(doc))
	if err != nil {
		t.Fatalf("Mask(%s): %v", doc, err)
	}
	return out
}

func decode(t *testing.T, doc []byte) map[string]interface{} {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v map[string]interface{}
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
	return v
}

func TestDeterministicAcrossDocuments(t *testing.T) {
	m := New(NewMemStore(), []string{"email"})
	out1 := mustMask(t, m, `{"email":"alice@example.com","id":1}`)
	out2 := mustMask(t, m, `{"id":2,"email":"alice@example.com"}`)
	out3 := mustMask(t, m, `{"email":"bob@example.com"}`)

	e1 := decode(t, out1)["email"]
	e2 := decode(t, out2)["email"]
	e3 := decode(t, out3)["email"]
	if e1 != e2 {
		t.Fatalf("same original mapped differently across documents: %v vs %v", e1, e2)
	}
	if e1 == "alice@example.com" {
		t.Fatalf("value was not masked: %v", e1)
	}
	if e1 == e3 {
		t.Fatalf("different originals mapped to the same value: %v", e1)
	}
}

func TestTypesAndNullPreserved(t *testing.T) {
	m := New(NewMemStore(), []string{"s", "i", "f", "b", "list"})
	doc := `{"s":"hello","i":42,"f":2.5,"b":true,"n":null,"list":["a","b"]}`
	out := mustMask(t, m, doc)
	v := decode(t, out)

	if _, ok := v["s"].(string); !ok {
		t.Fatalf("string field became %T", v["s"])
	}
	if n, ok := v["i"].(json.Number); !ok || strings.ContainsAny(n.String(), ".eE") {
		t.Fatalf("integer field became %v (%T)", v["i"], v["i"])
	}
	if n, ok := v["f"].(json.Number); !ok || !strings.Contains(n.String(), ".") {
		t.Fatalf("float field became %v (%T)", v["f"], v["f"])
	}
	if _, ok := v["b"].(bool); !ok {
		t.Fatalf("bool field became %T", v["b"])
	}
	if v["n"] != nil {
		t.Fatalf("null field became %v", v["n"])
	}
	list, ok := v["list"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("array field became %v (%T)", v["list"], v["list"])
	}
	for _, e := range list {
		if s, ok := e.(string); !ok || s == "a" || s == "b" {
			t.Fatalf("array element not masked as string: %v", e)
		}
	}
	if !json.Valid(out) {
		t.Fatalf("output is not valid JSON: %s", out)
	}
}

func TestCollisionSuffixAllocation(t *testing.T) {
	store := NewMemStore()
	m := New(store, []string{"v"}, WithDigestFunc(constantDigest))

	a := decode(t, mustMask(t, m, `{"v":"alpha"}`))["v"]
	b := decode(t, mustMask(t, m, `{"v":"beta"}`))["v"]
	c := decode(t, mustMask(t, m, `{"v":"gamma"}`))["v"]
	seen := map[interface{}]bool{a: true}
	if seen[b] || seen[c] || b == c {
		t.Fatalf("colliding originals did not get distinct values: %v %v %v", a, b, c)
	}
	if !strings.Contains(b.(string), "#1") || !strings.Contains(c.(string), "#2") {
		t.Fatalf("expected suffix allocation, got %v and %v", b, c)
	}

	// Suffix allocation is stable: re-masking replays the table.
	if again := decode(t, mustMask(t, m, `{"v":"beta"}`))["v"]; again != b {
		t.Fatalf("suffixed mapping not stable: %v vs %v", b, again)
	}

	// Numbers collide too and must stay numeric and distinct.
	n1 := decode(t, mustMask(t, m, `{"v":10}`))["v"]
	n2 := decode(t, mustMask(t, m, `{"v":20}`))["v"]
	if n1 == n2 {
		t.Fatalf("colliding numbers not distinguished: %v", n1)
	}
	if _, ok := n1.(json.Number); !ok {
		t.Fatalf("number became %T", n1)
	}

	// Booleans: only two values exist, so they must swap deterministically.
	t1 := decode(t, mustMask(t, m, `{"v":true}`))["v"]
	t2 := decode(t, mustMask(t, m, `{"v":false}`))["v"]
	if t1 == t2 {
		t.Fatalf("colliding booleans not distinguished: %v", t1)
	}

	table, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Entries) != 7 {
		t.Fatalf("expected 7 published mappings, got %d: %v", len(table.Entries), table.Entries)
	}
}

func TestRecoveryAcrossRestarts(t *testing.T) {
	dir, err := ioutil.TempDir("", "mask")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "table.json")

	m1 := New(NewFileStore(path), []string{"email"})
	out1 := mustMask(t, m1, `{"email":"alice@example.com"}`)
	masked := decode(t, out1)["email"]

	// Simulate a crash mid-commit: a stale temp file must be ignored.
	if err := ioutil.WriteFile(path+".tmp", []byte(`{"version":99,"entries":{"broken`), 0644); err != nil {
		t.Fatal(err)
	}

	// A fresh process (new Masker and FileStore) recovers the table.
	m2 := New(NewFileStore(path), []string{"email"})
	out2 := mustMask(t, m2, `{"id":7,"email":"alice@example.com"}`)
	if got := decode(t, out2)["email"]; got != masked {
		t.Fatalf("mapping not recovered after restart: %v vs %v", masked, got)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale temp file was not cleaned up: %v", err)
	}
}

type brokenStore struct {
	inner *MemStore
	err   error
}

func (s *brokenStore) Load() (Table, error) { return s.inner.Load() }
func (s *brokenStore) Commit(uint64, Table) error {
	return s.err
}

func TestFailedCommitPublishesNothing(t *testing.T) {
	inner := NewMemStore()
	m := New(&brokenStore{inner: inner, err: errors.New("disk on fire")}, []string{"email"})

	if _, err := m.Mask([]byte(`{"email":"a@b.c"}`)); err == nil {
		t.Fatal("expected commit error")
	}
	table, err := inner.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(table.Entries) != 0 || table.Version != 0 {
		t.Fatalf("partial mappings published after failed commit: %+v", table)
	}
}

// flakyStore simulates a concurrent writer winning the first commit race.
type flakyStore struct {
	inner     *MemStore
	conflicts int
}

func (s *flakyStore) Load() (Table, error) { return s.inner.Load() }
func (s *flakyStore) Commit(expected uint64, t Table) error {
	if s.conflicts > 0 {
		s.conflicts--
		cur, _ := s.inner.Load()
		if err := s.inner.Commit(cur.Version, Table{Entries: map[string]string{`s:"other"`: `"tok_other"`}}); err != nil {
			return err
		}
		return ErrConflict
	}
	return s.inner.Commit(expected, t)
}

func TestCommitConflictRetries(t *testing.T) {
	inner := NewMemStore()
	m := New(&flakyStore{inner: inner, conflicts: 1}, []string{"email"})

	out, err := m.Mask([]byte(`{"email":"a@b.c"}`))
	if err != nil {
		t.Fatalf("Mask did not survive one conflict: %v", err)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid output: %s", out)
	}
	table, _ := inner.Load()
	if len(table.Entries) != 2 {
		t.Fatalf("expected concurrent + own mappings, got %v", table.Entries)
	}
}

func TestUnspecifiedFieldsByteIdentical(t *testing.T) {
	doc := "{ \"keep\" : \"a\\nb\\té\" ,\n  \"nested\":{\"arr\":[1,2.50e2,{\"x\":null}]},\t\"email\":\"a@b.c\" }"

	// With no selected fields the document is copied byte for byte.
	out := mustMask(t, New(NewMemStore(), nil), doc)
	if !bytes.Equal(out, []byte(doc)) {
		t.Fatalf("document changed with no fields selected:\n%s\n%s", doc, out)
	}

	// With one field selected, only that value's bytes change.
	store := NewMemStore()
	m := New(store, []string{"email"})
	mini := mustMask(t, m, `{"email":"a@b.c"}`)
	maskedLit := strings.TrimSuffix(strings.TrimPrefix(string(mini), `{"email":`), `}`)

	expected := strings.Replace(doc, `"a@b.c"`, maskedLit, 1)
	out = mustMask(t, m, doc)
	if string(out) != expected {
		t.Fatalf("bytes outside selected field changed:\ngot:  %s\nwant: %s", out, expected)
	}
}

func TestNestedFieldMatchedAtAnyDepth(t *testing.T) {
	m := New(NewMemStore(), []string{"email"})
	out := mustMask(t, m, `{"user":{"email":"x@y.z","name":"Ann"}}`)
	v := decode(t, out)
	user := v["user"].(map[string]interface{})
	if user["email"] == "x@y.z" {
		t.Fatal("nested email not masked")
	}
	if user["name"] != "Ann" {
		t.Fatalf("sibling field changed: %v", user["name"])
	}
}

func TestInvalidJSONRejected(t *testing.T) {
	m := New(NewMemStore(), []string{"a"})
	for _, doc := range []string{`{`, `{"a":}`, `{"a":01}`, `{"a":"x`, `[1,]`, `{"a":tru}`} {
		if _, err := m.Mask([]byte(doc)); err == nil {
			t.Fatalf("expected error for %s", doc)
		}
	}
}
