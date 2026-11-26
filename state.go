package rpki

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var ErrRollback = fmt.Errorf("%w: manifest rollback or replay", ErrManifestSequence)

// Watermark is the last fully accepted manifest state for one CA.
type Watermark struct {
	CAKey                string    `json:"caKey"`
	CAName               string    `json:"caName,omitempty"`
	RepositoryDir        string    `json:"repositoryDir,omitempty"`
	ManifestNumber       *big.Int  `json:"-"`
	ManifestNumberString string    `json:"manifestNumber"`
	ThisUpdate           time.Time `json:"thisUpdate"`
	NextUpdate           time.Time `json:"nextUpdate,omitempty"`
	ManifestSHA256       string    `json:"manifestSha256,omitempty"`
	LastSuccess          time.Time `json:"lastSuccess"`
}

func (w Watermark) normalized() Watermark {
	if w.ManifestNumber != nil {
		w.ManifestNumberString = w.ManifestNumber.String()
	}
	if w.LastSuccess.IsZero() {
		w.LastSuccess = time.Now().UTC()
	}
	w.ThisUpdate = w.ThisUpdate.UTC()
	w.NextUpdate = w.NextUpdate.UTC()
	w.LastSuccess = w.LastSuccess.UTC()
	return w
}

func (w Watermark) number() (*big.Int, error) {
	if w.ManifestNumber != nil {
		return new(big.Int).Set(w.ManifestNumber), nil
	}
	if w.ManifestNumberString == "" {
		return nil, nil
	}
	n, ok := new(big.Int).SetString(w.ManifestNumberString, 10)
	if !ok {
		return nil, fmt.Errorf("rpki: invalid persisted manifestNumber %q", w.ManifestNumberString)
	}
	w.ManifestNumber = n
	return n, nil
}

// StateStore persists per-CA anti-rollback watermarks.
type StateStore interface {
	LoadWatermark(ctx context.Context, caKey string) (Watermark, bool, error)
	SaveWatermark(ctx context.Context, wm Watermark) error
}

// MemoryStateStore is useful for tests and one-shot processes.
type MemoryStateStore struct {
	mu     sync.RWMutex
	states map[string]Watermark
}

func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{states: map[string]Watermark{}}
}

func (s *MemoryStateStore) LoadWatermark(_ context.Context, caKey string) (Watermark, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wm, ok := s.states[caKey]
	if !ok {
		return Watermark{}, false, nil
	}
	n, err := wm.number()
	if err != nil {
		return Watermark{}, false, err
	}
	wm.ManifestNumber = n
	return wm, true, nil
}

func (s *MemoryStateStore) SaveWatermark(_ context.Context, wm Watermark) error {
	if wm.CAKey == "" {
		return errors.New("rpki: watermark missing CAKey")
	}
	wm = wm.normalized()
	if wm.ManifestNumber == nil {
		return errors.New("rpki: watermark missing manifest number")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[wm.CAKey] = wm
	return nil
}

// FileStateStore stores watermarks as atomically replaced JSON.
type FileStateStore struct {
	path string
	mu   sync.Mutex
}

type fileStateDocument struct {
	Version int                  `json:"version"`
	CAs     map[string]Watermark `json:"cas"`
}

func NewFileStateStore(path string) *FileStateStore {
	return &FileStateStore{path: path}
}

func (s *FileStateStore) LoadWatermark(_ context.Context, caKey string) (Watermark, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readDocumentLocked()
	if err != nil {
		return Watermark{}, false, err
	}
	wm, ok := doc.CAs[caKey]
	if !ok {
		return Watermark{}, false, nil
	}
	n, err := wm.number()
	if err != nil {
		return Watermark{}, false, err
	}
	wm.ManifestNumber = n
	return wm, true, nil
}

func (s *FileStateStore) SaveWatermark(_ context.Context, wm Watermark) error {
	if wm.CAKey == "" {
		return errors.New("rpki: watermark missing CAKey")
	}
	if wm.ManifestNumber == nil && wm.ManifestNumberString != "" {
		n, ok := new(big.Int).SetString(wm.ManifestNumberString, 10)
		if !ok {
			return fmt.Errorf("rpki: invalid manifestNumber %q", wm.ManifestNumberString)
		}
		wm.ManifestNumber = n
	}
	if wm.ManifestNumber == nil {
		return errors.New("rpki: watermark missing manifest number")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.readDocumentLocked()
	if err != nil {
		return err
	}
	doc.CAs[wm.CAKey] = wm.normalized()
	return s.writeDocumentLocked(doc)
}

func (s *FileStateStore) readDocumentLocked() (fileStateDocument, error) {
	doc := fileStateDocument{Version: 1, CAs: map[string]Watermark{}}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return doc, nil
	}
	if err != nil {
		return doc, err
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("rpki: parse state file %s: %w", s.path, err)
	}
	if doc.CAs == nil {
		doc.CAs = map[string]Watermark{}
	}
	return doc, nil
}

func (s *FileStateStore) writeDocumentLocked(doc fileStateDocument) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".rpki-state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
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
	return os.Rename(tmpName, s.path)
}

// CheckWatermark enforces strict manifest-number/time monotonicity. An exact
// same number/time/hash manifest is allowed as an idempotent repeated round.
func CheckWatermark(old Watermark, candidate Watermark) error {
	oldNum, err := old.number()
	if err != nil {
		return err
	}
	candidateNum, err := candidate.number()
	if err != nil {
		return err
	}
	if oldNum == nil {
		return nil
	}
	switch candidateNum.Cmp(oldNum) {
	case -1:
		return fmt.Errorf("%w: CA %s candidate manifestNumber %s is older than persisted %s", ErrRollback, old.CAKey, candidateNum, oldNum)
	case 0:
		if candidate.ThisUpdate.Before(old.ThisUpdate) {
			return fmt.Errorf("%w: CA %s equal number %s has older thisUpdate %s < %s", ErrRollback, old.CAKey, candidateNum, candidate.ThisUpdate.Format(time.RFC3339), old.ThisUpdate.Format(time.RFC3339))
		}
		if candidate.ThisUpdate.Equal(old.ThisUpdate) {
			if old.ManifestSHA256 != "" && candidate.ManifestSHA256 != "" && !equalFold(candidate.ManifestSHA256, old.ManifestSHA256) {
				return fmt.Errorf("%w: CA %s reused manifestNumber %s/thisUpdate with different content", ErrRollback, old.CAKey, candidateNum)
			}
			return nil
		}
		return fmt.Errorf("%w: CA %s reused manifestNumber %s with newer thisUpdate", ErrManifestSequence, old.CAKey, candidateNum)
	case 1:
		if !old.ThisUpdate.IsZero() && candidate.ThisUpdate.Before(old.ThisUpdate) {
			return fmt.Errorf("%w: CA %s manifestNumber advanced but thisUpdate regressed %s < %s", ErrRollback, old.CAKey, candidate.ThisUpdate.Format(time.RFC3339), old.ThisUpdate.Format(time.RFC3339))
		}
		return nil
	default:
		return nil
	}
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x := a[i]
		y := b[i]
		if 'A' <= x && x <= 'F' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'F' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
