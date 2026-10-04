package splitter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// CrashHooks are invoked at durable-state boundaries inside Apply so tests
// can simulate a process crash (typically by panicking) at exact points.
type CrashHooks struct {
	AfterIntentPersisted  func() // intent journaled, target not yet written
	AfterTargetWrite      func() // target write confirmed, pointer not yet committed
	AfterPointerCommitted func() // pointer committed, journal entry not yet closed
}

// Store is the dual-target splitter. All mutating operations are serialized;
// every state transition is persisted before the next one begins, so a crash
// at any point is recoverable by rolling intents forward.
type Store struct {
	Dir     string
	Online  Target
	Archive Target
	Hooks   CrashHooks

	mu       sync.Mutex
	journal  *journal
	seq      uint64
	pointers map[string]Pointer
}

// Open (re)opens a Store rooted at dir and replays any interrupted intents.
// Online/Archive default to FileTargets under dir when not set by the caller;
// inject custom targets (e.g. FaultTarget) before calling Open via NewStore.
func Open(dir string) (*Store, error) {
	return NewStore(dir, nil, nil)
}

// NewStore is Open with injectable targets; nil targets become FileTargets.
func NewStore(dir string, online, archive Target) (*Store, error) {
	if online == nil {
		online = &FileTarget{Dir: filepath.Join(dir, "online")}
	}
	if archive == nil {
		archive = &FileTarget{Dir: filepath.Join(dir, "archive")}
	}
	s := &Store{
		Dir:      dir,
		Online:   online,
		Archive:  archive,
		journal:  &journal{dir: filepath.Join(dir, "journal")},
		pointers: map[string]Pointer{},
	}
	for _, sub := range []string{"journal", "pointers", "snapshots"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	if err := s.loadPointers(); err != nil {
		return nil, err
	}
	seq, err := s.journal.maxSeq()
	if err != nil {
		return nil, err
	}
	s.seq = seq
	if err := s.Recover(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) target(loc Location) Target {
	if loc == Archive {
		return s.Archive
	}
	return s.Online
}

func (s *Store) pointerPath(key string) string {
	return filepath.Join(s.Dir, "pointers", encodeKey(key)+".json")
}

func (s *Store) loadPointers() error {
	files, err := os.ReadDir(filepath.Join(s.Dir, "pointers"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.Dir, "pointers", f.Name()))
		if err != nil {
			return err
		}
		var p Pointer
		if err := json.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("corrupt pointer %s: %v", f.Name(), err)
		}
		s.pointers[p.Key] = p
	}
	return nil
}

// commitPointer atomically persists the pointer and updates the in-memory view.
func (s *Store) commitPointer(p Pointer) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.pointerPath(p.Key), data); err != nil {
		return err
	}
	s.pointers[p.Key] = p
	return nil
}

// Apply routes one event. Only a strictly higher version is accepted; the
// same version with identical content is an idempotent no-op, and the same
// version with different content is a conflict. The event's destination is
// written and confirmed before the version pointer is committed, so readers
// only ever see one confirmed location per key.
func (s *Store) Apply(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(ev)
}

func (s *Store) applyLocked(ev Event) error {
	if ev.Key == "" {
		return fmt.Errorf("empty key")
	}
	if p, ok := s.pointers[ev.Key]; ok {
		switch {
		case ev.Version < p.Version:
			return ErrStale
		case ev.Version == p.Version:
			if ev.Hash() == p.Hash && ev.IsDeleted == p.IsDeleted {
				return nil // idempotent replay
			}
			return ErrConflict
		}
	}

	s.seq++
	entry := journalEntry{
		Seq:       s.seq,
		Key:       ev.Key,
		Version:   ev.Version,
		Content:   ev.Content,
		IsDeleted: ev.IsDeleted,
		Hash:      ev.Hash(),
		Dest:      ev.Dest(),
		State:     StateIntended,
	}
	if err := s.journal.save(entry); err != nil {
		return err
	}
	if s.Hooks.AfterIntentPersisted != nil {
		s.Hooks.AfterIntentPersisted()
	}

	if err := s.target(entry.Dest).Put(entry.record()); err != nil {
		return err // intent stays journaled; recovery will redo/confirm
	}
	entry.State = StateTargetWritten
	if err := s.journal.save(entry); err != nil {
		return err
	}
	if s.Hooks.AfterTargetWrite != nil {
		s.Hooks.AfterTargetWrite()
	}

	if err := s.commitPointer(Pointer{
		Key:       ev.Key,
		Version:   ev.Version,
		Location:  entry.Dest,
		Hash:      entry.Hash,
		IsDeleted: ev.IsDeleted,
	}); err != nil {
		return err
	}
	if s.Hooks.AfterPointerCommitted != nil {
		s.Hooks.AfterPointerCommitted()
	}

	return s.journal.remove(entry)
}

// Recover rolls every interrupted intent forward, in order. It is idempotent
// and safe to run any number of times; Open runs it automatically.
func (s *Store) Recover() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.journal.load()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := s.recoverEntry(e); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) recoverEntry(e journalEntry) error {
	if p, ok := s.pointers[e.Key]; ok && p.Version >= e.Version {
		// Pointer already at or past this intent (crash after pointer commit,
		// or a later event superseded it). Never roll the pointer back: a
		// delayed/duplicated delete must not resurrect an old archived copy.
		return s.journal.remove(e)
	}
	// Pointer not yet committed for this version. The target write may or may
	// not have landed (ack lost, or crash before the write); make it so.
	rec, err := s.target(e.Dest).Get(e.Key, e.Version)
	if err == ErrNotFound || (err == nil && rec.Hash != e.Hash) {
		if werr := s.target(e.Dest).Put(e.record()); werr != nil {
			return werr
		}
	} else if err != nil {
		return err
	}
	if err := s.commitPointer(Pointer{
		Key:       e.Key,
		Version:   e.Version,
		Location:  e.Dest,
		Hash:      e.Hash,
		IsDeleted: e.IsDeleted,
	}); err != nil {
		return err
	}
	return s.journal.remove(e)
}

// Get resolves a key through its committed pointer: exactly one location is
// consulted, so a stale copy on the other side is never visible.
func (s *Store) Get(key string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pointers[key]
	if !ok {
		return Record{}, ErrNotFound
	}
	return s.target(p.Location).Get(p.Key, p.Version)
}

// Pointer returns the committed pointer for a key.
func (s *Store) Pointer(key string) (Pointer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pointers[key]
	return p, ok
}

// Pointers returns every committed pointer, ordered by key.
func (s *Store) Pointers() []Pointer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Pointer, 0, len(s.pointers))
	for _, p := range s.pointers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Scan resolves every key through its pointer (the committed read view).
func (s *Store) Scan() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.pointers))
	for k := range s.pointers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Record, 0, len(keys))
	for _, k := range keys {
		p := s.pointers[k]
		rec, err := s.target(p.Location).Get(p.Key, p.Version)
		if err != nil {
			return nil, fmt.Errorf("pointer %s@%d -> %s: %v", k, p.Version, p.Location, err)
		}
		out = append(out, rec)
	}
	return out, nil
}
