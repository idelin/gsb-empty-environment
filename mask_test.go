package mask

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempStore(t *testing.T) (*Store, string, func()) {
	dir, err := ioutil.TempDir("", "maskstore")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "map.json")
	st, err := OpenStore(path)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatal(err)
	}
	return st, path, func() { os.RemoveAll(dir) }
}

func fieldToken(t *testing.T, doc []byte, keys ...string) string {
	var v interface{}
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatalf("masked output is not valid JSON: %v", err)
	}
	for _, k := range keys {
		obj, ok := v.(map[string]interface{})
		if !ok {
			t.Fatalf("path %v: not an object at %q", keys, k)
		}
		v = obj[k]
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("path %v: masked value is not a string: %T", keys, v)
	}
	return s
}

func TestDeterministicAcrossFiles(t *testing.T) {
	st, path, cleanup := tempStore(t)
	defer cleanup()

	opts := Options{Fields: []string{"user.email", "email"}}
	m := New(st, opts)
	out1, err := m.MaskDocument([]byte(`{"user":{"email":"a@x.com","name":"Ann"}}`))
	if err != nil {
		t.Fatal(err)
	}
	tok1 := fieldToken(t, out1, "user", "email")
	if tok1 == "a@x.com" || !strings.HasPrefix(tok1, "msk_") {
		t.Fatalf("unexpected token %q", tok1)
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}

	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m2 := New(st2, opts)
	out2, err := m2.MaskDocument([]byte(`{"email":"a@x.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if tok2 := fieldToken(t, out2, "email"); tok2 != tok1 {
		t.Fatalf("same original mapped differently across files: %q vs %q", tok1, tok2)
	}
	if err := st2.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestCollisionSuffixesPersist(t *testing.T) {
	constDigest := func(string) string { return "CONSTANT" }
	opts := Options{Fields: []string{"v"}, Digest: constDigest}

	st, path, cleanup := tempStore(t)
	defer cleanup()
	m := New(st, opts)

	want := map[string]string{
		"a": "msk_constant",
		"b": "msk_constant-2",
		"c": "msk_constant-3",
	}
	for orig, tok := range want {
		out, err := m.MaskDocument([]byte(`{"v":"` + orig + `"}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := fieldToken(t, out, "v"); got != tok {
			t.Fatalf("original %q: got token %q, want %q", orig, got, tok)
		}
	}
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}

	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	m2 := New(st2, opts)
	out, err := m2.MaskDocument([]byte(`{"v":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldToken(t, out, "v"); got != "msk_constant-2" {
		t.Fatalf("collision suffix not stable after reload: got %q", got)
	}
	out, err = m2.MaskDocument([]byte(`{"v":"d"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldToken(t, out, "v"); got != "msk_constant-4" {
		t.Fatalf("new collision after reload: got %q, want msk_constant-4", got)
	}
}

func TestCommitFailurePublishesNothing(t *testing.T) {
	st, path, cleanup := tempStore(t)
	defer cleanup()

	m := New(st, Options{Fields: []string{"v"}})
	if _, err := m.MaskDocument([]byte(`{"v":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Lookup("secret"); !ok {
		t.Fatal("staged allocation missing before commit")
	}

	// Force the atomic rename to fail: the target path is now a directory.
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := st.Commit(); err == nil {
		t.Fatal("commit should have failed")
	}

	entries, err := ioutil.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial mapping published: %d entries in %s", len(entries), path)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind after failed commit: %v", err)
	}

	st.Rollback()
	if _, ok := st.Lookup("secret"); ok {
		t.Fatal("staged allocation survived rollback")
	}
}

func TestRecoveryFromInterruptedCommit(t *testing.T) {
	st, path, cleanup := tempStore(t)
	defer cleanup()

	m := New(st, Options{Fields: []string{"v"}})
	out, err := m.MaskDocument([]byte(`{"v":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	tok := fieldToken(t, out, "v")
	if err := st.Commit(); err != nil {
		t.Fatal(err)
	}

	// Simulate a crash mid-commit: a stale temp file remains next to the
	// last fully committed mapping table.
	if err := ioutil.WriteFile(path+".tmp", []byte("junk"), 0600); err != nil {
		t.Fatal(err)
	}
	st2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := st2.Lookup("x"); !ok || got != tok {
		t.Fatalf("committed mapping lost during recovery: %q, %v", got, ok)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("stale temp file not cleaned up: %v", err)
	}
}

func TestUnspecifiedFieldsByteIdentical(t *testing.T) {
	doc := `{"user":{"email":"a@x.com","note":"line\nbreak \"q\"","deleted":null,"age":37,"tags":["x","a@x.com"]},"ok":true,"id":12345}`
	st, _, cleanup := tempStore(t)
	defer cleanup()

	m := New(st, Options{Fields: []string{"user.email", "user.note", "user.deleted", "user.age"}})
	out, err := m.MaskDocument([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}

	tokEmail, ok := st.Lookup("a@x.com")
	if !ok {
		t.Fatal("email not masked")
	}
	tokNote, ok := st.Lookup("line\nbreak \"q\"")
	if !ok {
		t.Fatal("note not masked")
	}

	// Restoring each token to its original encoded form must reproduce the
	// input byte for byte: every unspecified byte (including the null, the
	// number, and the identical string under the unspecified tags field)
	// is untouched.
	restored := strings.Replace(string(out), tokEmail, `a@x.com`, -1)
	restored = strings.Replace(restored, tokNote, `line\nbreak \"q\"`, -1)
	if restored != doc {
		t.Fatalf("unspecified bytes changed:\n got: %s\nwant: %s", restored, doc)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	user := decoded["user"].(map[string]interface{})
	if user["deleted"] != nil {
		t.Fatalf("null not preserved: %#v", user["deleted"])
	}
	if user["age"] != float64(37) {
		t.Fatalf("number not preserved: %#v", user["age"])
	}
	if user["tags"].([]interface{})[1] != "a@x.com" {
		t.Fatal("unspecified occurrence of the same value was masked")
	}
	if _, isString := user["email"].(string); !isString {
		t.Fatal("masked value does not preserve string type")
	}
}

func TestArrayElementsAndWildcard(t *testing.T) {
	st, _, cleanup := tempStore(t)
	defer cleanup()

	m := New(st, Options{Fields: []string{"users.email", "users.*.ssn"}})
	doc := `{"users":[{"email":"a@x.com","pii":{"ssn":"111"}},{"email":"b@x.com","pii":{"ssn":"222"}}]}`
	out, err := m.MaskDocument([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Users []struct {
			Email string `json:"email"`
			PII   struct {
				SSN string `json:"ssn"`
			} `json:"pii"`
		} `json:"users"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	for i, u := range decoded.Users {
		if !strings.HasPrefix(u.Email, "msk_") {
			t.Fatalf("users[%d].email not masked: %q", i, u.Email)
		}
		if !strings.HasPrefix(u.PII.SSN, "msk_") {
			t.Fatalf("users[%d].pii.ssn not masked: %q", i, u.PII.SSN)
		}
	}
	if decoded.Users[0].Email == decoded.Users[1].Email {
		t.Fatal("distinct originals mapped to the same token")
	}
}
