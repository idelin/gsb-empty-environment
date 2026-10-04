package pagezip

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// DictRef identifies one exact dictionary version: an ID plus the digest of
// its content. Two dictionaries may share an ID only if their content (and
// therefore digest) differs; the digest is what pages bind to.
type DictRef struct {
	ID     string
	Digest Digest
}

// Store is a directory-backed page compression store. Pages are compressed
// against shared, versioned dictionaries; every page header pins the exact
// dictionary version it was compressed with, and an append-only ledger
// records page->version references for crash recovery and reclamation.
type Store struct {
	dir      string
	mu       sync.Mutex
	ledger   *os.File
	pageRefs map[string]DictRef
	refCount map[DictRef]int
}

const (
	pageSuffix = ".page"
	dictSuffix = ".dict"
	ledgerName = "ledger.log"
	tmpSuffix  = ".tmp"
)

// Open opens (or creates) the store in dir and recovers reference state by
// replaying the ledger and reconciling it with committed page files.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	removeStaleTmp(dir)
	headers, present, err := ScanPageHeaders(dir)
	if err != nil {
		return nil, err
	}
	ledgerPath := filepath.Join(dir, ledgerName)
	lf, err := os.OpenFile(ledgerPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	pageRefs, counts, err := ReplayLedger(lf, headers, present)
	if err != nil {
		lf.Close()
		return nil, err
	}
	if _, err := lf.Seek(0, io.SeekEnd); err != nil {
		lf.Close()
		return nil, err
	}
	return &Store{
		dir:      dir,
		ledger:   lf,
		pageRefs: pageRefs,
		refCount: counts,
	}, nil
}

// Close flushes and closes the ledger.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ledger == nil {
		return nil
	}
	err := s.ledger.Sync()
	if cerr := s.ledger.Close(); err == nil {
		err = cerr
	}
	s.ledger = nil
	return err
}

// PutDict stores a dictionary version and returns its content-addressed
// reference. Storing identical content under the same ID is idempotent.
func (s *Store) PutDict(id string, content []byte) (DictRef, error) {
	if err := validID(id); err != nil {
		return DictRef{}, err
	}
	ref := DictRef{ID: id, Digest: DigestOf(content)}
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.dictPath(ref)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeFileAtomic(path, content); err != nil {
			return DictRef{}, err
		}
	}
	if err := s.appendLedger(ledgerEntry{Op: opDictAdd, DictID: id, Digest: ref.Digest.String()}); err != nil {
		return DictRef{}, err
	}
	return ref, nil
}

// GetDict loads and hash-verifies one exact dictionary version.
func (s *Store) GetDict(ref DictRef) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadDict(ref)
}

// WritePage compresses data against the exact dictionary version ref and
// atomically replaces the page. The page file is the commit record; the
// ledger setref entry is appended after it, so a crash mid-switch is
// recovered from the committed page header rather than guessed.
func (s *Store) WritePage(pageID string, ref DictRef, data []byte) error {
	if err := validID(pageID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dict, err := s.loadDict(ref)
	if err != nil {
		return err
	}
	payload, err := compressWithDict(dict, data)
	if err != nil {
		return err
	}
	blob := encodePage(pageHeader{dictID: ref.ID, digest: ref.Digest, payload: payload})
	if err := s.commitPageFile(pageID, blob); err != nil {
		return err
	}
	if err := s.appendLedger(ledgerEntry{Op: opSetRef, Page: pageID, DictID: ref.ID, Digest: ref.Digest.String()}); err != nil {
		return err
	}
	s.applyRef(pageID, ref)
	return nil
}

// ReadPage decodes a page using exactly the dictionary version its header
// binds to. A missing version yields *DictMissingError, a hash-mismatched
// dictionary *DictCorruptError, and a damaged page file *PageCorruptError.
func (s *Store) ReadPage(pageID string) ([]byte, error) {
	if err := validID(pageID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	blob, err := ioutil.ReadFile(s.pagePath(pageID))
	if os.IsNotExist(err) {
		return nil, ErrPageNotFound
	}
	if err != nil {
		return nil, err
	}
	h, err := decodePage(blob)
	if err != nil {
		return nil, &PageCorruptError{PageID: pageID, Reason: err.Error()}
	}
	dict, err := s.loadDict(DictRef{ID: h.dictID, Digest: h.digest})
	if err != nil {
		return nil, err
	}
	data, err := decompressWithDict(dict, h.payload)
	if err != nil {
		return nil, &PageCorruptError{PageID: pageID, Reason: "decompression failed: " + err.Error()}
	}
	return data, nil
}

// DeletePage removes a page and releases its dictionary reference.
func (s *Store) DeletePage(pageID string) error {
	if err := validID(pageID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.pagePath(pageID)); os.IsNotExist(err) {
		return ErrPageNotFound
	}
	if err := os.Remove(s.pagePath(pageID)); err != nil {
		return err
	}
	if err := s.appendLedger(ledgerEntry{Op: opDelPage, Page: pageID}); err != nil {
		return err
	}
	if old, ok := s.pageRefs[pageID]; ok {
		s.decRef(old)
		delete(s.pageRefs, pageID)
	}
	return nil
}

// DeleteDict removes one dictionary version. It refuses with ErrDictInUse
// while any page still references that version.
func (s *Store) DeleteDict(ref DictRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refCount[ref] > 0 {
		return ErrDictInUse
	}
	if _, err := os.Stat(s.dictPath(ref)); os.IsNotExist(err) {
		return &DictMissingError{ID: ref.ID, Digest: ref.Digest}
	}
	if err := os.Remove(s.dictPath(ref)); err != nil {
		return err
	}
	return s.appendLedger(ledgerEntry{Op: opDictDel, DictID: ref.ID, Digest: ref.Digest.String()})
}

// Reclaim deletes every stored dictionary version with zero page
// references and returns the reclaimed versions. Referenced versions are
// never touched, so old dictionaries survive until their pages are
// rewritten.
func (s *Store) Reclaim() ([]DictRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fis, err := ioutil.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var reclaimed []DictRef
	for _, fi := range fis {
		ref, ok := parseDictFileName(fi.Name())
		if !ok {
			continue
		}
		if s.refCount[ref] > 0 {
			continue
		}
		if err := os.Remove(s.dictPath(ref)); err != nil {
			return reclaimed, err
		}
		if err := s.appendLedger(ledgerEntry{Op: opDictDel, DictID: ref.ID, Digest: ref.Digest.String()}); err != nil {
			return reclaimed, err
		}
		reclaimed = append(reclaimed, ref)
	}
	return reclaimed, nil
}

// PageRef returns the dictionary version a page currently references.
func (s *Store) PageRef(pageID string) (DictRef, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.pageRefs[pageID]
	return ref, ok
}

// RefCount returns how many pages reference a dictionary version.
func (s *Store) RefCount(ref DictRef) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refCount[ref]
}

// ScanPageHeaders reads every committed page file in dir. It returns the
// decodable page headers and the set of all page IDs present on disk,
// including undecodable ones.
func ScanPageHeaders(dir string) (headers map[string]DictRef, present map[string]bool, err error) {
	headers = make(map[string]DictRef)
	present = make(map[string]bool)
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, fi := range fis {
		name := fi.Name()
		if !strings.HasSuffix(name, pageSuffix) {
			continue
		}
		pageID := name[:len(name)-len(pageSuffix)]
		present[pageID] = true
		blob, rerr := ioutil.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			continue
		}
		h, derr := decodePage(blob)
		if derr != nil {
			continue // corrupt page: leave its ledger reference authoritative
		}
		headers[pageID] = DictRef{ID: h.dictID, Digest: h.digest}
	}
	return headers, present, nil
}

func (s *Store) loadDict(ref DictRef) ([]byte, error) {
	content, err := ioutil.ReadFile(s.dictPath(ref))
	if os.IsNotExist(err) {
		return nil, &DictMissingError{ID: ref.ID, Digest: ref.Digest}
	}
	if err != nil {
		return nil, err
	}
	if DigestOf(content) != ref.Digest {
		return nil, &DictCorruptError{ID: ref.ID, Digest: ref.Digest}
	}
	return content, nil
}

func (s *Store) applyRef(pageID string, ref DictRef) {
	if old, ok := s.pageRefs[pageID]; ok {
		s.decRef(old)
	}
	s.pageRefs[pageID] = ref
	s.refCount[ref]++
}

func (s *Store) decRef(ref DictRef) {
	s.refCount[ref]--
	if s.refCount[ref] <= 0 {
		delete(s.refCount, ref)
	}
}

func (s *Store) appendLedger(e ledgerEntry) error {
	if err := appendEntry(s.ledger, e); err != nil {
		return err
	}
	return s.ledger.Sync()
}

func (s *Store) commitPageFile(pageID string, blob []byte) error {
	return writeFileAtomic(s.pagePath(pageID), blob)
}

func (s *Store) pagePath(pageID string) string {
	return filepath.Join(s.dir, pageID+pageSuffix)
}

func (s *Store) dictPath(ref DictRef) string {
	return filepath.Join(s.dir, dictFileName(ref))
}

func dictFileName(ref DictRef) string {
	return ref.ID + "." + ref.Digest.String() + dictSuffix
}

func parseDictFileName(name string) (DictRef, bool) {
	if !strings.HasSuffix(name, dictSuffix) {
		return DictRef{}, false
	}
	stem := name[:len(name)-len(dictSuffix)]
	i := strings.LastIndex(stem, ".")
	if i < 0 {
		return DictRef{}, false
	}
	d, err := ParseDigest(stem[i+1:])
	if err != nil {
		return DictRef{}, false
	}
	return DictRef{ID: stem[:i], Digest: d}, true
}

func validID(id string) error {
	if id == "" {
		return errors.New("pagezip: empty id")
	}
	for _, r := range id {
		ok := r == '-' || r == '_' ||
			(r >= '0' && r <= '9') ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z')
		if !ok {
			return fmt.Errorf("pagezip: invalid id %q", id)
		}
	}
	return nil
}

func writeFileAtomic(path string, content []byte) error {
	tmp := path + tmpSuffix
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
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
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}

func removeStaleTmp(dir string) {
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return
	}
	for _, fi := range fis {
		if strings.HasSuffix(fi.Name(), tmpSuffix) {
			os.Remove(filepath.Join(dir, fi.Name()))
		}
	}
}
