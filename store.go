package pagestore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Store is a page compression store. Pages share dictionaries and may
// switch between dictionary versions; every page header pins the exact
// dictionary version (ID + content digest) it was compressed with, and an
// append-only reference ledger records which version each live page uses.
// A dictionary version is reclaimable only when no committed page binding
// references it.
//
// Crash order for a page write (including a dictionary switch, which is
// just a rewrite with a different DictKey):
//  1. page file written atomically (temp + rename) and fsynced
//  2. ledger BIND record appended and fsynced
//
// A crash between the two leaves an orphan page file with no ledger
// binding; recovery reports it as incomplete and ignores it. A torn ledger
// tail is truncated at the last valid record.
type Store struct {
	dir    string
	mu     sync.Mutex
	ledger *os.File

	bindings     map[uint64]DictKey   // committed page -> dict version
	dicts        map[DictKey][]byte   // healthy dictionary content
	corruptDicts map[DictKey]struct{} // present on disk but digest mismatch
	refs         map[DictKey]int      // derived from bindings
}

// RecoveryReport describes what Open found while rebuilding state.
type RecoveryReport struct {
	// IncompletePages are page files with no committed ledger binding
	// (interrupted writes). They are ignored by the store.
	IncompletePages []uint64
	// CorruptDicts are dictionary files whose content no longer matches
	// the digest they were registered under.
	CorruptDicts []DictKey
	// LedgerTruncatedBytes is how many torn tail bytes were dropped from
	// the reference ledger.
	LedgerTruncatedBytes int
}

// Open loads (or creates) the store in dir and recovers state by replaying
// the reference ledger from scratch.
func Open(dir string) (*Store, *RecoveryReport, error) {
	report := &RecoveryReport{}
	pagesDir := filepath.Join(dir, "pages")
	dictsDir := filepath.Join(dir, "dicts")
	for _, d := range []string{pagesDir, dictsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, nil, err
		}
	}

	dicts, corrupt, err := loadDicts(dictsDir)
	if err != nil {
		return nil, nil, err
	}
	for key := range corrupt {
		report.CorruptDicts = append(report.CorruptDicts, key)
	}
	sort.Slice(report.CorruptDicts, func(i, j int) bool {
		a, b := report.CorruptDicts[i], report.CorruptDicts[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return string(a.Digest[:]) < string(b.Digest[:])
	})

	ledgerPath := filepath.Join(dir, "refs.log")
	state, validBytes, err := ReplayLedger(ledgerPath)
	if err != nil {
		return nil, nil, err
	}
	if info, statErr := os.Stat(ledgerPath); statErr == nil && info.Size() > int64(validBytes) {
		report.LedgerTruncatedBytes = int(info.Size()) - validBytes
		if err := os.Truncate(ledgerPath, int64(validBytes)); err != nil {
			return nil, nil, err
		}
	}

	incomplete, err := scanOrphanPages(pagesDir, state.Bindings)
	if err != nil {
		return nil, nil, err
	}
	report.IncompletePages = incomplete

	ledgerFile, err := os.OpenFile(ledgerPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}

	s := &Store{
		dir:          dir,
		ledger:       ledgerFile,
		bindings:     state.Bindings,
		dicts:        dicts,
		corruptDicts: corrupt,
		refs:         state.Refs,
	}
	return s, report, nil
}

// Close flushes and closes the ledger.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ledger.Close()
}

// RegisterDictionary stores content as a new exact version of dictionary
// id and returns its version key. Registering the same id with different
// content creates a distinct version; registering identical content is a
// no-op.
func (s *Store) RegisterDictionary(id uint64, content []byte) (DictKey, error) {
	key := MakeDictKey(id, content)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.dicts[key]; ok {
		return key, nil
	}
	if _, bad := s.corruptDicts[key]; bad {
		return DictKey{}, fmt.Errorf("%w: %s", ErrDictionaryCorrupt, key)
	}
	if err := writeFileAtomic(filepath.Join(s.dictsDir(), key.fileName()), content); err != nil {
		return DictKey{}, err
	}
	stored := make([]byte, len(content))
	copy(stored, content)
	s.dicts[key] = stored
	return key, nil
}

// WritePage compresses raw with the exact dictionary version key and
// commits the page. Writing an existing pageID rebinds it (a dictionary
// switch); the old version's reference is released only after the new
// binding is committed to the ledger.
func (s *Store) WritePage(pageID uint64, key DictKey, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dict, err := s.healthyDict(key)
	if err != nil {
		return err
	}
	payload, err := compressWithDict(raw, dict)
	if err != nil {
		return err
	}
	if err := writeFileAtomic(s.pagePath(pageID), encodePage(pageID, key, payload)); err != nil {
		return err
	}
	if err := s.appendLedger(encodeBindRecord(pageID, key)); err != nil {
		return err
	}
	old, had := s.bindings[pageID]
	if had && old != key {
		s.refs[old]--
	}
	if !had || old != key {
		s.refs[key]++
	}
	s.bindings[pageID] = key
	return nil
}

// ReadPage decodes a page using exactly the dictionary version pinned in
// its header. It never substitutes another version of the same dictionary
// ID.
func (s *Store) ReadPage(pageID uint64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bound, ok := s.bindings[pageID]
	if !ok {
		return nil, fmt.Errorf("%w: %d", ErrPageNotFound, pageID)
	}
	data, err := os.ReadFile(s.pagePath(pageID))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %d (file lost)", ErrPageNotFound, pageID)
		}
		return nil, err
	}
	_, key, payload, err := decodePage(data)
	if err != nil {
		return nil, err
	}
	if key != bound {
		return nil, fmt.Errorf("%w: page %d header %s disagrees with ledger %s",
			ErrPageCorrupt, pageID, key, bound)
	}
	dict, err := s.healthyDict(key)
	if err != nil {
		return nil, err
	}
	raw, err := decompressWithDict(payload, dict)
	if err != nil {
		return nil, fmt.Errorf("%w: page %d payload: %v", ErrPageCorrupt, pageID, err)
	}
	return raw, nil
}

// DeletePage releases the page's dictionary reference and removes the page.
func (s *Store) DeletePage(pageID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.bindings[pageID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrPageNotFound, pageID)
	}
	if err := s.appendLedger(encodeReleaseRecord(pageID)); err != nil {
		return err
	}
	if err := os.Remove(s.pagePath(pageID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	s.refs[key]--
	delete(s.bindings, pageID)
	return nil
}

// Collect reclaims every dictionary version with zero committed page
// references and returns the reclaimed keys. A version still pinned by any
// old (not yet rewritten) page is never collected.
func (s *Store) Collect() ([]DictKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var collected []DictKey
	for key := range s.dicts {
		if s.refs[key] > 0 {
			continue
		}
		if err := os.Remove(filepath.Join(s.dictsDir(), key.fileName())); err != nil && !os.IsNotExist(err) {
			return collected, err
		}
		delete(s.dicts, key)
		collected = append(collected, key)
	}
	for key := range s.corruptDicts {
		if s.refs[key] > 0 {
			continue
		}
		if err := os.Remove(filepath.Join(s.dictsDir(), key.fileName())); err != nil && !os.IsNotExist(err) {
			return collected, err
		}
		delete(s.corruptDicts, key)
	}
	sort.Slice(collected, func(i, j int) bool {
		a, b := collected[i], collected[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return string(a.Digest[:]) < string(b.Digest[:])
	})
	return collected, nil
}

// RefCount reports how many committed pages currently pin the given exact
// dictionary version.
func (s *Store) RefCount(key DictKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refs[key]
}

// Binding returns the exact dictionary version a page is committed to.
func (s *Store) Binding(pageID uint64) (DictKey, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.bindings[pageID]
	return key, ok
}

// healthyDict resolves a key to verified content, distinguishing a missing
// version from a corrupt one.
func (s *Store) healthyDict(key DictKey) ([]byte, error) {
	if _, bad := s.corruptDicts[key]; bad {
		return nil, fmt.Errorf("%w: %s", ErrDictionaryCorrupt, key)
	}
	content, ok := s.dicts[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrDictionaryMissing, key)
	}
	if sha256.Sum256(content) != key.Digest {
		return nil, fmt.Errorf("%w: %s", ErrDictionaryCorrupt, key)
	}
	return content, nil
}

func (s *Store) appendLedger(rec []byte) error {
	if _, err := s.ledger.Write(rec); err != nil {
		return err
	}
	return s.ledger.Sync()
}

func (s *Store) pagesDir() string { return filepath.Join(s.dir, "pages") }
func (s *Store) dictsDir() string { return filepath.Join(s.dir, "dicts") }

func (s *Store) pagePath(pageID uint64) string {
	return filepath.Join(s.pagesDir(), fmt.Sprintf("page_%d.pg", pageID))
}

func loadDicts(dir string) (map[DictKey][]byte, map[DictKey]struct{}, error) {
	dicts := make(map[DictKey][]byte)
	corrupt := make(map[DictKey]struct{})
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		key, ok := parseDictFileName(e.Name())
		if !ok {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, nil, err
		}
		if sha256.Sum256(content) != key.Digest {
			corrupt[key] = struct{}{}
			continue
		}
		dicts[key] = content
	}
	return dicts, corrupt, nil
}

func scanOrphanPages(dir string, bindings map[uint64]DictKey) ([]uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var orphans []uint64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".pg") {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(e.Name(), "page_"), ".pg"), 10, 64)
		if err != nil {
			continue
		}
		if _, ok := bindings[id]; !ok {
			orphans = append(orphans, id)
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i] < orphans[j] })
	return orphans, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
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
