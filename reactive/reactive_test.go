package reactive

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEffectRerunsOnDependencyChange(t *testing.T) {
	s := NewSignal[int](nil, "counter", 0)
	runs, last := 0, -1
	e := NewEffect(func() {
		runs++
		last = s.Get()
	})
	if runs != 1 || last != 0 {
		t.Fatalf("initial run: runs=%d last=%d", runs, last)
	}
	s.Set(5)
	if runs != 2 || last != 5 {
		t.Fatalf("after set: runs=%d last=%d", runs, last)
	}
	e.Dispose()
	s.Set(6)
	if runs != 2 {
		t.Fatalf("disposed effect reran: runs=%d", runs)
	}
}

func TestComputedChainReevaluates(t *testing.T) {
	store := NewStore()
	a := NewSignal(store, "@persist a", 1)
	double := NewComputed(store, "double", func() int { return a.Get() * 2 })
	quad := NewComputed(store, "quad", func() int { return double.Get() * 2 })
	if got := quad.Get(); got != 4 {
		t.Fatalf("quad=%d, want 4", got)
	}
	a.Set(3)
	if got := quad.Get(); got != 12 {
		t.Fatalf("quad=%d, want 12", got)
	}
}

func TestCheckpointContainsOnlyPersistedSignals(t *testing.T) {
	store := NewStore()
	NewSignal(store, "@persist kept", 42)
	NewSignal(store, "scratch", "temp")

	path := filepath.Join(t.TempDir(), "cp.json")
	if err := store.SaveCheckpoint(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		t.Fatal(err)
	}
	if len(cp.Signals) != 1 {
		t.Fatalf("checkpoint signals = %v, want exactly 1", cp.Signals)
	}
	var got int
	if err := json.Unmarshal(cp.Signals["kept"], &got); err != nil || got != 42 {
		t.Fatalf("kept=%d err=%v", got, err)
	}
	if strings.Contains(string(data), "scratch") {
		t.Fatal("transient signal leaked into checkpoint")
	}
	if names := store.PersistedNames(); len(names) != 1 || names[0] != "kept" {
		t.Fatalf("PersistedNames=%v", names)
	}
}

func TestHydrationRestoresStateAndRealignsEffects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.json")

	// ---- process 1: live system, mixed transient + persisted state ----
	{
		store := NewStore()
		counter := NewSignal(store, "@persist counter", 0)
		user := NewSignal(store, "@persist user", "anonymous")
		session := NewSignal(store, "session", "<none>")
		counter.Set(7)
		user.Set("alice")
		session.Set("sess-123")
		if err := store.SaveCheckpoint(path); err != nil {
			t.Fatal(err)
		}
	}

	// ---- process 2: restarted, fresh memory ----
	store := NewStore()
	counter := NewSignal(store, "@persist counter", 0)
	user := NewSignal(store, "@persist user", "anonymous")
	session := NewSignal(store, "session", "<none>")
	doubled := NewComputed(store, "doubled", func() int { return counter.Get() * 2 })

	type observation struct {
		counter int
		doubled int
		user    string
	}
	var seen []observation
	NewEffect(func() {
		seen = append(seen, observation{counter.Get(), doubled.Get(), user.Get()})
	})

	if err := store.LoadCheckpoint(path); err != nil {
		t.Fatal(err)
	}

	if counter.Peek() != 7 || user.Peek() != "alice" {
		t.Fatalf("persisted state not restored: counter=%d user=%q", counter.Peek(), user.Peek())
	}
	if got := session.Peek(); got != "<none>" {
		t.Fatalf("transient state leaked across restart: %q", got)
	}
	if got := doubled.Peek(); got != 14 {
		t.Fatalf("derived value not re-aligned: doubled=%d, want 14", got)
	}
	if len(seen) < 2 {
		t.Fatalf("effect did not re-run during hydration: %d runs", len(seen))
	}
	if last := seen[len(seen)-1]; last != (observation{7, 14, "alice"}) {
		t.Fatalf("final observation = %+v, want {7 14 alice}", last)
	}
}

func TestHydrationIgnoresUnknownSignals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.json")
	blob := `{"version":1,"created_at":"2026-01-01T00:00:00Z","signals":{"ghost":1,"counter":3}}`
	if err := os.WriteFile(path, []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	counter := NewSignal(store, "@persist counter", 0)
	if err := store.LoadCheckpoint(path); err != nil {
		t.Fatal(err)
	}
	if got := counter.Peek(); got != 3 {
		t.Fatalf("counter=%d, want 3", got)
	}
}

func TestHydrationRejectsBadVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cp.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"signals":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewStore().LoadCheckpoint(path); err == nil {
		t.Fatal("expected version error, got nil")
	}
}

func TestStructSignalRoundTrip(t *testing.T) {
	type Profile struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	path := filepath.Join(t.TempDir(), "cp.json")

	store := NewStore()
	p := NewSignal(store, "@persist profile", Profile{})
	p.Set(Profile{Name: "bob", Tags: []string{"admin", "ops"}})
	if err := store.SaveCheckpoint(path); err != nil {
		t.Fatal(err)
	}

	store2 := NewStore()
	p2 := NewSignal(store2, "@persist profile", Profile{})
	if err := store2.LoadCheckpoint(path); err != nil {
		t.Fatal(err)
	}
	got := p2.Peek()
	if got.Name != "bob" || len(got.Tags) != 2 || got.Tags[1] != "ops" {
		t.Fatalf("profile=%+v", got)
	}
}
