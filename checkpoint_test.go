package speckey

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildPopulatedSet returns a set exercising primary records,
// duplicates, and isolated conflicts, plus the adds needed to
// reproduce it (used to verify recovery behavior).
func buildPopulatedSet(t *testing.T) (*Set, []struct {
	key     SpecKey
	content string
	want    Outcome
}) {
	t.Helper()
	s := NewSet(ConflictIsolate)
	type add struct {
		key     SpecKey
		content string
		want    Outcome
	}
	adds := []add{
		{NewKey(Str("fmt"), Str("1.0")), "content-a", Added},
		{NewKey(Str("fmt"), Str("1.0")), "content-a", Duplicate},
		{NewKey(Str("fmt"), Str("1.0")), "content-b", Isolated},
		{NewKey(Null(), Null()), "nulls", Added},
		{NewKey(Str(""), Str("")), "empties", Added},
		{NewKey(Str("a|b"), Str("V2:xy")), "tricky", Added},
		{NewKey(Str("a|b"), Str("V2:xy")), "tricky-2", Isolated},
	}
	for _, a := range adds {
		if o, err := s.Add(a.key, a.content); err != nil || o != a.want {
			t.Fatalf("setup Add(%v, %q): got %v, %v, want %v", a.key, a.content, o, err, a.want)
		}
	}
	replay := make([]struct {
		key     SpecKey
		content string
		want    Outcome
	}, len(adds))
	for i, a := range adds {
		replay[i].key = a.key
		replay[i].content = a.content
		replay[i].want = Duplicate // after recovery every add dedups
	}
	return s, replay
}

func TestCheckpointRoundTrip(t *testing.T) {
	s, replay := buildPopulatedSet(t)
	var buf bytes.Buffer
	if err := s.Save(&buf); err != nil {
		t.Fatal(err)
	}
	got, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Strategy() != s.Strategy() {
		t.Fatalf("strategy = %v, want %v", got.Strategy(), s.Strategy())
	}
	if got.Len() != s.Len() {
		t.Fatalf("Len = %d, want %d", got.Len(), s.Len())
	}
	// Recovery: replaying every historical add against the restored
	// set must dedup exactly as if no restart happened.
	for i, a := range replay {
		if o, err := got.Add(a.key, a.content); err != nil || o != a.want {
			t.Fatalf("replay %d: got %v, %v, want %v", i, o, err, a.want)
		}
	}
	if got.Len() != s.Len() {
		t.Fatalf("Len after replay = %d, want %d", got.Len(), s.Len())
	}
}

func TestCheckpointFileRecovery(t *testing.T) {
	dir, err := ioutil.TempDir("", "speckey")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checkpoint.json")

	s, _ := buildPopulatedSet(t)
	if err := s.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file should not remain after atomic save")
	}
	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != s.Len() {
		t.Fatalf("Len = %d, want %d", got.Len(), s.Len())
	}
	// Spot-check primary and isolated contents survived.
	k := NewKey(Str("fmt"), Str("1.0"))
	all := got.GetAll(k)
	if len(all) != 2 {
		t.Fatalf("GetAll = %v, want primary + 1 isolated", all)
	}
}

func TestCheckpointCorruptionDetected(t *testing.T) {
	s, _ := buildPopulatedSet(t)
	var buf bytes.Buffer
	if err := s.Save(&buf); err != nil {
		t.Fatal(err)
	}
	valid := buf.Bytes()

	tamper := func(mutate func(*checkpoint)) []byte {
		var cp checkpoint
		if err := json.Unmarshal(valid, &cp); err != nil {
			t.Fatal(err)
		}
		mutate(&cp)
		out, err := json.Marshal(cp)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	cases := map[string][]byte{
		"not json":      []byte("{nope"),
		"wrong version": tamper(func(c *checkpoint) { c.Version = 99 }),
		"wrong count":   tamper(func(c *checkpoint) { c.Count++ }),
		"record removed": tamper(func(c *checkpoint) {
			for k := range c.Records {
				delete(c.Records, k)
				break
			}
		}),
		"content altered": tamper(func(c *checkpoint) {
			for k := range c.Records {
				c.Records[k] += "x"
				break
			}
		}),
		"digest altered": tamper(func(c *checkpoint) { c.SHA256 = strings.Repeat("0", 64) }),
	}
	for name, data := range cases {
		if _, err := Load(bytes.NewReader(data)); err == nil {
			t.Fatalf("%s: corruption not detected", name)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := LoadFrom(filepath.Join(os.TempDir(), "speckey-no-such-file.json")); err == nil {
		t.Fatal("expected error for missing checkpoint file")
	}
}
