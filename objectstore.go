package netsec

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ObjectSnapshot is a consistent view of one object generation.
type ObjectSnapshot struct {
	Ref            ObjectRef `json:"ref"`
	Metadata       []byte    `json:"metadata"`
	MetadataDigest Digest    `json:"metadataDigest"`
	Data           []byte    `json:"data"`
	DataDigest     Digest    `json:"dataDigest"`
}

// StagedData is durable temporary data which has not become visible.
type StagedData struct {
	TxID   string    `json:"txID"`
	Ref    ObjectRef `json:"ref"`
	Path   string    `json:"path"`
	Digest Digest    `json:"digest"`
}

// StagedMetadata is durable metadata staged after a successful old-digest
// comparison but before Commit becomes visible.
type StagedMetadata struct {
	TxID      string    `json:"txID"`
	Ref       ObjectRef `json:"ref"`
	Path      string    `json:"path"`
	OldDigest Digest    `json:"oldDigest"`
	NewDigest Digest    `json:"newDigest"`
}

// ObjectStore is the storage contract used by Host.  Implementations must
// treat Ref.Generation as part of object identity and must never resolve an
// old generation to a newer object number reuse.
type ObjectStore interface {
	CurrentGeneration(ctx context.Context, id ObjectID) (uint64, error)
	Read(ctx context.Context, ref ObjectRef) (ObjectSnapshot, error)

	StageData(ctx context.Context, txID string, ref ObjectRef, data []byte, digest Digest) (StagedData, error)
	StageMetadata(ctx context.Context, txID string, ref ObjectRef, oldDigest, newDigest Digest, metadata []byte) (StagedMetadata, error)

	// PublishData makes durable staged data visible if the object still has
	// the expected generation and the visible data is absent or already has
	// the expected digest.
	PublishData(ctx context.Context, txID string, ref ObjectRef, digest Digest) error

	// PublishMetadata performs the metadata CAS.  If no staged file exists,
	// the implementation may write metadata to staging first, but it must
	// only rename after verifying the current old or new digest.
	PublishMetadata(ctx context.Context, txID string, ref ObjectRef, oldDigest, newDigest Digest, metadata []byte) error
	Rollback(ctx context.Context, txID string) error
}

// ObjectCreator is implemented by stores that allow an administrative seed
// object.  Ordinary writes must still go through Host.Execute.
type ObjectCreator interface {
	CreateObject(ctx context.Context, ref ObjectRef, metadata, data []byte) error
}

// AppliedMarker records that a committed transaction was actually published
// to a specific object generation.  It lets recovery distinguish "committed
// and applied, then the object number was reused" from "Commit was durable but
// publication never happened, and the object number was reused".  The former
// must not be migrated to the new generation, and must not block the host.
type AppliedMarker interface {
	WasApplied(ctx context.Context, ref ObjectRef, txID string, newMetadataDigest Digest) (bool, error)
	MarkApplied(ctx context.Context, ref ObjectRef, txID string, newMetadataDigest Digest) error
}

// StagingCleaner removes staging files that do not belong to any trusted log
// transaction.  It is used after recovery so a crash before the first Prepare
// record cannot leave visible-adjacent temporary state behind.
type StagingCleaner interface {
	CleanupOrphans(ctx context.Context, keep map[TxID]struct{}) error
}

// FileStore stores each object generation in its own directory.  The object
// number's "current" file points at exactly one generation, so object number
// reuse cannot make an old transaction apply to the new object.
type FileStore struct {
	objectsDir string
	stagingDir string
}

// NewFileStore creates (if necessary) an object directory and a staging
// directory.
func NewFileStore(objectsDir, stagingDir string) (*FileStore, error) {
	if objectsDir == "" || stagingDir == "" {
		return nil, fmt.Errorf("netsec: file store directories must not be empty")
	}
	if err := os.MkdirAll(objectsDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return nil, err
	}
	return &FileStore{objectsDir: objectsDir, stagingDir: stagingDir}, nil
}

func objectKey(id ObjectID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

func (s *FileStore) objectDir(id ObjectID) string {
	return filepath.Join(s.objectsDir, objectKey(id))
}

func (s *FileStore) generationDir(ref ObjectRef) string {
	return filepath.Join(s.objectDir(ref.ID), strconv.FormatUint(ref.Generation, 10))
}

func (s *FileStore) currentPath(id ObjectID) string {
	return filepath.Join(s.objectDir(id), "current")
}

func (s *FileStore) metadataPath(ref ObjectRef) string {
	return filepath.Join(s.generationDir(ref), "meta")
}

func (s *FileStore) dataPath(ref ObjectRef) string {
	return filepath.Join(s.generationDir(ref), "data")
}

func (s *FileStore) appliedPath(ref ObjectRef, txID string) string {
	sum := DigestString(txID)
	return filepath.Join(s.generationDir(ref), "applied", sum.String())
}

func (s *FileStore) stagePath(txID, suffix string) string {
	sum := DigestString(txID)
	return filepath.Join(s.stagingDir, sum.String()+"."+suffix)
}

func (s *FileStore) CurrentGeneration(_ context.Context, id ObjectID) (uint64, error) {
	b, err := os.ReadFile(s.currentPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, ErrObjectNotFound
		}
		return 0, err
	}
	gen, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("netsec: invalid current generation: %w", err)
	}
	return gen, nil
}

func (s *FileStore) Read(_ context.Context, ref ObjectRef) (ObjectSnapshot, error) {
	current, err := s.CurrentGeneration(context.Background(), ref.ID)
	if err != nil {
		return ObjectSnapshot{}, err
	}
	if current != ref.Generation {
		return ObjectSnapshot{}, fmt.Errorf("%w: object %q current=%d requested=%d", ErrObjectGenerationChanged, ref.ID, current, ref.Generation)
	}
	meta, err := os.ReadFile(s.metadataPath(ref))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ObjectSnapshot{}, ErrObjectNotFound
		}
		return ObjectSnapshot{}, err
	}
	data, err := os.ReadFile(s.dataPath(ref))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			data = nil
		} else {
			return ObjectSnapshot{}, err
		}
	}
	return ObjectSnapshot{
		Ref:            ref,
		Metadata:       meta,
		MetadataDigest: DigestBytes(meta),
		Data:           data,
		DataDigest:     DigestBytes(data),
	}, nil
}

func (s *FileStore) CreateObject(_ context.Context, ref ObjectRef, metadata, data []byte) error {
	if ref.ID == "" {
		return fmt.Errorf("%w: empty object id", ErrInvalidRequest)
	}
	if err := os.MkdirAll(s.objectDir(ref.ID), 0o700); err != nil {
		return err
	}
	if old, err := s.CurrentGeneration(context.Background(), ref.ID); err == nil {
		if ref.Generation <= old {
			return fmt.Errorf("%w: current=%d requested=%d", ErrGenerationRegression, old, ref.Generation)
		}
	} else if !errors.Is(err, ErrObjectNotFound) {
		return err
	}
	genDir := s.generationDir(ref)
	if err := os.MkdirAll(genDir, 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(s.metadataPath(ref), metadata, 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(s.dataPath(ref), data, 0o600); err != nil {
		return err
	}
	if err := syncDir(genDir); err != nil {
		return err
	}
	if err := writeFileAtomic(s.currentPath(ref.ID), []byte(strconv.FormatUint(ref.Generation, 10)+"\n"), 0o600); err != nil {
		return err
	}
	return syncDir(s.objectDir(ref.ID))
}

func (s *FileStore) StageData(_ context.Context, txID string, ref ObjectRef, data []byte, digest Digest) (StagedData, error) {
	if err := s.checkCurrent(ref); err != nil {
		return StagedData{}, err
	}
	if digest.IsZero() {
		digest = DigestBytes(data)
	}
	path := s.stagePath(txID, "data")
	if existing, err := os.ReadFile(path); err == nil && DigestBytes(existing) == digest {
		return StagedData{TxID: txID, Ref: ref, Path: path, Digest: digest}, nil
	}
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return StagedData{}, err
	}
	return StagedData{TxID: txID, Ref: ref, Path: path, Digest: digest}, nil
}

func (s *FileStore) StageMetadata(_ context.Context, txID string, ref ObjectRef, oldDigest, newDigest Digest, metadata []byte) (StagedMetadata, error) {
	if err := s.checkCurrent(ref); err != nil {
		return StagedMetadata{}, err
	}
	if got := DigestBytes(metadata); got != newDigest {
		return StagedMetadata{}, fmt.Errorf("%w: staged metadata digest mismatch", ErrInvalidRecord)
	}
	current, err := os.ReadFile(s.metadataPath(ref))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			current = nil
		} else {
			return StagedMetadata{}, err
		}
	}
	if DigestBytes(current) != oldDigest {
		return StagedMetadata{}, ErrMetadataConflict
	}
	path := s.stagePath(txID, "meta")
	if err := writeFileAtomic(path, metadata, 0o600); err != nil {
		return StagedMetadata{}, err
	}
	return StagedMetadata{
		TxID:      txID,
		Ref:       ref,
		Path:      path,
		OldDigest: oldDigest,
		NewDigest: newDigest,
	}, nil
}

func (s *FileStore) PublishData(_ context.Context, txID string, ref ObjectRef, digest Digest) error {
	if ref.ID == "" {
		return fmt.Errorf("%w: empty object id", ErrInvalidRequest)
	}
	if err := s.checkCurrent(ref); err != nil {
		return err
	}
	path := s.stagePath(txID, "data")
	final := s.dataPath(ref)
	if existing, err := os.ReadFile(final); err == nil {
		if DigestBytes(existing) == digest {
			return removeIfExists(path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrStagingMissing
		}
		return err
	}
	if err := os.Rename(path, final); err != nil {
		return err
	}
	return syncDir(filepath.Dir(final))
}

func (s *FileStore) PublishMetadata(_ context.Context, txID string, ref ObjectRef, oldDigest, newDigest Digest, metadata []byte) error {
	if ref.ID == "" {
		return fmt.Errorf("%w: empty object id", ErrInvalidRequest)
	}
	if err := s.checkCurrent(ref); err != nil {
		return err
	}
	if got := DigestBytes(metadata); got != newDigest {
		return fmt.Errorf("%w: metadata digest mismatch", ErrInvalidRecord)
	}
	path := s.stagePath(txID, "meta")
	final := s.metadataPath(ref)
	current, err := os.ReadFile(final)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			current = nil
		} else {
			return err
		}
	}
	currentDigest := DigestBytes(current)
	if currentDigest == newDigest {
		if err := s.MarkApplied(context.Background(), ref, txID, newDigest); err != nil {
			return err
		}
		return removeIfExists(path)
	}
	if currentDigest != oldDigest {
		return ErrMetadataConflict
	}
	if staged, err := os.ReadFile(path); err == nil {
		if DigestBytes(staged) != newDigest {
			// The authenticated patch is in the log, so a torn or
			// stale staging file can be regenerated safely.
			if err := writeFileAtomic(path, metadata, 0o600); err != nil {
				return err
			}
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := writeFileAtomic(path, metadata, 0o600); err != nil {
			return err
		}
	} else {
		return err
	}
	if err := os.Rename(path, final); err != nil {
		return err
	}
	if err := s.MarkApplied(context.Background(), ref, txID, newDigest); err != nil {
		return err
	}
	return syncDir(filepath.Dir(final))
}

func (s *FileStore) MarkApplied(_ context.Context, ref ObjectRef, txID string, newMetadataDigest Digest) error {
	return writeFileAtomic(s.appliedPath(ref, txID), []byte(newMetadataDigest.String()+"\n"), 0o600)
}

func (s *FileStore) WasApplied(_ context.Context, ref ObjectRef, txID string, newMetadataDigest Digest) (bool, error) {
	b, err := os.ReadFile(s.appliedPath(ref, txID))
	if err == nil && strings.TrimSpace(string(b)) == newMetadataDigest.String() {
		return true, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	// The marker is written just after the metadata rename.  If a crash
	// lands between those two operations, the old generation's metadata is
	// still durable evidence that publication happened.
	meta, metaErr := os.ReadFile(s.metadataPath(ref))
	if metaErr != nil {
		if errors.Is(metaErr, os.ErrNotExist) {
			return false, nil
		}
		return false, metaErr
	}
	return DigestBytes(meta) == newMetadataDigest, nil
}

func (s *FileStore) Rollback(_ context.Context, txID string) error {
	var errs []error
	for _, suffix := range []string{"data", "meta"} {
		if err := removeIfExists(s.stagePath(txID, suffix)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *FileStore) CleanupOrphans(_ context.Context, keep map[TxID]struct{}) error {
	entries, err := os.ReadDir(s.stagingDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	keepHashes := make(map[string]struct{}, len(keep))
	for txID := range keep {
		keepHashes[DigestString(txID).String()] = struct{}{}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		base := entry.Name()
		if dot := strings.IndexByte(base, '.'); dot >= 0 {
			base = base[:dot]
		}
		if _, ok := keepHashes[base]; ok {
			continue
		}
		if err := removeIfExists(filepath.Join(s.stagingDir, entry.Name())); err != nil {
			return err
		}
	}
	return syncDir(s.stagingDir)
}

func (s *FileStore) checkCurrent(ref ObjectRef) error {
	current, err := s.CurrentGeneration(context.Background(), ref.ID)
	if err != nil {
		return err
	}
	if current != ref.Generation {
		return fmt.Errorf("%w: object %q current=%d requested=%d", ErrObjectGenerationChanged, ref.ID, current, ref.Generation)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".netsec-tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := writeAll(tmp, data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return syncDir(dir)
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		// Some filesystems do not support directory fsync.  Data file
		// fsync plus atomic rename is still used in that case.
		if errors.Is(err, os.ErrInvalid) {
			return nil
		}
		if strings.Contains(strings.ToLower(err.Error()), "invalid argument") ||
			strings.Contains(strings.ToLower(err.Error()), "not supported") {
			return nil
		}
		return err
	}
	return nil
}
