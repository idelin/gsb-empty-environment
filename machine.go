package rpki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// FailurePolicy states what a host should serve after a failed update round.
type FailurePolicy string

const (
	// FailurePolicyRetainLastGood keeps persisted watermarks and tells the host
	// to continue using its previous last-good view.
	FailurePolicyRetainLastGood FailurePolicy = "fail-closed/retain-last-good"

	// FailurePolicyRestoreSnapshot additionally loads the persisted last-good
	// snapshot so the host can atomically switch back to it.
	FailurePolicyRestoreSnapshot FailurePolicy = "fail-closed/restore-last-good-snapshot"
)

// SnapshotStore persists complete accepted repository file sets.
type SnapshotStore interface {
	CommitSnapshot(ctx context.Context, talKey string, files map[string][]byte) (string, error)
	LastGoodSnapshot(ctx context.Context, talKey string) (string, bool, error)
	LoadSnapshot(ctx context.Context, snapshotID string) (Repository, error)
}

// FileExporter allows a state machine to snapshot a repository.
type FileExporter interface {
	ExportFiles() (map[string][]byte, error)
}

// RoundMachine runs validation plus explicit fail-safe state/snapshot policy.
type RoundMachine struct {
	States    StateStore
	Snapshots SnapshotStore
	Policy    FailurePolicy
	Options   Options
}

// RoundResult is the validation outcome and the safe view after failure.
type RoundResult struct {
	Accepted           bool
	Report             *CAReport
	Policy             FailurePolicy
	StateRetained      bool
	SnapshotID         string
	LastGoodID         string
	LastGoodRepository Repository
	RestoreErr         error
}

// RunRound validates candidate and commits watermarks/snapshot only through
// the normal per-CA success path. A rejected candidate never overwrites the
// attacked CA watermark.
func (m *RoundMachine) RunRound(ctx context.Context, tal TAL, candidate Repository) (*RoundResult, error) {
	if m == nil {
		return nil, errors.New("rpki: nil RoundMachine")
	}
	policy := m.Policy
	if policy == "" {
		policy = FailurePolicyRetainLastGood
	}
	opts := m.Options
	opts.States = m.States
	validator := NewValidator(candidate, opts)
	report, err := validator.ValidateTAL(ctx, tal)
	result := &RoundResult{Report: report, Policy: policy, StateRetained: true}
	if err != nil {
		if policy == FailurePolicyRestoreSnapshot && m.Snapshots != nil {
			id, ok, loadErr := m.Snapshots.LastGoodSnapshot(ctx, tal.Name)
			if loadErr != nil {
				result.RestoreErr = loadErr
			} else if ok {
				repo, loadErr := m.Snapshots.LoadSnapshot(ctx, id)
				if loadErr != nil {
					result.RestoreErr = loadErr
				} else {
					result.LastGoodID = id
					result.LastGoodRepository = repo
				}
			}
		}
		return result, err
	}

	result.Accepted = true
	if m.Snapshots != nil {
		files, exportErr := exportRepository(candidate)
		if exportErr != nil {
			return result, fmt.Errorf("rpki: validation succeeded but snapshot export failed: %w", exportErr)
		}
		id, commitErr := m.Snapshots.CommitSnapshot(ctx, tal.Name, files)
		if commitErr != nil {
			return result, fmt.Errorf("rpki: validation succeeded but snapshot commit failed: %w", commitErr)
		}
		result.SnapshotID = id
	}
	return result, nil
}

func exportRepository(repo Repository) (map[string][]byte, error) {
	exporter, ok := repo.(FileExporter)
	if !ok {
		return nil, fmt.Errorf("rpki: repository %T does not support full export", repo)
	}
	return exporter.ExportFiles()
}

// ExportFiles returns a deep copy of all map files.
func (r *MapRepository) ExportFiles() (map[string][]byte, error) {
	out := make(map[string][]byte, len(r.Files))
	for name, content := range r.Files {
		normalized, err := NormalizeRepositoryPath(name)
		if err != nil {
			return nil, err
		}
		out[normalized] = append([]byte(nil), content...)
	}
	return out, nil
}

// ExportFiles reads every regular file from the offline OS repository.
func (r *OSRepository) ExportFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(r.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, err := fs.ReadFile(r.fsys, p)
		if err != nil {
			return err
		}
		normalized, err := NormalizeRepositoryPath(p)
		if err != nil {
			return err
		}
		out[normalized] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FileSnapshotStore is a content-addressed snapshot store with a last-good pointer.
type FileSnapshotStore struct {
	root string
	mu   sync.Mutex
}

func NewFileSnapshotStore(root string) *FileSnapshotStore {
	return &FileSnapshotStore{root: root}
}

func (s *FileSnapshotStore) CommitSnapshot(_ context.Context, talKey string, files map[string][]byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(files))
	for name := range files {
		normalized, err := NormalizeRepositoryPath(name)
		if err != nil {
			return "", err
		}
		names = append(names, normalized)
	}
	sort.Strings(names)
	hasher := sha256.New()
	for _, name := range names {
		_, _ = hasher.Write([]byte(name))
		_, _ = hasher.Write([]byte{0})
		sum := sha256.Sum256(files[name])
		_, _ = hasher.Write(sum[:])
	}
	id := hex.EncodeToString(hasher.Sum(nil))
	snapshotDir := filepath.Join(s.root, "snapshots", id)
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return "", err
	}
	for _, name := range names {
		target, err := safeSnapshotPath(snapshotDir, name)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, append([]byte(nil), files[name]...), 0o644); err != nil {
			return "", err
		}
	}
	if err := s.writeLastGoodLocked(talKey, id); err != nil {
		return "", err
	}
	return id, nil
}

func (s *FileSnapshotStore) LastGoodSnapshot(_ context.Context, talKey string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := s.lastGoodPath(talKey)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	id := stringTrimSpace(string(data))
	if !isHexID(id) {
		return "", false, fmt.Errorf("rpki: invalid last-good snapshot id %q", id)
	}
	return id, true, nil
}

func (s *FileSnapshotStore) LoadSnapshot(_ context.Context, id string) (Repository, error) {
	if !isHexID(id) {
		return nil, fmt.Errorf("rpki: invalid snapshot id %q", id)
	}
	root := filepath.Join(s.root, "snapshots", id)
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	repo, err := NewOSRepository(root)
	if err != nil {
		return nil, err
	}
	files, err := repo.ExportFiles()
	if err != nil {
		return nil, err
	}
	return NewMapRepository(files), nil
}

func (s *FileSnapshotStore) writeLastGoodLocked(talKey, id string) error {
	if err := os.MkdirAll(filepath.Join(s.root, "last-good"), 0o755); err != nil {
		return err
	}
	path := s.lastGoodPath(talKey)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".last-good-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(id + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (s *FileSnapshotStore) lastGoodPath(talKey string) string {
	keySum := sha256.Sum256([]byte(talKey))
	return filepath.Join(s.root, "last-good", hex.EncodeToString(keySum[:])+".id")
}

func safeSnapshotPath(root, name string) (string, error) {
	normalized, err := NormalizeRepositoryPath(name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(normalized))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || startsWithParent(relative) {
		return "", fmt.Errorf("%w: snapshot path %s", ErrRepositoryPath, name)
	}
	return target, nil
}

func startsWithParent(relative string) bool {
	return relative == ".." || len(relative) >= 3 && relative[:3] == ".."+string(filepath.Separator)
}

func isHexID(id string) bool {
	if len(id) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func stringTrimSpace(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\n' || value[0] == '\r' || value[0] == '\t') {
		value = value[1:]
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != ' ' && last != '\n' && last != '\r' && last != '\t' {
			break
		}
		value = value[:len(value)-1]
	}
	return value
}
