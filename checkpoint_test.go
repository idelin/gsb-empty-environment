package canonkey

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "canonkey-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// buildPopulatedStore creates a store exercising dedup, isolation, null
// fields, and adversarial payloads, plus the list of adds to replay.
func buildPopulatedStore(t *testing.T) (*Store, []struct {
	key     Key
	content []byte
}) {
	t.Helper()
	s := NewStore(ConflictIsolate)
	type add struct {
		key     Key
		content []byte
	}
	adds := []add{
		{EncodeKey(String("alpha"), Null()), []byte("a1")},
		{EncodeKey(String("alpha"), String("")), []byte("a2")},
		{EncodeKey(Int(1)), []byte("i1")},
		{EncodeKey(String("1")), []byte("s1")},
		{EncodeKey(String("x:y#z"), Bytes([]byte{0, 1})), []byte("adv")},
		{EncodeKey(String("alpha"), Null()), []byte("a1")},     // dedup hit
		{EncodeKey(String("alpha"), Null()), []byte("a1")},     // dedup hit
		{EncodeKey(String("alpha"), Null()), []byte("a1-alt")}, // conflict -> isolate
		{EncodeKey(Int(1)), []byte("i1-alt")},                  // conflict -> isolate
	}
	for i, a := range adds {
		if _, err := s.Add(a.key, a.content); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	replay := make([]struct {
		key     Key
		content []byte
	}, len(adds))
	for i, a := range adds {
		replay[i].key = a.key
		replay[i].content = a.content
	}
	return s, replay
}

func TestCheckpointRoundTrip(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checkpoint.json")

	s, _ := buildPopulatedStore(t)
	if err := s.SaveCheckpoint(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Policy() != s.Policy() {
		t.Fatalf("policy = %v, want %v", loaded.Policy(), s.Policy())
	}
	if loaded.Len() != s.Len() || loaded.IsolatedLen() != s.IsolatedLen() {
		t.Fatalf("sizes = %d/%d, want %d/%d",
			loaded.Len(), loaded.IsolatedLen(), s.Len(), s.IsolatedLen())
	}
	for _, k := range s.Keys() {
		want, _ := s.Get(k)
		got, ok := loaded.Get(k)
		if !ok || string(got) != string(want) {
			t.Fatalf("key %q: got %q,%v want %q", string(k), got, ok, want)
		}
		wantIso := s.Isolated(k)
		gotIso := loaded.Isolated(k)
		if len(gotIso) != len(wantIso) {
			t.Fatalf("key %q: isolated %d, want %d", string(k), len(gotIso), len(wantIso))
		}
		for i := range wantIso {
			if string(gotIso[i]) != string(wantIso[i]) {
				t.Fatalf("key %q isolated[%d] = %q, want %q",
					string(k), i, gotIso[i], wantIso[i])
			}
		}
	}
}

// TestCheckpointRecoveryReplay simulates crash recovery: reload the
// checkpoint, replay the exact same workload, and require every add to be
// recognized as duplicate/isolated exactly as before the crash — no
// double-inserts, no lost conflicts.
func TestCheckpointRecoveryReplay(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checkpoint.json")

	s, replay := buildPopulatedStore(t)
	if err := s.SaveCheckpoint(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	wantOutcomes := make([]Outcome, len(replay))
	for i, a := range replay {
		o, err := s.Add(a.key, a.content)
		if err != nil {
			t.Fatalf("baseline replay add %d: %v", i, err)
		}
		wantOutcomes[i] = o
	}
	for i, a := range replay {
		o, err := loaded.Add(a.key, a.content)
		if err != nil {
			t.Fatalf("recovered replay add %d: %v", i, err)
		}
		if o != wantOutcomes[i] {
			t.Fatalf("recovered replay add %d: outcome %v, want %v", i, o, wantOutcomes[i])
		}
	}
	if loaded.Len() != s.Len() || loaded.IsolatedLen() != s.IsolatedLen() {
		t.Fatalf("post-replay sizes = %d/%d, want %d/%d",
			loaded.Len(), loaded.IsolatedLen(), s.Len(), s.IsolatedLen())
	}
}

// TestCheckpointContinueAfterRecovery ensures new keys can be added after
// recovery and re-checkpointed without disturbing restored state.
func TestCheckpointContinueAfterRecovery(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checkpoint.json")

	s, _ := buildPopulatedStore(t)
	if err := s.SaveCheckpoint(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	newKey := EncodeKey(String("post-crash"), Bool(true))
	if o, err := loaded.Add(newKey, []byte("new")); err != nil || o != Inserted {
		t.Fatalf("post-recovery add: %v, %v", o, err)
	}
	if err := loaded.SaveCheckpoint(path); err != nil {
		t.Fatalf("re-save: %v", err)
	}
	reloaded, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Len() != s.Len()+1 {
		t.Fatalf("reloaded Len = %d, want %d", reloaded.Len(), s.Len()+1)
	}
	if got, ok := reloaded.Get(newKey); !ok || string(got) != "new" {
		t.Fatalf("new key missing after re-checkpoint: %q, %v", got, ok)
	}
}

func TestCheckpointTamperDetected(t *testing.T) {
	s, _ := buildPopulatedStore(t)

	cases := []struct {
		name   string
		mutate func(raw []byte) []byte
	}{
		{"truncated", func(raw []byte) []byte { return raw[:len(raw)/2] }},
		{"bit-flipped", func(raw []byte) []byte {
			cp := make([]byte, len(raw))
			copy(cp, raw)
			cp[len(cp)/2] ^= 0xff
			return cp
		}},
		{"garbage", func(raw []byte) []byte { return []byte("not json at all") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := tempDir(t)
			defer os.RemoveAll(dir)
			path := filepath.Join(dir, "checkpoint.json")
			if err := s.SaveCheckpoint(path); err != nil {
				t.Fatalf("save: %v", err)
			}
			raw, err := ioutil.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := ioutil.WriteFile(path, tc.mutate(raw), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadCheckpoint(path); err == nil {
				t.Fatal("tampered checkpoint loaded without error")
			} else if !strings.Contains(err.Error(), ErrCheckpointCorrupt.Error()) {
				t.Fatalf("error %v does not report corruption", err)
			}
		})
	}
}

func TestCheckpointAtomicNoTempLeftBehind(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "checkpoint.json")
	s, _ := buildPopulatedStore(t)
	if err := s.SaveCheckpoint(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "checkpoint.json" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("unexpected files left behind: %v", names)
	}
}
