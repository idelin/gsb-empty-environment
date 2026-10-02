package reactive

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"time"
)

// CheckpointVersion is the on-disk schema version of JSON checkpoints.
const CheckpointVersion = 1

// Checkpoint is the durable snapshot of a Store. Only Signals carrying the
// @persist annotation are included; transient state never leaves memory.
type Checkpoint struct {
	Version int                        `json:"version"`
	SavedAt time.Time                  `json:"savedAt"`
	Signals map[string]json.RawMessage `json:"signals"`
}

// HydrationReport describes what an incremental hydration pass did.
type HydrationReport struct {
	// Restored lists @persist Signals whose values were loaded, in the
	// deterministic (sorted) order they were applied.
	Restored []string
	// SkippedUnknown lists checkpoint entries with no registered Signal.
	SkippedUnknown []string
	// SkippedTransient lists checkpoint entries whose registered Signal
	// lacks the @persist annotation; they are never hydrated.
	SkippedTransient []string
}

// Snapshot builds an in-memory Checkpoint of the @persist Signals.
func (st *Store) Snapshot() (*Checkpoint, error) {
	cp := &Checkpoint{
		Version: CheckpointVersion,
		SavedAt: time.Now().UTC(),
		Signals: make(map[string]json.RawMessage),
	}
	for _, sig := range st.Signals() {
		if !sig.Persistent() {
			continue
		}
		raw, err := json.Marshal(sig.Peek())
		if err != nil {
			return nil, fmt.Errorf("checkpoint: marshal signal %q: %w", sig.name, err)
		}
		cp.Signals[sig.name] = raw
	}
	return cp, nil
}

// SaveCheckpoint serializes the @persist Signals as a JSON checkpoint.
func (st *Store) SaveCheckpoint(w io.Writer) error {
	cp, err := st.Snapshot()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(cp)
}

// SaveCheckpointFile atomically writes a JSON checkpoint to path.
func (st *Store) SaveCheckpointFile(path string) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := st.SaveCheckpoint(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Hydrate incrementally restores a checkpoint into the store. Signals are
// applied one at a time in deterministic order; every applied Set triggers
// the dependent Effects, so the derived graph re-aligns step by step as
// hydration progresses. Transient Signals are never touched and keep their
// freshly-initialized (reset) values.
func (st *Store) Hydrate(cp *Checkpoint) (*HydrationReport, error) {
	if cp.Version != CheckpointVersion {
		return nil, fmt.Errorf("hydrate: unsupported checkpoint version %d", cp.Version)
	}
	report := &HydrationReport{}

	names := make([]string, 0, len(cp.Signals))
	for name := range cp.Signals {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		sig := st.Lookup(name)
		if sig == nil {
			report.SkippedUnknown = append(report.SkippedUnknown, name)
			continue
		}
		if !sig.Persistent() {
			report.SkippedTransient = append(report.SkippedTransient, name)
			continue
		}
		value, err := decodeLike(sig.Peek(), cp.Signals[name])
		if err != nil {
			return report, fmt.Errorf("hydrate: decode signal %q: %w", name, err)
		}
		sig.Set(value) // triggers dependent Effects: incremental re-alignment
		report.Restored = append(report.Restored, name)
	}
	return report, nil
}

// LoadCheckpoint reads a JSON checkpoint and hydrates the store from it.
func (st *Store) LoadCheckpoint(r io.Reader) (*HydrationReport, error) {
	var cp Checkpoint
	if err := json.NewDecoder(r).Decode(&cp); err != nil {
		return nil, fmt.Errorf("hydrate: parse checkpoint: %w", err)
	}
	return st.Hydrate(&cp)
}

// LoadCheckpointFile loads a JSON checkpoint from path and hydrates the store.
func (st *Store) LoadCheckpointFile(path string) (*HydrationReport, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return st.LoadCheckpoint(f)
}

// decodeLike unmarshals raw into a value of the same concrete type as the
// signal's current value, so hydration preserves static types (e.g. int
// stays int, not float64). Falls back to generic decoding for nil values.
func decodeLike(current any, raw json.RawMessage) (any, error) {
	if current == nil {
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return v, nil
	}
	ptr := reflect.New(reflect.TypeOf(current))
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return nil, err
	}
	return ptr.Elem().Interface(), nil
}
