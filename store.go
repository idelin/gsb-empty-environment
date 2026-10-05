package blockgc

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ErrNotFound is returned when a block does not exist in the live area.
var ErrNotFound = fmt.Errorf("block not found")

// Block is a unit of shared content. Refs holds IDs of other blocks this
// block points to. References may dangle (target missing) or form cycles;
// the store itself imposes no validity constraints.
type Block struct {
	ID   string   `json:"id"`
	Refs []string `json:"refs,omitempty"`
	Data string   `json:"data,omitempty"`
}

// Store is a filesystem-backed block store with three areas:
//
//	blocks/     live blocks
//	quarantine/ blocks pending deletion (moved here by the GC sweep)
//	journal/    per-epoch confirmation records (append-only JSONL)
//
// All mutations are atomic (tmp file + rename) so a crash leaves either the
// old or the new state, never a torn file.
type Store struct {
	dir string
	mu  sync.Mutex
}

// OpenStore creates (if needed) and opens the store rooted at dir.
func OpenStore(dir string) (*Store, error) {
	for _, sub := range []string{"blocks", "quarantine", "journal"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

func validID(id string) bool {
	return id != "" && !strings.ContainsAny(id, "/\\") && id != "." && id != ".."
}

// Put writes a block atomically, replacing any existing block with the same ID.
func (s *Store) Put(b Block) error {
	if !validID(b.ID) {
		return fmt.Errorf("invalid block id %q", b.ID)
	}
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tmp, err := ioutil.TempFile(filepath.Join(s.dir, "blocks"), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.blockPath(b.ID))
}

// Get reads a live block. Returns ErrNotFound if absent, or a parse error
// if the on-disk content is corrupt.
func (s *Store) Get(id string) (Block, error) {
	var b Block
	data, err := ioutil.ReadFile(s.blockPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return b, ErrNotFound
		}
		return b, err
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("corrupt block %q: %v", id, err)
	}
	return b, nil
}

// getAny reads a block from the live area, falling back to quarantine.
// Used by verification traversals: a quarantined block's references are
// still followed so that restoring it also protects everything it keeps
// alive.
func (s *Store) getAny(id string) (Block, error) {
	b, err := s.Get(id)
	if err == nil {
		return b, nil
	}
	data, err := ioutil.ReadFile(s.quarantinePath(id))
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(data, &b); err != nil {
		return b, fmt.Errorf("corrupt block %q: %v", id, err)
	}
	return b, nil
}

// List returns the IDs of all live blocks.
func (s *Store) List() ([]string, error) {
	return listDir(filepath.Join(s.dir, "blocks"))
}

// ListQuarantined returns the IDs of all blocks sitting in quarantine.
func (s *Store) ListQuarantined() ([]string, error) {
	return listDir(filepath.Join(s.dir, "quarantine"))
}

// Quarantine moves a live block into the quarantine area. It is a rename,
// hence atomic; a crash either leaves the block live or quarantined.
func (s *Store) Quarantine(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.Rename(s.blockPath(id), s.quarantinePath(id))
}

// Restore moves a quarantined block back to the live area.
func (s *Store) Restore(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.Rename(s.quarantinePath(id), s.blockPath(id))
}

// DeleteQuarantined permanently removes a quarantined block.
func (s *Store) DeleteQuarantined(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := os.Remove(s.quarantinePath(id))
	if os.IsNotExist(err) {
		return nil // deletion is idempotent
	}
	return err
}

func (s *Store) blockPath(id string) string {
	return filepath.Join(s.dir, "blocks", id)
}

func (s *Store) quarantinePath(id string) string {
	return filepath.Join(s.dir, "quarantine", id)
}

func listDir(dir string) ([]string, error) {
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, fi := range fis {
		if fi.Mode().IsRegular() && !strings.HasPrefix(fi.Name(), ".") {
			ids = append(ids, fi.Name())
		}
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// Journal: confirmation records for quarantine/restore/confirm/delete.
// One append-only JSONL file per GC epoch; replayed after a crash.
// ---------------------------------------------------------------------------

type journalOp struct {
	Epoch int    `json:"epoch"`
	ID    string `json:"id"`
	Op    string `json:"op"` // "quarantine" | "restore" | "confirm" | "delete"
}

type journal struct {
	path string
	f    *os.File
}

// nextEpoch returns a fresh, monotonically increasing epoch number.
func (s *Store) nextEpoch() (int, error) {
	fis, err := ioutil.ReadDir(filepath.Join(s.dir, "journal"))
	if err != nil {
		return 0, err
	}
	max := 0
	for _, fi := range fis {
		name := fi.Name()
		if !strings.HasPrefix(name, "epoch-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "epoch-"), ".jsonl"))
		if err == nil && n > max {
			max = n
		}
	}
	return max + 1, nil
}

func (s *Store) openJournal(epoch int) (*journal, error) {
	path := filepath.Join(s.dir, "journal", fmt.Sprintf("epoch-%06d.jsonl", epoch))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &journal{path: path, f: f}, nil
}

func (j *journal) append(epoch int, id, op string) error {
	rec, err := json.Marshal(journalOp{Epoch: epoch, ID: id, Op: op})
	if err != nil {
		return err
	}
	if _, err := j.f.Write(append(rec, '\n')); err != nil {
		return err
	}
	return j.f.Sync() // durability: record survives a crash
}

func (j *journal) close() error { return j.f.Close() }

// replayJournals folds every journal file into the last recorded op per
// block ID. Used by crash recovery to distinguish blocks whose deletion was
// already confirmed from blocks that still need re-verification.
func (s *Store) replayJournals() (map[string]string, error) {
	fis, err := ioutil.ReadDir(filepath.Join(s.dir, "journal"))
	if err != nil {
		return nil, err
	}
	last := map[string]string{}
	for _, fi := range fis {
		if !strings.HasSuffix(fi.Name(), ".jsonl") {
			continue
		}
		data, err := ioutil.ReadFile(filepath.Join(s.dir, "journal", fi.Name()))
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if line == "" {
				continue
			}
			var op journalOp
			if err := json.Unmarshal([]byte(line), &op); err != nil {
				continue // tolerate a torn trailing record
			}
			last[op.ID] = op.Op
		}
	}
	return last, nil
}
