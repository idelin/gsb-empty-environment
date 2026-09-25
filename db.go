package pagebackup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// PageSize is the fixed size of one database page.
const PageSize = 4096

// dbState is the persistent header of a database directory.
type dbState struct {
	Version uint64            `json:"version"`
	Mod     map[uint32]uint64 `json:"mod"` // page id -> version that last modified it
}

// DB is a page-versioned database stored as a directory:
//
//	<dir>/state.json      version + per-page modification stamps
//	<dir>/pages/%08x.pg   one file per page
type DB struct {
	dir   string
	state dbState
}

func statePath(dir string) string { return filepath.Join(dir, "state.json") }
func pagesDir(dir string) string  { return filepath.Join(dir, "pages") }
func pagePath(dir string, id uint32) string {
	return filepath.Join(pagesDir(dir), fmt.Sprintf("%08x.pg", id))
}

// CreateDB creates an empty database at version 0 in dir.
func CreateDB(dir string) (*DB, error) {
	if err := os.MkdirAll(pagesDir(dir), 0755); err != nil {
		return nil, err
	}
	db := &DB{dir: dir, state: dbState{Version: 0, Mod: map[uint32]uint64{}}}
	if err := db.saveState(); err != nil {
		return nil, err
	}
	return db, nil
}

// OpenDB opens an existing database directory.
func OpenDB(dir string) (*DB, error) {
	data, err := ioutil.ReadFile(statePath(dir))
	if err != nil {
		return nil, err
	}
	var st dbState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("decode state: %v", err)
	}
	if st.Mod == nil {
		st.Mod = map[uint32]uint64{}
	}
	return &DB{dir: dir, state: st}, nil
}

// Version returns the current version of the database.
func (db *DB) Version() uint64 { return db.state.Version }

// ApplyChanges writes the given pages and bumps the version by one.
func (db *DB) ApplyChanges(pages map[uint32][]byte) error {
	for id, data := range pages {
		if len(data) != PageSize {
			return fmt.Errorf("page %d: got %d bytes, want %d", id, len(data), PageSize)
		}
	}
	next := db.state.Version + 1
	for id, data := range pages {
		if err := writeFileAtomic(pagePath(db.dir, id), data); err != nil {
			return err
		}
		db.state.Mod[id] = next
	}
	db.state.Version = next
	return db.saveState()
}

func (db *DB) saveState() error {
	data, err := json.Marshal(db.state)
	if err != nil {
		return err
	}
	return writeFileAtomic(statePath(db.dir), data)
}

// PageIDs returns the sorted ids of all pages present on disk.
func (db *DB) PageIDs() ([]uint32, error) { return listPageIDs(pagesDir(db.dir)) }

// ReadPage reads one page from disk.
func (db *DB) ReadPage(id uint32) ([]byte, error) {
	return ioutil.ReadFile(pagePath(db.dir, id))
}

// ChangedSince returns the sorted ids of pages modified after base.
func (db *DB) ChangedSince(base uint64) ([]uint32, error) {
	ids, err := db.PageIDs()
	if err != nil {
		return nil, err
	}
	var out []uint32
	for _, id := range ids {
		if db.state.Mod[id] > base {
			out = append(out, id)
		}
	}
	return out, nil
}

// Digest returns the content digest of the whole database at the
// current version: sha256 over sorted "pageid:pagehash" lines.
func (db *DB) Digest() (string, error) {
	ids, err := db.PageIDs()
	if err != nil {
		return "", err
	}
	return digestPages(pagesDir(db.dir), ids)
}

func listPageIDs(dir string) ([]uint32, error) {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ids []uint32
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".pg") {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSuffix(name, ".pg"), 16, 32)
		if err != nil {
			return nil, fmt.Errorf("bad page file %q: %v", name, err)
		}
		ids = append(ids, uint32(n))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func digestPages(dir string, ids []uint32) (string, error) {
	h := sha256.New()
	for _, id := range ids {
		data, err := ioutil.ReadFile(filepath.Join(dir, fmt.Sprintf("%08x.pg", id)))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(h, "%08x:%s\n", id, hex.EncodeToString(sum[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
