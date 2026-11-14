package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	storeVersion = 1

	defaultMaxArtifactSize = int64(2 << 30) // 2 GiB
	maxArtifactNameLength  = 1024
	copyBufferSize         = 64 << 10
)

// Option configures a Store.
type Option func(*storeOptions) error

type storeOptions struct {
	now         func() time.Time
	defaultTTL  time.Duration
	maxArtifact int64
}

// WithClock supplies a deterministic clock, primarily for tests.
func WithClock(now func() time.Time) Option {
	return func(opts *storeOptions) error {
		if now == nil {
			return errors.New("clock must not be nil")
		}
		opts.now = now
		return nil
	}
}

// WithDefaultTTL sets the retention window applied when an upload does not
// specify an explicit expiry. A zero duration means artifacts do not expire by
// default.
func WithDefaultTTL(ttl time.Duration) Option {
	return func(opts *storeOptions) error {
		if ttl < 0 {
			return errors.New("default TTL must not be negative")
		}
		opts.defaultTTL = ttl
		return nil
	}
}

// WithMaxArtifactSize limits the size of a single upload. A zero value uses the
// built-in 2 GiB limit; a negative value disables the limit.
func WithMaxArtifactSize(size int64) Option {
	return func(opts *storeOptions) error {
		if size == 0 {
			opts.maxArtifact = defaultMaxArtifactSize
			return nil
		}
		opts.maxArtifact = size
		return nil
	}
}

// UploadOption configures one upload.
type UploadOption func(*uploadOptions) error

type uploadOptions struct {
	expiresAt      time.Time
	ttl            time.Duration
	ttlSet         bool
	expectedDigest *Digest
	mediaType      string
}

// WithExpiresAt sets an absolute retention deadline.
func WithExpiresAt(expiresAt time.Time) UploadOption {
	return func(opts *uploadOptions) error {
		opts.expiresAt = expiresAt
		return nil
	}
}

// WithTTL sets a relative retention window. A zero duration means "do not use
// the store default", i.e. no expiry unless WithExpiresAt is also supplied.
func WithTTL(ttl time.Duration) UploadOption {
	return func(opts *uploadOptions) error {
		if ttl < 0 {
			return errors.New("TTL must not be negative")
		}
		opts.ttl = ttl
		opts.ttlSet = true
		return nil
	}
}

// WithExpectedDigest requires an upload's content to match the supplied digest.
// It is useful when a caller already knows the digest of the bytes.
func WithExpectedDigest(d Digest) UploadOption {
	return func(opts *uploadOptions) error {
		parsed, err := ParseDigest(string(d))
		if err != nil {
			return err
		}
		opts.expectedDigest = &parsed
		return nil
	}
}

// WithMediaType records an informational media type with the artifact.
func WithMediaType(mediaType string) UploadOption {
	return func(opts *uploadOptions) error {
		opts.mediaType = mediaType
		return nil
	}
}

// DownloadOption configures one download.
type DownloadOption func(*downloadOptions) error

type downloadOptions struct {
	expectedDigest *Digest
}

// WithDownloadDigest requires the resolved artifact to have the supplied digest.
func WithDownloadDigest(d Digest) DownloadOption {
	return func(opts *downloadOptions) error {
		parsed, err := ParseDigest(string(d))
		if err != nil {
			return err
		}
		opts.expectedDigest = &parsed
		return nil
	}
}

type manifest struct {
	Version int                   `json:"version"`
	Entries map[string]*diskEntry `json:"entries"`
}

type diskEntry struct {
	Artifact Artifact `json:"artifact"`
	Events   []Event  `json:"events"`
}

// Store is a content-addressed artifact store with immutable name bindings.
//
// A name is bound once to one digest inside a (workspace, scope, run)
// namespace. Uploading different content under the same name is a conflict and
// never replaces the original record. Blobs are stored by digest, so even a
// filesystem-level replacement is detected by the digest check performed before
// any bytes are released to a downloader.
type Store struct {
	mu           sync.RWMutex
	root         string
	blobDir      string
	tmpDir       string
	manifestPath string
	data         manifest
	now          func() time.Time
	defaultTTL   time.Duration
	maxArtifact  int64
}

// ArtifactStore is an alias for Store.
type ArtifactStore = Store

// NewStore opens or creates an artifact store rooted at root. The directory may
// be shared by multiple pipeline processes at different times; concurrent
// access from separate processes is not supported.
func NewStore(root string, opts ...Option) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("artifact store root is required")
	}

	settings := storeOptions{
		now:         time.Now,
		maxArtifact: defaultMaxArtifactSize,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&settings); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact store root: %w", err)
	}
	blobDir := filepath.Join(root, "blobs")
	tmpDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(blobDir, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact blob directory: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact temporary directory: %w", err)
	}

	store := &Store{
		root:         root,
		blobDir:      blobDir,
		tmpDir:       tmpDir,
		manifestPath: filepath.Join(root, "manifest.json"),
		data: manifest{
			Version: storeVersion,
			Entries: make(map[string]*diskEntry),
		},
		now:         settings.now,
		defaultTTL:  settings.defaultTTL,
		maxArtifact: settings.maxArtifact,
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	store.removeStaleTemps()
	return store, nil
}

// OpenStore is an alias for NewStore.
func OpenStore(root string, opts ...Option) (*Store, error) {
	return NewStore(root, opts...)
}

// New is an alias for NewStore.
func New(root string, opts ...Option) (*Store, error) {
	return NewStore(root, opts...)
}

// Open is an alias for NewStore.
func Open(root string, opts ...Option) (*Store, error) {
	return NewStore(root, opts...)
}

// NewArtifactStore is an alias for NewStore.
func NewArtifactStore(root string, opts ...Option) (*Store, error) {
	return NewStore(root, opts...)
}

// Root returns the filesystem root used by the store.
func (s *Store) Root() string { return s.root }

func (s *Store) load() error {
	data, err := os.ReadFile(s.manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read artifact manifest: %w", err)
	}
	var loaded manifest
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("%w: decode manifest: %v", ErrCorruptStore, err)
	}
	if loaded.Version != storeVersion {
		return fmt.Errorf("%w: unsupported manifest version %d", ErrCorruptStore, loaded.Version)
	}
	if loaded.Entries == nil {
		loaded.Entries = make(map[string]*diskEntry)
	}
	for key, entry := range loaded.Entries {
		if entry == nil {
			return fmt.Errorf("%w: nil entry %q", ErrCorruptStore, key)
		}
		artifact := entry.Artifact
		if artifact.Name == "" {
			return fmt.Errorf("%w: entry %q has no name", ErrCorruptStore, key)
		}
		if _, err := ParseDigest(string(artifact.Digest)); err != nil {
			return fmt.Errorf("%w: entry %q: %v", ErrCorruptStore, key, err)
		}
		if key != recordKey(artifact.Source.normalize(), artifact.Name) {
			return fmt.Errorf("%w: entry key %q does not match metadata", ErrCorruptStore, key)
		}
	}
	s.data = loaded
	return nil
}

func (s *Store) removeStaleTemps() {
	entries, err := os.ReadDir(s.tmpDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		_ = os.Remove(filepath.Join(s.tmpDir, entry.Name()))
	}
}

func recordKey(source Source, name string) string {
	var builder strings.Builder
	builder.WriteString(source.Workspace)
	builder.WriteByte(0)
	builder.WriteString(source.Scope)
	builder.WriteByte(0)
	builder.WriteString(source.RunID)
	builder.WriteByte(0)
	builder.WriteString(name)
	return builder.String()
}

func (s *Store) blobPath(d Digest) string {
	hexDigest := string(d)
	return filepath.Join(s.blobDir, hexDigest[:2], hexDigest)
}

func validateName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: name is required", ErrInvalidName)
	case len(name) > maxArtifactNameLength:
		return fmt.Errorf("%w: name exceeds %d bytes", ErrInvalidName, maxArtifactNameLength)
	case !utf8.ValidString(name):
		return fmt.Errorf("%w: name is not valid UTF-8", ErrInvalidName)
	case strings.ContainsRune(name, 0):
		return fmt.Errorf("%w: name contains NUL", ErrInvalidName)
	case strings.ContainsAny(name, "\r\n"):
		return fmt.Errorf("%w: name contains a line break", ErrInvalidName)
	default:
		return nil
	}
}

// Upload streams content into the store under name and binds the name to the
// content digest for source's workspace/scope/run. If name already exists with
// the same digest the call is idempotent; if it exists with a different digest
// the original record is left untouched and *ConflictError is returned.
func (s *Store) Upload(ctx context.Context, source Source, name string, content io.Reader, opts ...UploadOption) (*Artifact, error) {
	if content == nil {
		return nil, errors.New("artifact content must not be nil")
	}
	if err := validateName(name); err != nil {
		return nil, err
	}
	source = source.normalize()
	if err := source.validate(); err != nil {
		return nil, err
	}
	uploadOpts := uploadOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&uploadOpts); err != nil {
			return nil, err
		}
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}

	tempPath, digest, size, err := s.stage(ctx, content, s.maxArtifact)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tempPath)

	if uploadOpts.expectedDigest != nil && digest != *uploadOpts.expectedDigest {
		return nil, &DigestMismatchError{
			Name:     name,
			Expected: *uploadOpts.expectedDigest,
			Actual:   digest,
		}
	}

	now := s.now().UTC()
	expiresAt := uploadOpts.expiresAt.UTC()
	if expiresAt.IsZero() {
		effectiveTTL := s.defaultTTL
		if uploadOpts.ttlSet {
			effectiveTTL = uploadOpts.ttl
		}
		if effectiveTTL > 0 {
			expiresAt = now.Add(effectiveTTL)
		}
	}

	key := recordKey(source, name)
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.data.Entries[key]; ok {
		if existing.Artifact.Digest == digest {
			// An idempotent upload may also repair a missing or corrupted blob;
			// the bytes were just verified against the bound digest.
			if err := s.installBlob(tempPath, digest, size); err != nil {
				return nil, err
			}
			artifact := existing.Artifact
			return &artifact, nil
		}
		conflict := &ConflictError{
			Name:           name,
			ExistingDigest: existing.Artifact.Digest,
			NewDigest:      digest,
			ExistingSource: existing.Artifact.Source,
		}
		existing.Events = append(existing.Events, Event{
			Kind:           EventConflict,
			At:             now,
			Actor:          source,
			Digest:         existing.Artifact.Digest,
			IncomingDigest: digest,
			Note:           fmt.Sprintf("rejected upload with digest %s", digest),
		})
		if err := s.save(); err != nil {
			return nil, err
		}
		return nil, conflict
	}

	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if err := s.installBlob(tempPath, digest, size); err != nil {
		return nil, err
	}

	artifact := Artifact{
		Name:      name,
		Digest:    digest,
		Size:      size,
		MediaType: uploadOpts.mediaType,
		Source:    source,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	entry := &diskEntry{
		Artifact: artifact,
		Events: []Event{{
			Kind:   EventUpload,
			At:     now,
			Actor:  source,
			Digest: digest,
		}},
	}
	s.data.Entries[key] = entry
	if err := s.save(); err != nil {
		delete(s.data.Entries, key)
		return nil, err
	}
	return &artifact, nil
}

// UploadBytes is a convenience wrapper around Upload.
func (s *Store) UploadBytes(ctx context.Context, source Source, name string, data []byte, opts ...UploadOption) (*Artifact, error) {
	return s.Upload(ctx, source, name, bytes.NewReader(data), opts...)
}

// Publish is a context-free convenience wrapper around Upload.
func (s *Store) Publish(source Source, name string, content io.Reader, opts ...UploadOption) (*Artifact, error) {
	return s.Upload(context.Background(), source, name, content, opts...)
}

// PublishBytes is a context-free byte-slice wrapper around Upload.
func (s *Store) PublishBytes(source Source, name string, data []byte, opts ...UploadOption) (*Artifact, error) {
	return s.UploadBytes(context.Background(), source, name, data, opts...)
}

// Put is an alias for Upload.
func (s *Store) Put(ctx context.Context, source Source, name string, content io.Reader, opts ...UploadOption) (*Artifact, error) {
	return s.Upload(ctx, source, name, content, opts...)
}

// PutBytes is an alias for UploadBytes.
func (s *Store) PutBytes(ctx context.Context, source Source, name string, data []byte, opts ...UploadOption) (*Artifact, error) {
	return s.UploadBytes(ctx, source, name, data, opts...)
}

func (s *Store) stage(ctx context.Context, content io.Reader, limit int64) (string, Digest, int64, error) {
	file, err := os.CreateTemp(s.tmpDir, "upload-*")
	if err != nil {
		return "", "", 0, fmt.Errorf("create upload staging file: %w", err)
	}
	path := file.Name()
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", "", 0, fmt.Errorf("secure upload staging file: %w", err)
	}

	hash := sha256.New()
	buffer := make([]byte, copyBufferSize)
	var size int64
	for {
		if err := checkContext(ctx); err != nil {
			return "", "", 0, err
		}
		n, readErr := content.Read(buffer)
		if n > 0 {
			size += int64(n)
			if limit >= 0 && size > limit {
				return "", "", 0, fmt.Errorf("%w: limit is %d bytes", ErrTooLarge, limit)
			}
			if _, err := file.Write(buffer[:n]); err != nil {
				return "", "", 0, fmt.Errorf("write upload staging file: %w", err)
			}
			if _, err := hash.Write(buffer[:n]); err != nil {
				return "", "", 0, fmt.Errorf("hash upload: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", 0, fmt.Errorf("read upload: %w", readErr)
		}
		if n == 0 {
			continue
		}
	}
	if err := file.Sync(); err != nil {
		return "", "", 0, fmt.Errorf("sync upload staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", "", 0, fmt.Errorf("close upload staging file: %w", err)
	}
	sum := hash.Sum(nil)
	success = true
	return path, Digest(hex.EncodeToString(sum)), size, nil
}

func (s *Store) installBlob(tempPath string, digest Digest, size int64) error {
	path := s.blobPath(digest)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create blob shard: %w", err)
	}

	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: blob %s is not a regular file", ErrCorruptStore, digest)
		}
		if err := verifyFileDigest(path, digest, size); err == nil {
			return nil
		}
		// The content-addressed location exists but does not contain the
		// advertised bytes. A legitimate upload of verified bytes may repair it.
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("repair blob permissions: %w", err)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove corrupt blob: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect blob: %w", err)
	}

	// Hard-linking the already-synced staging file publishes the blob without
	// ever exposing a partially written file at the content-addressed path.
	if err := os.Link(tempPath, path); err == nil {
		_ = os.Chmod(path, 0o444)
		return nil
	} else if !errors.Is(err, os.ErrExist) {
		// Some filesystems do not support hard links; fall back to an exclusive
		// create and copy while retaining the same no-overwrite property.
		return s.copyBlobExclusive(tempPath, path, digest, size)
	}
	if err := verifyFileDigest(path, digest, size); err != nil {
		return err
	}
	return nil
}

func (s *Store) copyBlobExclusive(source, destination string, digest Digest, size int64) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open staging blob: %w", err)
	}
	defer sourceFile.Close()

	destinationFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return verifyFileDigest(destination, digest, size)
	}
	if err != nil {
		return fmt.Errorf("create blob: %w", err)
	}
	success := false
	defer func() {
		_ = destinationFile.Close()
		if !success {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		return fmt.Errorf("copy blob: %w", err)
	}
	if err := destinationFile.Sync(); err != nil {
		return fmt.Errorf("sync blob: %w", err)
	}
	if err := destinationFile.Close(); err != nil {
		return fmt.Errorf("close blob: %w", err)
	}
	if err := verifyFileDigest(destination, digest, size); err != nil {
		return err
	}
	success = true
	return os.Chmod(destination, 0o444)
}

func verifyFileDigest(path string, expected Digest, expectedSize int64) error {
	file, err := openRegularFile(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat blob: %w", err)
	}
	if expectedSize >= 0 && info.Size() != expectedSize {
		return &DigestMismatchError{Expected: expected, Actual: Digest("size-mismatch")}
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash blob: %w", err)
	}
	actual := Digest(hex.EncodeToString(hash.Sum(nil)))
	if actual != expected {
		return &DigestMismatchError{Expected: expected, Actual: actual}
	}
	return nil
}

func openRegularFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("inspect blob: %w", err)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: blob is not a regular file", ErrCorruptStore)
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("open blob: %w", err)
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("stat blob: %w", err)
	}
	if !os.SameFile(before, after) {
		_ = file.Close()
		return nil, fmt.Errorf("%w: blob changed while opening", ErrCorruptStore)
	}
	return file, nil
}

// Download verifies the artifact digest and provenance before writing any bytes
// to destination. Bytes that fail verification are never released to the
// caller. A successful download is appended to the artifact's provenance chain.
func (s *Store) Download(ctx context.Context, requester Source, name string, destination io.Writer, opts ...DownloadOption) (*Artifact, error) {
	if destination == nil {
		return nil, errors.New("download destination must not be nil")
	}
	artifact, stagedPath, cleanup, err := s.prepareDownload(ctx, requester, name, opts...)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	if err := copyFileToWriter(ctx, stagedPath, destination); err != nil {
		return nil, err
	}
	return artifact, nil
}

// DownloadBytes downloads and verifies an artifact and returns its bytes.
func (s *Store) DownloadBytes(ctx context.Context, requester Source, name string, opts ...DownloadOption) ([]byte, *Artifact, error) {
	var buffer bytes.Buffer
	artifact, err := s.Download(ctx, requester, name, &buffer, opts...)
	if err != nil {
		return nil, nil, err
	}
	return buffer.Bytes(), artifact, nil
}

// Fetch returns verified artifact bytes without metadata.
func (s *Store) Fetch(ctx context.Context, requester Source, name string, opts ...DownloadOption) ([]byte, error) {
	data, _, err := s.DownloadBytes(ctx, requester, name, opts...)
	return data, err
}

// Get is an alias for Fetch.
func (s *Store) Get(ctx context.Context, requester Source, name string, opts ...DownloadOption) ([]byte, error) {
	return s.Fetch(ctx, requester, name, opts...)
}

// Read is an alias for Download.
func (s *Store) Read(ctx context.Context, requester Source, name string, destination io.Writer, opts ...DownloadOption) (*Artifact, error) {
	return s.Download(ctx, requester, name, destination, opts...)
}

// Open verifies the artifact and returns a reader over verified bytes. The
// reader must be closed by the caller.
func (s *Store) Open(ctx context.Context, requester Source, name string, opts ...DownloadOption) (io.ReadCloser, *Artifact, error) {
	artifact, stagedPath, cleanup, err := s.prepareDownload(ctx, requester, name, opts...)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(stagedPath)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("open verified artifact: %w", err)
	}
	return &cleanupReadCloser{File: file, cleanup: cleanup}, artifact, nil
}

type cleanupReadCloser struct {
	*os.File
	cleanup func()
	once    sync.Once
}

func (r *cleanupReadCloser) Close() error {
	err := r.File.Close()
	r.once.Do(r.cleanup)
	return err
}

func (s *Store) prepareDownload(ctx context.Context, requester Source, name string, opts ...DownloadOption) (*Artifact, string, func(), error) {
	if err := validateName(name); err != nil {
		return nil, "", nil, err
	}
	requester = requester.normalize()
	if err := requester.validate(); err != nil {
		return nil, "", nil, err
	}
	downloadOpts := downloadOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&downloadOpts); err != nil {
			return nil, "", nil, err
		}
	}
	if err := checkContext(ctx); err != nil {
		return nil, "", nil, err
	}

	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, err := s.lookupForConsumer(requester, name)
	if err != nil {
		return nil, "", nil, err
	}
	artifact := entry.Artifact
	if artifact.Expired(now) {
		return nil, "", nil, &ExpiredError{Name: name, ExpiresAt: artifact.ExpiresAt, Now: now}
	}
	if downloadOpts.expectedDigest != nil && artifact.Digest != *downloadOpts.expectedDigest {
		return nil, "", nil, &DigestMismatchError{
			Name:     name,
			Expected: *downloadOpts.expectedDigest,
			Actual:   artifact.Digest,
		}
	}

	stagedPath, actual, err := s.stageVerifiedBlob(ctx, artifact.Digest, artifact.Size)
	if err != nil {
		var mismatch *DigestMismatchError
		if errors.As(err, &mismatch) {
			mismatch.Name = name
			entry.Events = append(entry.Events, Event{
				Kind:         EventIntegrityFailure,
				At:           now,
				Actor:        requester,
				Digest:       artifact.Digest,
				ActualDigest: actual,
				Note:         "download refused because stored content failed digest verification",
			})
			if saveErr := s.save(); saveErr != nil {
				return nil, "", nil, saveErr
			}
		}
		return nil, "", nil, err
	}
	cleanup := func() { _ = os.Remove(stagedPath) }

	entry.Events = append(entry.Events, Event{
		Kind:   EventDownload,
		At:     now,
		Actor:  requester,
		Digest: artifact.Digest,
	})
	if err := s.save(); err != nil {
		cleanup()
		return nil, "", nil, err
	}
	artifactCopy := artifact
	return &artifactCopy, stagedPath, cleanup, nil
}

func (s *Store) lookupForConsumer(requester Source, name string) (*diskEntry, error) {
	key := recordKey(requester, name)
	if entry, ok := s.data.Entries[key]; ok {
		artifact := entry.Artifact
		if artifact.Source.Workspace != requester.Workspace ||
			artifact.Source.Scope != requester.Scope ||
			artifact.Source.RunID != requester.RunID {
			return nil, &AccessError{
				Name:    name,
				Source:  artifact.Source,
				Request: requester,
				Reason:  ErrForbidden,
			}
		}
		return entry, nil
	}

	// Distinguish cross-workspace and cross-run attempts from an ordinary
	// missing artifact so audit logs and API clients receive a precise reason.
	crossWorkspace := false
	crossRun := false
	crossScope := false
	for _, entry := range s.data.Entries {
		if entry.Artifact.Name != name {
			continue
		}
		if entry.Artifact.Source.Workspace != requester.Workspace {
			crossWorkspace = true
			continue
		}
		if entry.Artifact.Source.Scope != requester.Scope {
			crossScope = true
			continue
		}
		if entry.Artifact.Source.RunID != requester.RunID {
			crossRun = true
		}
	}
	switch {
	case crossRun:
		return nil, &AccessError{Name: name, Request: requester, Reason: ErrCrossRun}
	case crossScope:
		return nil, &AccessError{Name: name, Request: requester, Reason: ErrForbidden}
	case crossWorkspace:
		return nil, &AccessError{Name: name, Request: requester, Reason: ErrCrossWorkspace}
	default:
		return nil, fmt.Errorf("%w: %q in workspace %q run %q", ErrNotFound, name, requester.Workspace, requester.RunID)
	}
}

func (s *Store) stageVerifiedBlob(ctx context.Context, digest Digest, size int64) (string, Digest, error) {
	source, err := openRegularFile(s.blobPath(digest))
	if err != nil {
		return "", "", err
	}
	defer source.Close()

	temp, err := os.CreateTemp(s.tmpDir, "download-*")
	if err != nil {
		return "", "", fmt.Errorf("create download staging file: %w", err)
	}
	path := temp.Name()
	success := false
	defer func() {
		_ = temp.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()

	hash := sha256.New()
	buffer := make([]byte, copyBufferSize)
	var readSize int64
	for {
		if err := checkContext(ctx); err != nil {
			return "", "", err
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			readSize += int64(n)
			if readSize > size {
				return "", Digest("size-mismatch"), &DigestMismatchError{Expected: digest, Actual: Digest("size-mismatch")}
			}
			if _, err := temp.Write(buffer[:n]); err != nil {
				return "", "", fmt.Errorf("write download staging file: %w", err)
			}
			if _, err := hash.Write(buffer[:n]); err != nil {
				return "", "", fmt.Errorf("hash download: %w", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", "", fmt.Errorf("read blob: %w", readErr)
		}
	}
	actual := Digest(hex.EncodeToString(hash.Sum(nil)))
	if readSize != size || actual != digest {
		return "", actual, &DigestMismatchError{Expected: digest, Actual: actual}
	}
	if err := temp.Sync(); err != nil {
		return "", actual, fmt.Errorf("sync download staging file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", actual, fmt.Errorf("close download staging file: %w", err)
	}
	success = true
	return path, actual, nil
}

func copyFileToWriter(ctx context.Context, path string, destination io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open verified content: %w", err)
	}
	defer file.Close()
	buffer := make([]byte, copyBufferSize)
	for {
		if err := checkContext(ctx); err != nil {
			return err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			if _, err := destination.Write(buffer[:n]); err != nil {
				return fmt.Errorf("write download destination: %w", err)
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read verified content: %w", readErr)
		}
	}
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func (s *Store) save() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode artifact manifest: %w", err)
	}
	temp, err := os.CreateTemp(s.root, "manifest-*")
	if err != nil {
		return fmt.Errorf("create manifest staging file: %w", err)
	}
	tempPath := temp.Name()
	success := false
	defer func() {
		_ = temp.Close()
		if !success {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("secure manifest staging file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write artifact manifest: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync artifact manifest: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close artifact manifest: %w", err)
	}
	if err := os.Rename(tempPath, s.manifestPath); err != nil {
		return fmt.Errorf("publish artifact manifest: %w", err)
	}
	success = true
	return nil
}

// Stat returns artifact metadata after applying the same access and expiry
// checks as Download, but does not create a download event.
func (s *Store) Stat(ctx context.Context, requester Source, name string) (*Artifact, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	requester = requester.normalize()
	if err := requester.validate(); err != nil {
		return nil, err
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, err := s.lookupForConsumer(requester, name)
	if err != nil {
		return nil, err
	}
	if entry.Artifact.Expired(now) {
		return nil, &ExpiredError{Name: name, ExpiresAt: entry.Artifact.ExpiresAt, Now: now}
	}
	artifact := entry.Artifact
	return &artifact, nil
}

// Provenance returns the immutable source binding and the full event chain for
// an artifact. Access and expiry are checked in the same way as Stat.
func (s *Store) Provenance(ctx context.Context, requester Source, name string) (*Provenance, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	requester = requester.normalize()
	if err := requester.validate(); err != nil {
		return nil, err
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry, err := s.lookupForConsumer(requester, name)
	if err != nil {
		return nil, err
	}
	if entry.Artifact.Expired(now) {
		return nil, &ExpiredError{Name: name, ExpiresAt: entry.Artifact.ExpiresAt, Now: now}
	}
	return &Provenance{
		Artifact: entry.Artifact,
		Events:   append([]Event(nil), entry.Events...),
	}, nil
}

// Chain returns only the ordered provenance events for an artifact.
func (s *Store) Chain(ctx context.Context, requester Source, name string) ([]Event, error) {
	provenance, err := s.Provenance(ctx, requester, name)
	if err != nil {
		return nil, err
	}
	return provenance.Events, nil
}

// List returns metadata for unexpired artifacts visible to requester. The
// returned slice is sorted by name and then creation time.
func (s *Store) List(ctx context.Context, requester Source) ([]Artifact, error) {
	requester = requester.normalize()
	if err := requester.validate(); err != nil {
		return nil, err
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifacts := make([]Artifact, 0)
	for _, entry := range s.data.Entries {
		artifact := entry.Artifact
		if artifact.Source.Workspace != requester.Workspace ||
			artifact.Source.Scope != requester.Scope ||
			artifact.Source.RunID != requester.RunID {
			continue
		}
		if artifact.Expired(now) {
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	sort.Slice(artifacts, func(i, j int) bool {
		if artifacts[i].Name == artifacts[j].Name {
			return artifacts[i].CreatedAt.Before(artifacts[j].CreatedAt)
		}
		return artifacts[i].Name < artifacts[j].Name
	})
	return artifacts, nil
}

// FindByDigest returns an accessible artifact record with the supplied digest.
// If multiple names share a digest, the lexicographically smallest name is
// returned. Use Stat or Download with an explicit name when exact provenance
// matters.
func (s *Store) FindByDigest(ctx context.Context, requester Source, digest Digest) (*Artifact, error) {
	parsed, err := ParseDigest(string(digest))
	if err != nil {
		return nil, err
	}
	requester = requester.normalize()
	if err := requester.validate(); err != nil {
		return nil, err
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	now := s.now().UTC()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var matches []Artifact
	var expired *Artifact
	crossWorkspace := false
	crossRun := false
	crossScope := false
	for _, entry := range s.data.Entries {
		artifact := entry.Artifact
		if artifact.Digest != parsed {
			continue
		}
		if artifact.Source.Workspace != requester.Workspace ||
			artifact.Source.Scope != requester.Scope ||
			artifact.Source.RunID != requester.RunID {
			if artifact.Source.Workspace != requester.Workspace {
				crossWorkspace = true
			} else if artifact.Source.Scope != requester.Scope {
				crossScope = true
			} else {
				crossRun = true
			}
			continue
		}
		if artifact.Expired(now) {
			candidate := artifact
			expired = &candidate
			continue
		}
		matches = append(matches, artifact)
	}
	if len(matches) == 0 {
		switch {
		case expired != nil:
			return nil, &ExpiredError{Name: expired.Name, ExpiresAt: expired.ExpiresAt, Now: now}
		case crossRun:
			return nil, &AccessError{Request: requester, Reason: ErrCrossRun}
		case crossScope:
			return nil, &AccessError{Request: requester, Reason: ErrForbidden}
		case crossWorkspace:
			return nil, &AccessError{Request: requester, Reason: ErrCrossWorkspace}
		default:
			return nil, fmt.Errorf("%w: digest %s", ErrNotFound, parsed)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })
	artifact := matches[0]
	return &artifact, nil
}

// DownloadDigest resolves an accessible artifact by content digest and
// downloads it by its bound name, performing the same verification and
// provenance recording as Download.
func (s *Store) DownloadDigest(ctx context.Context, requester Source, digest Digest, destination io.Writer, opts ...DownloadOption) (*Artifact, error) {
	artifact, err := s.FindByDigest(ctx, requester, digest)
	if err != nil {
		return nil, err
	}
	options := make([]DownloadOption, 0, len(opts)+1)
	options = append(options, WithDownloadDigest(artifact.Digest))
	options = append(options, opts...)
	return s.Download(ctx, requester, artifact.Name, destination, options...)
}

// FetchDigest returns verified bytes addressed by content digest.
func (s *Store) FetchDigest(ctx context.Context, requester Source, digest Digest, opts ...DownloadOption) ([]byte, error) {
	var buffer bytes.Buffer
	if _, err := s.DownloadDigest(ctx, requester, digest, &buffer, opts...); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// OpenDigest verifies an artifact addressed by content digest and returns a
// reader over the verified bytes.
func (s *Store) OpenDigest(ctx context.Context, requester Source, digest Digest, opts ...DownloadOption) (io.ReadCloser, *Artifact, error) {
	artifact, err := s.FindByDigest(ctx, requester, digest)
	if err != nil {
		return nil, nil, err
	}
	options := make([]DownloadOption, 0, len(opts)+1)
	options = append(options, WithDownloadDigest(artifact.Digest))
	options = append(options, opts...)
	return s.Open(ctx, requester, artifact.Name, options...)
}
