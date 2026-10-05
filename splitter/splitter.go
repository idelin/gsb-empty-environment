package splitter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ApplyResult classifies the outcome of a successfully processed event.
type ApplyResult string

const (
	ResultApplied    ApplyResult = "applied"    // new highest version, migration executed
	ResultStale      ApplyResult = "stale"      // lower version, ignored (incl. delayed deletes)
	ResultIdempotent ApplyResult = "idempotent" // same version + same content, no-op
)

// Hooks are optional fault-injection points around the non-atomic steps of
// a migration. Tests panic from them to simulate a process crash; the
// crashed Splitter instance must then be discarded and reopened.
type Hooks struct {
	AfterPointerCommit func() // pointer durable, journal not yet updated
	BeforeCleanup      func() // about to delete the previous copy
	AfterCleanup       func() // previous copy deleted, journal not yet updated
}

// Config wires a Splitter. Stores may override the targets (e.g. with
// fault-injecting adapters); nil stores default to file adapters under Dir.
type Config struct {
	Dir    string
	Stores map[Location]BlobStore
	Hooks  *Hooks
}

// Record is the result of a Get: the confirmed pointer plus its content.
type Record struct {
	Pointer Pointer
	Content []byte
}

// Splitter routes versioned records between an online and an archive
// target. It is safe for concurrent use by a single process; crash safety
// comes from the journal + atomic pointer commits, not from this mutex.
type Splitter struct {
	mu          sync.Mutex
	dir         string
	blobs       map[Location]BlobStore
	journal     *Journal
	pointers    *PointerStore
	state       map[string]Pointer // committed pointers
	inflight    map[string]Intent  // journaled, unfinished intents
	hooks       *Hooks
	snapshotDir string
	snapshots   map[int64][]Pointer
	nextSnapID  int64
}

// Open loads committed pointers and the journal from cfg.Dir, then runs
// recovery so any interrupted migration is driven to completion before
// new events are accepted.
func Open(cfg Config) (*Splitter, error) {
	if cfg.Dir == "" {
		return nil, errors.New("splitter: Config.Dir is required")
	}
	blobs := map[Location]BlobStore{}
	for _, loc := range []Location{LocationOnline, LocationArchive} {
		if cfg.Stores != nil && cfg.Stores[loc] != nil {
			blobs[loc] = cfg.Stores[loc]
			continue
		}
		st, err := NewFileAdapter(filepath.Join(cfg.Dir, string(loc)))
		if err != nil {
			return nil, err
		}
		blobs[loc] = st
	}
	ps, err := NewPointerStore(filepath.Join(cfg.Dir, "pointers"))
	if err != nil {
		return nil, err
	}
	j, err := OpenJournal(filepath.Join(cfg.Dir, "journal"))
	if err != nil {
		return nil, err
	}
	state, err := ps.LoadAll()
	if err != nil {
		return nil, err
	}
	snapshotDir := filepath.Join(cfg.Dir, "snapshots")
	snapshots, maxID, err := loadSnapshots(snapshotDir)
	if err != nil {
		return nil, err
	}
	s := &Splitter{
		dir:         cfg.Dir,
		blobs:       blobs,
		journal:     j,
		pointers:    ps,
		state:       state,
		inflight:    map[string]Intent{},
		hooks:       cfg.Hooks,
		snapshotDir: snapshotDir,
		snapshots:   snapshots,
		nextSnapID:  maxID,
	}
	if err := s.Recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// Apply routes one event. Per key only a higher version is accepted; an
// equal version with identical content is an idempotent no-op, with
// different content a conflict. Lower versions (e.g. delayed deletes) are
// dropped so they can never resurrect an archived record.
func (s *Splitter) Apply(ev Event) (ApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.Key == "" {
		return "", errors.New("splitter: empty key")
	}
	ptr, ok := s.state[ev.Key]
	if ok {
		switch {
		case ev.Version < ptr.Version:
			return ResultStale, nil
		case ev.Version == ptr.Version:
			if ev.Hash() == ptr.Hash && ev.IsDeleted == ptr.Deleted {
				return ResultIdempotent, nil
			}
			return "", &ConflictError{
				Key: ev.Key, Version: ev.Version,
				ExistingHash: ptr.Hash, IncomingHash: ev.Hash(),
			}
		}
	}
	// A previous attempt for this key may have failed after persisting an
	// intent (e.g. lost acknowledgement). Resume or supersede it.
	if in, ok := s.inflight[ev.Key]; ok {
		switch {
		case in.Version == ev.Version && in.Hash == ev.Hash():
			return s.drive(in)
		case in.Version > ev.Version:
			return ResultStale, nil
		default: // newer event supersedes the unfinished intent
			if err := s.journal.Complete(ev.Key); err != nil {
				return "", err
			}
			delete(s.inflight, ev.Key)
		}
	}
	in := Intent{
		Key: ev.Key, Version: ev.Version, Hash: ev.Hash(),
		Deleted: ev.IsDeleted, Location: ev.Location(),
		Content: ev.Content, Phase: PhaseIntent,
	}
	if ok {
		in.HasPrev = true
		in.PrevLocation = ptr.Location
		in.PrevVersion = ptr.Version
	}
	if err := s.journal.Put(in); err != nil {
		return "", err
	}
	s.inflight[ev.Key] = in
	return s.drive(in)
}

// drive executes an intent from its recorded phase to completion. Every
// step is idempotent and journaled before the next one starts.
func (s *Splitter) drive(in Intent) (ApplyResult, error) {
	if in.Phase == PhaseIntent {
		if err := s.blobs[in.Location].Put(BlobName(in.Key, in.Version), in.Content); err != nil {
			return "", err
		}
		in.Phase = PhaseCopied
		if err := s.journal.Put(in); err != nil {
			return "", err
		}
		s.inflight[in.Key] = in
	}
	if in.Phase == PhaseCopied {
		// Commit only if still the highest known version: a newer pointer
		// (committed by a concurrent/retried intent) always wins.
		if ptr, ok := s.state[in.Key]; !ok || ptr.Version < in.Version {
			np := Pointer{
				Key: in.Key, Version: in.Version, Hash: in.Hash,
				Deleted: in.Deleted, Location: in.Location,
			}
			if err := s.pointers.Commit(np); err != nil {
				return "", err
			}
			s.state[in.Key] = np
		}
		if s.hooks != nil && s.hooks.AfterPointerCommit != nil {
			s.hooks.AfterPointerCommit()
		}
		in.Phase = PhaseCommitted
		if err := s.journal.Put(in); err != nil {
			return "", err
		}
		s.inflight[in.Key] = in
	}
	if in.Phase == PhaseCommitted {
		if err := s.cleanupPrev(in); err != nil {
			return "", err
		}
	}
	if err := s.journal.Complete(in.Key); err != nil {
		return "", err
	}
	delete(s.inflight, in.Key)
	return ResultApplied, nil
}

// cleanupPrev removes the superseded copy after a cross-target move.
// Copies pinned by an active snapshot are left for GC.
func (s *Splitter) cleanupPrev(in Intent) error {
	if !in.HasPrev || in.PrevLocation == in.Location {
		return nil
	}
	name := BlobName(in.Key, in.PrevVersion)
	if s.pinned(in.PrevLocation, name) {
		return nil
	}
	if s.hooks != nil && s.hooks.BeforeCleanup != nil {
		s.hooks.BeforeCleanup()
	}
	if err := s.blobs[in.PrevLocation].Delete(name); err != nil {
		return err
	}
	if s.hooks != nil && s.hooks.AfterCleanup != nil {
		s.hooks.AfterCleanup()
	}
	return nil
}

// Recover finishes or discards every journaled intent. It is idempotent
// and safe to run any number of times; Open calls it automatically.
func (s *Splitter) Recover() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	intents, err := s.journal.Intents()
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(intents))
	for k := range intents {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		in := intents[k]
		ptr, ok := s.state[in.Key]
		switch {
		case ok && ptr.Version > in.Version:
			// Superseded by a newer committed version: drop the intent.
			if err := s.journal.Complete(in.Key); err != nil {
				return err
			}
		case ok && ptr.Version == in.Version:
			// Pointer commit already happened; only cleanup may remain.
			if err := s.cleanupPrev(in); err != nil {
				return err
			}
			if err := s.journal.Complete(in.Key); err != nil {
				return err
			}
		default:
			// Pointer not committed (or key unseen): resume the migration.
			if _, err := s.drive(in); err != nil {
				return err
			}
		}
	}
	return nil
}

// Get resolves a key through its committed pointer only, so readers always
// see exactly one confirmed location regardless of in-flight migrations.
func (s *Splitter) Get(key string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ptr, ok := s.state[key]
	if !ok {
		return nil, ErrNotFound
	}
	data, err := s.blobs[ptr.Location].Get(BlobName(ptr.Key, ptr.Version))
	if err != nil {
		return nil, err
	}
	return &Record{Pointer: ptr, Content: data}, nil
}

// ScanAll returns the committed pointer (highest accepted version) per key.
// This is what queries use.
func (s *Splitter) ScanAll() []Pointer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Pointer, 0, len(s.state))
	for _, p := range s.state {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// ReferenceScan is the reference implementation of a full scan: it walks
// both blob stores directly and keeps the highest version per key,
// independent of the pointer store. Tests and operators can cross-check
// pointer state against it.
func (s *Splitter) ReferenceScan() (map[string]Pointer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]Pointer{}
	for loc, store := range s.blobs {
		names, err := store.List()
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			key, version, err := ParseBlobName(n)
			if err != nil {
				continue
			}
			if cur, ok := out[key]; !ok || version > cur.Version {
				out[key] = Pointer{Key: key, Version: version, Location: loc}
			}
		}
	}
	return out, nil
}

// BeginSnapshot pins the current pointer set so GC keeps every blob an
// in-flight reader may still need. The pin is persisted and survives
// restarts until EndSnapshot.
func (s *Splitter) BeginSnapshot() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSnapID++
	id := s.nextSnapID
	ptrs := make([]Pointer, 0, len(s.state))
	for _, p := range s.state {
		ptrs = append(ptrs, p)
	}
	sort.Slice(ptrs, func(i, j int) bool { return ptrs[i].Key < ptrs[j].Key })
	data, err := json.Marshal(ptrs)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(s.snapshotDir, strconv.FormatInt(id, 10)+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return 0, err
	}
	if err := fsyncDir(s.snapshotDir); err != nil {
		return 0, err
	}
	s.snapshots[id] = ptrs
	return id, nil
}

// EndSnapshot releases a pin taken by BeginSnapshot.
func (s *Splitter) EndSnapshot(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, id)
	err := os.Remove(filepath.Join(s.snapshotDir, strconv.FormatInt(id, 10)+".json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return fsyncDir(s.snapshotDir)
}

// pinned reports whether an active snapshot still references a blob.
func (s *Splitter) pinned(loc Location, name string) bool {
	for _, snap := range s.snapshots {
		for _, p := range snap {
			if p.Location == loc && BlobName(p.Key, p.Version) == name {
				return true
			}
		}
	}
	return false
}

// GC deletes only blobs that no committed pointer and no active snapshot
// references. It returns the removed "location/name" identifiers.
func (s *Splitter) GC() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	refs := map[Location]map[string]bool{LocationOnline: {}, LocationArchive: {}}
	addRef := func(p Pointer) { refs[p.Location][BlobName(p.Key, p.Version)] = true }
	for _, p := range s.state {
		addRef(p)
	}
	for _, snap := range s.snapshots {
		for _, p := range snap {
			addRef(p)
		}
	}
	var removed []string
	for loc, store := range s.blobs {
		names, err := store.List()
		if err != nil {
			return removed, err
		}
		for _, n := range names {
			if refs[loc][n] {
				continue
			}
			if err := store.Delete(n); err != nil {
				return removed, err
			}
			removed = append(removed, fmt.Sprintf("%s/%s", loc, n))
		}
	}
	sort.Strings(removed)
	return removed, nil
}

func loadSnapshots(dir string) (map[int64][]Pointer, int64, error) {
	out := map[int64][]Pointer{}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, 0, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var maxID int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSuffix(e.Name(), ".json"), 10, 64)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, 0, err
		}
		var ptrs []Pointer
		if err := json.Unmarshal(data, &ptrs); err != nil {
			return nil, 0, err
		}
		out[id] = ptrs
		if id > maxID {
			maxID = id
		}
	}
	return out, maxID, nil
}
