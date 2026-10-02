package reactive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// buildAppStore wires the same state tree used across tests: a mix of
// @persist and transient Signals plus a derived chain
// count -> doubled -> effect log.
func buildAppStore() (*Store, *Signal, *Signal, *Signal, *[]string) {
	st := NewStore()

	count := st.Signal("count", 0, Persist())         // @persist
	user := st.Signal("user", "anonymous", Persist()) // @persist
	draft := st.Signal("draft", "")                   // transient
	_ = draft

	doubled := st.Computed("doubled", func() any {
		return count.Get().(int) * 2
	})

	log := &[]string{}
	st.Effect(func() {
		*log = append(*log, user.Get().(string)+":"+strconv.Itoa(doubled.Get().(int)))
	})
	return st, count, user, doubled, log
}

func TestCheckpointContainsOnlyPersistedSignals(t *testing.T) {
	st := NewStore()
	st.Signal("count", 42, Persist())
	st.Signal("user", "alice", Persist())
	st.Signal("draft", "unsent typing...") // transient
	st.Signal("scrollPos", 17.5)           // transient

	var buf bytes.Buffer
	if err := st.SaveCheckpoint(&buf); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(buf.Bytes(), &cp); err != nil {
		t.Fatalf("unmarshal checkpoint: %v", err)
	}
	if cp.Version != CheckpointVersion {
		t.Errorf("version = %d, want %d", cp.Version, CheckpointVersion)
	}
	got := make([]string, 0, len(cp.Signals))
	for name := range cp.Signals {
		got = append(got, name)
	}
	want := []string{"count", "user"}
	if len(got) != len(want) ||
		!(got[0] == want[0] || got[0] == want[1]) ||
		!(got[1] == want[0] || got[1] == want[1]) {
		t.Errorf("checkpoint signals = %v, want exactly %v", got, want)
	}
	for _, transient := range []string{"draft", "scrollPos"} {
		if _, ok := cp.Signals[transient]; ok {
			t.Errorf("transient signal %q leaked into checkpoint", transient)
		}
	}
}

func TestRestartRestoresPersistedAndResetsTransient(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	// --- Process lifetime 1: mutate state, write checkpoint. ---
	st1 := NewStore()
	count := st1.Signal("count", 0, Persist())
	user := st1.Signal("user", "anonymous", Persist())
	draft := st1.Signal("draft", "")
	count.Set(41)
	count.Set(42)
	user.Set("alice")
	draft.Set("half-typed message") // transient, must NOT survive restart
	if err := st1.SaveCheckpointFile(path); err != nil {
		t.Fatalf("SaveCheckpointFile: %v", err)
	}

	// --- Process lifetime 2: fresh store, same registrations, hydrate. ---
	st2 := NewStore()
	count2 := st2.Signal("count", 0, Persist())
	user2 := st2.Signal("user", "anonymous", Persist())
	draft2 := st2.Signal("draft", "")

	var observed []int
	eff := st2.Effect(func() {
		observed = append(observed, count2.Get().(int))
	})

	report, err := st2.LoadCheckpointFile(path)
	if err != nil {
		t.Fatalf("LoadCheckpointFile: %v", err)
	}

	if got := count2.Get().(int); got != 42 {
		t.Errorf("persisted count = %d, want 42", got)
	}
	if got := user2.Get().(string); got != "alice" {
		t.Errorf("persisted user = %q, want %q", got, "alice")
	}
	if got := draft2.Get().(string); got != "" {
		t.Errorf("transient draft = %q, want safely reset to %q", got, "")
	}
	// The effect ran once at registration (initial 0) and once more when
	// hydration applied count=42: derived state re-aligned automatically.
	wantObserved := []int{0, 42}
	if !reflect.DeepEqual(observed, wantObserved) {
		t.Errorf("effect observations = %v, want %v", observed, wantObserved)
	}
	if eff.Runs() != 2 {
		t.Errorf("effect runs = %d, want 2", eff.Runs())
	}
	if !reflect.DeepEqual(report.Restored, []string{"count", "user"}) {
		t.Errorf("restored = %v, want [count user]", report.Restored)
	}
	if len(report.SkippedUnknown) != 0 || len(report.SkippedTransient) != 0 {
		t.Errorf("unexpected skips: %+v", report)
	}
}

func TestDependencyChainRealignsAfterHydration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	// Lifetime 1.
	st1, count1, user1, _, _ := buildAppStore()
	count1.Set(21)
	user1.Set("bob")
	if err := st1.SaveCheckpointFile(path); err != nil {
		t.Fatalf("SaveCheckpointFile: %v", err)
	}

	// Lifetime 2.
	st2, _, _, doubled2, log := buildAppStore()
	if _, err := st2.LoadCheckpointFile(path); err != nil {
		t.Fatalf("LoadCheckpointFile: %v", err)
	}

	// Derived chain count -> doubled -> effect must be fully re-evaluated.
	if got := doubled2.Get().(int); got != 42 {
		t.Errorf("derived doubled = %d, want 42", got)
	}
	last := (*log)[len(*log)-1]
	if last != "bob:42" {
		t.Errorf("final derived effect output = %q, want %q", last, "bob:42")
	}
}

func TestHydrationSkipsUnknownAndTransientEntries(t *testing.T) {
	st := NewStore()
	st.Signal("count", 0, Persist())
	draft := st.Signal("draft", "reset")

	// Hand-crafted checkpoint: one valid entry, one entry targeting a
	// transient Signal, one entry for a Signal that no longer exists.
	raw := `{
	  "version": 1,
	  "savedAt": "2026-10-02T00:00:00Z",
	  "signals": {
	    "count": 7,
	    "draft": "forged",
	    "removed": 1
	  }
	}`
	report, err := st.LoadCheckpoint(bytes.NewBufferString(raw))
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}

	if got := st.Lookup("count").Get().(int); got != 7 {
		t.Errorf("count = %d, want 7", got)
	}
	if got := draft.Get().(string); got != "reset" {
		t.Errorf("transient draft = %q, must stay %q", got, "reset")
	}
	if !reflect.DeepEqual(report.Restored, []string{"count"}) {
		t.Errorf("restored = %v, want [count]", report.Restored)
	}
	if !reflect.DeepEqual(report.SkippedTransient, []string{"draft"}) {
		t.Errorf("skipped transient = %v, want [draft]", report.SkippedTransient)
	}
	if !reflect.DeepEqual(report.SkippedUnknown, []string{"removed"}) {
		t.Errorf("skipped unknown = %v, want [removed]", report.SkippedUnknown)
	}
}

func TestHydrationIsIncremental(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	st1 := NewStore()
	a := st1.Signal("a", 0, Persist())
	b := st1.Signal("b", 0, Persist())
	a.Set(1)
	b.Set(2)
	if err := st1.SaveCheckpointFile(path); err != nil {
		t.Fatalf("SaveCheckpointFile: %v", err)
	}

	st2 := NewStore()
	a2 := st2.Signal("a", 0, Persist())
	b2 := st2.Signal("b", 0, Persist())
	// Observes the pair (a,b) on every re-evaluation: hydration applies
	// signals one by one, so intermediate states must be visible.
	type pair struct{ a, b int }
	var seen []pair
	st2.Effect(func() {
		seen = append(seen, pair{a2.Get().(int), b2.Get().(int)})
	})

	if _, err := st2.LoadCheckpointFile(path); err != nil {
		t.Fatalf("LoadCheckpointFile: %v", err)
	}

	want := []pair{{0, 0}, {1, 0}, {1, 2}} // initial, after a, after b
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("incremental states = %v, want %v", seen, want)
	}
}

func TestUnsupportedCheckpointVersion(t *testing.T) {
	st := NewStore()
	_, err := st.LoadCheckpoint(bytes.NewBufferString(`{"version": 99, "signals": {}}`))
	if err == nil {
		t.Fatal("expected error for unsupported checkpoint version")
	}
}

func TestCheckpointFileIsValidJSONOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "checkpoint.json")

	st := NewStore()
	st.Signal("count", 42, Persist())
	st.Signal("draft", "nope")
	if err := st.SaveCheckpointFile(path); err != nil {
		t.Fatalf("SaveCheckpointFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("checkpoint file is not valid JSON: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary checkpoint file was not cleaned up")
	}
}
