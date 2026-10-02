package reactive

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// CheckpointVersion is the on-disk checkpoint format version.
const CheckpointVersion = 1

// Checkpoint is the JSON-serializable snapshot of all @persist signals.
type Checkpoint struct {
	Version   int                        `json:"version"`
	CreatedAt time.Time                  `json:"created_at"`
	Signals   map[string]json.RawMessage `json:"signals"`
}

// boundSignal adapts a generic Signal[T] to untyped JSON accessors.
type boundSignal struct {
	get func() (json.RawMessage, error)
	set func(json.RawMessage) error
}

// Store owns the registry of @persist-annotated signals and provides
// checkpoint save/load.
type Store struct {
	mu    sync.RWMutex
	bound map[string]boundSignal
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{bound: make(map[string]boundSignal)}
}

func (st *Store) register(name string, b boundSignal) {
	st.mu.Lock()
	st.bound[name] = b
	st.mu.Unlock()
}

// PersistedNames returns the sorted names of all @persist signals.
func (st *Store) PersistedNames() []string {
	st.mu.RLock()
	defer st.mu.RUnlock()
	names := make([]string, 0, len(st.bound))
	for name := range st.bound {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// NewSignal creates a Signal owned by store. The name may carry
// annotations, e.g. "@persist counter": the "@persist" annotation
// marks the signal for inclusion in checkpoints. A nil store creates
// a standalone, always-transient signal.
func NewSignal[T any](store *Store, annotatedName string, initial T) *Signal[T] {
	name, annotations := parseName(annotatedName)
	sig := newSignal(name, initial)
	if store != nil && annotations["persist"] {
		store.register(name, boundSignal{
			get: func() (json.RawMessage, error) {
				raw, err := json.Marshal(sig.Peek())
				if err != nil {
					return nil, fmt.Errorf("marshal %q: %w", name, err)
				}
				return raw, nil
			},
			set: func(raw json.RawMessage) error {
				var v T
				if err := json.Unmarshal(raw, &v); err != nil {
					return fmt.Errorf("hydrate %q: %w", name, err)
				}
				sig.Set(v) // notifies effects: dependency chain re-aligns
				return nil
			},
		})
	}
	return sig
}

// parseName splits "@persist counter" into ("counter", {"persist": true}).
func parseName(s string) (string, map[string]bool) {
	annotations := make(map[string]bool)
	var nameParts []string
	for _, tok := range strings.Fields(s) {
		if strings.HasPrefix(tok, "@") && len(tok) > 1 {
			annotations[strings.TrimPrefix(tok, "@")] = true
		} else {
			nameParts = append(nameParts, tok)
		}
	}
	return strings.Join(nameParts, " "), annotations
}

// SaveCheckpoint serializes every @persist signal to path as JSON.
// The write is atomic (temp file + rename).
func (st *Store) SaveCheckpoint(path string) error {
	st.mu.RLock()
	defer st.mu.RUnlock()
	cp := Checkpoint{
		Version:   CheckpointVersion,
		CreatedAt: time.Now().UTC(),
		Signals:   make(map[string]json.RawMessage, len(st.bound)),
	}
	for name, b := range st.bound {
		raw, err := b.get()
		if err != nil {
			return err
		}
		cp.Signals[name] = raw
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadCheckpoint incrementally hydrates @persist signals from a
// checkpoint file. Each value is applied through Signal.Set in
// deterministic (sorted-name) order, so dependent Computed values and
// Effects re-align as every signal comes back. Checkpoint entries
// without a live signal are skipped; transient signals are untouched
// and keep their freshly initialized values.
func (st *Store) LoadCheckpoint(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return fmt.Errorf("parse checkpoint: %w", err)
	}
	if cp.Version != CheckpointVersion {
		return fmt.Errorf("unsupported checkpoint version %d", cp.Version)
	}
	names := make([]string, 0, len(cp.Signals))
	for name := range cp.Signals {
		names = append(names, name)
	}
	sort.Strings(names)
	st.mu.RLock()
	defer st.mu.RUnlock()
	for _, name := range names {
		b, ok := st.bound[name]
		if !ok {
			continue
		}
		if err := b.set(cp.Signals[name]); err != nil {
			return err
		}
	}
	return nil
}
