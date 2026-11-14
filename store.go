package netsec

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultPrefix      = "state"
	defaultBackupLimit = 10
	defaultLockTTL     = 2 * time.Minute
	operationLockTTL   = 30 * time.Second
	stateFileMode      = 0o600
	stateDirMode       = 0o700
	maxArtifactSize    = 128 << 20
	versionFormat      = "netsec.state.version.v1"
	lockFormat         = "netsec.state.lock.v1"
)

// Config configures a Store. Root is the workspace-local directory that holds
// encrypted artifacts. Keys must come from a source outside that directory.
type Config struct {
	Root        string
	WorkspaceID string
	AccountID   string
	KeyProvider KeyProvider
	Prefix      string
	// BackupLimit controls how many encrypted revisions are retained. Zero
	// uses the default of 10; a negative value disables backups.
	BackupLimit int
	// Rand must be a cryptographically secure random source. It defaults to
	// crypto/rand.Reader and should only be overridden by tests.
	Rand         io.Reader
	Clock        func() time.Time
	VerifyCaller CallerVerifier

	// ExportAuthorizer is consulted for every plaintext export. A nil value
	// denies all exports.
	ExportAuthorizer ExportAuthorizer
	AuditSink        AuditSink
}

// Store reads and writes protected state for one workspace and account.
type Store struct {
	root        string
	rootFS      *os.Root
	prefix      string
	workspaceID string
	accountID   string
	keys        KeyProvider
	backupLimit int
	rand        io.Reader
	now         func() time.Time

	verifyCaller     CallerVerifier
	exportAuthorizer ExportAuthorizer
	auditSink        AuditSink

	mu     sync.Mutex
	held   map[string]*LockHandle
	closed bool

	nonceCounter atomic.Uint64
}

// Open creates a store rooted at cfg.Root. The directory is created with 0700
// permissions when absent, but the store does not rely on directory
// permissions for confidentiality.
func Open(cfg Config) (*Store, error) {
	if strings.TrimSpace(cfg.Root) == "" {
		return nil, fmt.Errorf("%w: root is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(cfg.WorkspaceID) == "" {
		return nil, fmt.Errorf("%w: workspace is required", ErrInvalidConfig)
	}
	if strings.TrimSpace(cfg.AccountID) == "" {
		return nil, fmt.Errorf("%w: account is required", ErrInvalidConfig)
	}
	if cfg.KeyProvider == nil {
		return nil, fmt.Errorf("%w: key provider is required", ErrInvalidConfig)
	}

	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve root: %v", ErrInvalidConfig, err)
	}
	if err := os.MkdirAll(root, stateDirMode); err != nil {
		return nil, fmt.Errorf("netsec: create state root: %w", err)
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("netsec: open state root: %w", err)
	}

	prefix := cfg.Prefix
	if prefix == "" {
		prefix = defaultPrefix
	}
	if !validPrefix(prefix) {
		rootFS.Close()
		return nil, fmt.Errorf("%w: prefix must contain only letters, digits, dot, dash, or underscore", ErrInvalidConfig)
	}

	backupLimit := cfg.BackupLimit
	if backupLimit == 0 {
		backupLimit = defaultBackupLimit
	}
	reader := cfg.Rand
	if reader == nil {
		reader = rand.Reader
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}

	return &Store{
		root:             root,
		rootFS:           rootFS,
		prefix:           prefix,
		workspaceID:      strings.TrimSpace(cfg.WorkspaceID),
		accountID:        strings.TrimSpace(cfg.AccountID),
		keys:             cfg.KeyProvider,
		backupLimit:      backupLimit,
		rand:             reader,
		now:              clock,
		verifyCaller:     cfg.VerifyCaller,
		exportAuthorizer: cfg.ExportAuthorizer,
		auditSink:        cfg.AuditSink,
		held:             make(map[string]*LockHandle),
	}, nil
}

// NewStore is an alias for Open.
func NewStore(cfg Config) (*Store, error) {
	return Open(cfg)
}

// New is an alias for Open.
func New(cfg Config) (*Store, error) {
	return Open(cfg)
}

// Close releases the directory handle and best-effort releases locks owned by
// this Store instance. Artifacts remain encrypted on disk.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	handles := make([]*LockHandle, 0, len(s.held))
	for _, handle := range s.held {
		handles = append(handles, handle)
	}
	s.mu.Unlock()

	for _, handle := range handles {
		_ = handle.Release(context.Background())
	}
	if s.rootFS != nil {
		return s.rootFS.Close()
	}
	return nil
}

// WorkspaceID returns the workspace bound to the store.
func (s *Store) WorkspaceID() string { return s.workspaceID }

// AccountID returns the account bound to the store.
func (s *Store) AccountID() string { return s.accountID }

// Root returns the absolute state directory.
func (s *Store) Root() string { return s.root }

// StoreInfo is safe, non-secret metadata about a store.
type StoreInfo struct {
	Root              string
	WorkspaceID       string
	AccountID         string
	CurrentGeneration uint64
	Version           uint64
}

// Info returns non-secret store metadata. When a caller is supplied, Info
// authenticates it before reading the encrypted version record; without a
// caller it does not touch encrypted artifacts and Version is zero.
func (s *Store) Info(ctx context.Context, callers ...Caller) (StoreInfo, error) {
	generation, err := s.keys.CurrentGeneration(ctx)
	if err != nil {
		return StoreInfo{}, fmt.Errorf("%w: %v", ErrKeyGeneration, err)
	}
	info := StoreInfo{
		Root:              s.root,
		WorkspaceID:       s.workspaceID,
		AccountID:         s.accountID,
		CurrentGeneration: generation,
	}
	if len(callers) == 0 {
		return info, nil
	}
	if len(callers) > 1 {
		return StoreInfo{}, fmt.Errorf("%w: Info accepts at most one caller", ErrInvalidConfig)
	}
	if _, err := s.authorize(ctx, callers[0], "info"); err != nil {
		return StoreInfo{}, err
	}
	unlockRoot := lockRoot(s.root)
	defer unlockRoot()
	version, _, err := s.readVersionRecord(ctx)
	if err == nil {
		info.Version = version.Version
	} else if !errors.Is(err, ErrNotFound) {
		return StoreInfo{}, err
	}
	return info, nil
}

func validPrefix(prefix string) bool {
	if prefix == "" || len(prefix) > 64 {
		return false
	}
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return prefix != "." && prefix != ".."
}

func (s *Store) stateName() string {
	return s.prefix + ".state.enc"
}

func (s *Store) versionName() string {
	return s.prefix + ".version.enc"
}

func (s *Store) lockName() string {
	return s.prefix + ".lock.enc"
}

func (s *Store) backupName(version uint64) string {
	return fmt.Sprintf("%s.backup.%020d.enc", s.prefix, version)
}

func (s *Store) backupRecordID(version uint64) string {
	return "backup/" + strconv.FormatUint(version, 10)
}

type versionRecord struct {
	Format     string    `json:"format"`
	Version    uint64    `json:"version"`
	Digest     string    `json:"digest"`
	Generation uint64    `json:"generation"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type lockRecord struct {
	Format      string    `json:"format"`
	OwnerID     string    `json:"owner"`
	WorkspaceID string    `json:"workspace"`
	AccountID   string    `json:"account"`
	Token       string    `json:"token"`
	AcquiredAt  time.Time `json:"acquired_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// WriteState seals data under the active key generation, advances the
// encrypted version, and stores the previous revision as an encrypted backup.
// The returned value is the new version.
func (s *Store) WriteState(ctx context.Context, caller Caller, data []byte) (uint64, error) {
	caller, err := s.authorize(ctx, caller, "write")
	if err != nil {
		return 0, err
	}
	if data == nil {
		data = []byte{}
	}

	unlockRoot := lockRoot(s.root)
	defer unlockRoot()

	handle, owned, err := s.ensureWriteLock(ctx, caller)
	if err != nil {
		s.audit(ctx, "write", caller, false, "lock acquisition failed", err)
		return 0, err
	}
	if owned {
		defer func() {
			_ = s.releaseLockLocked(context.Background(), handle)
		}()
	}

	oldVersion, hasOld, err := s.readVersionRecord(ctx)
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.audit(ctx, "write", caller, false, "version read failed", err)
		return 0, err
	}
	if !hasOld {
		if _, statErr := s.rootFS.Stat(s.stateName()); statErr == nil {
			err := fmt.Errorf("%w: state artifact exists without a version record", ErrIntegrity)
			s.audit(ctx, "write", caller, false, "orphaned state artifact", err)
			return 0, err
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return 0, fmt.Errorf("netsec: inspect state artifact: %w", statErr)
		}
	}

	var oldState []byte
	if hasOld {
		oldState, err = s.readStateFor(ctx, oldVersion)
		if err != nil {
			s.audit(ctx, "write", caller, false, "existing state is not readable", err)
			return 0, fmt.Errorf("%w: refusing to overwrite unreadable state: %v", ErrIntegrity, err)
		}
	}

	generation, err := s.keys.CurrentGeneration(ctx)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrKeyGeneration, err)
	}

	nextVersion := uint64(1)
	if hasOld {
		nextVersion = oldVersion.Version + 1
		if s.backupLimit >= 0 {
			if err := s.writeArtifact(
				ctx,
				s.backupName(oldVersion.Version),
				ArtifactBackup,
				s.backupRecordID(oldVersion.Version),
				oldVersion.Version,
				generation,
				oldState,
			); err != nil {
				s.audit(ctx, "write", caller, false, "backup write failed", err)
				return 0, err
			}
		}
	}

	if err := s.writeArtifact(ctx, s.stateName(), ArtifactState, "state", nextVersion, generation, data); err != nil {
		s.audit(ctx, "write", caller, false, "state write failed", err)
		return 0, err
	}
	record := versionRecord{
		Format:     versionFormat,
		Version:    nextVersion,
		Digest:     digestHex(data),
		Generation: generation,
		UpdatedAt:  s.now().UTC(),
	}
	recordBytes, err := json.Marshal(record)
	if err != nil {
		return 0, fmt.Errorf("netsec: encode version: %w", err)
	}
	if err := s.writeArtifact(ctx, s.versionName(), ArtifactVersion, "version", 0, generation, recordBytes); err != nil {
		s.audit(ctx, "write", caller, false, "version write failed", err)
		return 0, err
	}

	if s.backupLimit > 0 {
		_ = s.pruneBackups(ctx)
	}
	s.audit(ctx, "write", caller, true, "state written", nil)
	return nextVersion, nil
}

// Write is an alias for WriteState.
func (s *Store) Write(ctx context.Context, caller Caller, data []byte) (uint64, error) {
	return s.WriteState(ctx, caller, data)
}

// Save is an alias for WriteState.
func (s *Store) Save(ctx context.Context, caller Caller, data []byte) (uint64, error) {
	return s.WriteState(ctx, caller, data)
}

// ReadState verifies the caller and reads the newest state revision. The
// version record and state payload must agree on version and digest.
func (s *Store) ReadState(ctx context.Context, caller Caller) ([]byte, error) {
	caller, err := s.authorize(ctx, caller, "read")
	if err != nil {
		return nil, err
	}
	unlockRoot := lockRoot(s.root)
	defer unlockRoot()
	version, _, err := s.readVersionRecord(ctx)
	if err != nil {
		s.audit(ctx, "read", caller, false, "version read failed", err)
		return nil, err
	}
	state, err := s.readStateFor(ctx, version)
	if err != nil {
		s.audit(ctx, "read", caller, false, "state read failed", err)
		return nil, err
	}
	s.audit(ctx, "read", caller, true, "state read", nil)
	return state, nil
}

// Read is an alias for ReadState.
func (s *Store) Read(ctx context.Context, caller Caller) ([]byte, error) {
	return s.ReadState(ctx, caller)
}

// Load is an alias for ReadState.
func (s *Store) Load(ctx context.Context, caller Caller) ([]byte, error) {
	return s.ReadState(ctx, caller)
}

// Version returns the current state version.
func (s *Store) Version(ctx context.Context, caller Caller) (uint64, error) {
	caller, err := s.authorize(ctx, caller, "version")
	if err != nil {
		return 0, err
	}
	unlockRoot := lockRoot(s.root)
	defer unlockRoot()
	record, _, err := s.readVersionRecord(ctx)
	if err != nil {
		s.audit(ctx, "version", caller, false, "version read failed", err)
		return 0, err
	}
	s.audit(ctx, "version", caller, true, "version read", nil)
	return record.Version, nil
}

// CurrentVersion is an alias for Version.
func (s *Store) CurrentVersion(ctx context.Context, caller Caller) (uint64, error) {
	return s.Version(ctx, caller)
}

// ReadBackup returns the encrypted-at-rest revision stored for version.
func (s *Store) ReadBackup(ctx context.Context, caller Caller, version uint64) ([]byte, error) {
	caller, err := s.authorize(ctx, caller, "read-backup")
	if err != nil {
		return nil, err
	}
	if version == 0 {
		return nil, fmt.Errorf("%w: version 0 is reserved", ErrNotFound)
	}
	unlockRoot := lockRoot(s.root)
	defer unlockRoot()
	data, _, err := s.readArtifact(ctx, s.backupName(version), ArtifactBackup, s.backupRecordID(version), version)
	if err != nil {
		s.audit(ctx, "read-backup", caller, false, "backup read failed", err)
		return nil, err
	}
	s.audit(ctx, "read-backup", caller, true, "backup read", nil)
	return data, nil
}

// Backups returns the versions for which an encrypted backup exists.
func (s *Store) Backups(ctx context.Context, caller Caller) ([]uint64, error) {
	caller, err := s.authorize(ctx, caller, "list-backups")
	if err != nil {
		return nil, err
	}
	unlockRoot := lockRoot(s.root)
	defer unlockRoot()
	versions, err := s.listBackupVersions()
	if err != nil {
		s.audit(ctx, "list-backups", caller, false, "backup listing failed", err)
		return nil, err
	}
	s.audit(ctx, "list-backups", caller, true, "backup listing", nil)
	return versions, nil
}

func (s *Store) authorize(ctx context.Context, caller Caller, action string) (Caller, error) {
	resolved, err := resolveCaller(ctx, caller)
	if err != nil {
		s.audit(ctx, action, caller, false, "caller resolution failed", err)
		return Caller{}, err
	}
	checked, err := s.checkCallerIdentity(ctx, resolved)
	if err != nil {
		s.audit(ctx, action, resolved, false, "caller identity rejected", err)
		return Caller{}, err
	}
	return checked, nil
}

func (s *Store) readVersionRecord(ctx context.Context) (versionRecord, bool, error) {
	data, generation, err := s.readArtifact(ctx, s.versionName(), ArtifactVersion, "version", 0)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return versionRecord{}, false, err
		}
		return versionRecord{}, false, err
	}
	var record versionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return versionRecord{}, false, fmt.Errorf("%w: malformed version record", ErrIntegrity)
	}
	if record.Format != versionFormat || record.Version == 0 || record.Digest == "" {
		return versionRecord{}, false, fmt.Errorf("%w: invalid version record", ErrIntegrity)
	}
	if record.Generation != generation {
		return versionRecord{}, false, fmt.Errorf("%w: version generation does not match its envelope", ErrIntegrity)
	}
	return record, true, nil
}

func (s *Store) readStateFor(ctx context.Context, version versionRecord) ([]byte, error) {
	data, _, err := s.readArtifact(ctx, s.stateName(), ArtifactState, "state", version.Version)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("%w: version record references missing state", errors.Join(ErrIntegrity, ErrNotFound))
		}
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(digestHex(data)), []byte(version.Digest)) != 1 {
		return nil, fmt.Errorf("%w: state digest does not match version record", errors.Join(ErrVersionMismatch, ErrIntegrity))
	}
	return data, nil
}

func (s *Store) readArtifact(ctx context.Context, name string, kind ArtifactKind, recordID string, sequence uint64) ([]byte, uint64, error) {
	raw, err := s.readFile(name)
	if err != nil {
		return nil, 0, err
	}
	env, err := parseEnvelope(raw)
	if err != nil {
		return nil, 0, err
	}
	rc := recordContext{
		WorkspaceID: s.workspaceID,
		AccountID:   s.accountID,
		Kind:        kind,
		RecordID:    recordID,
		Generation:  env.Generation,
		Sequence:    sequence,
	}
	plaintext, err := s.open(ctx, rc, raw)
	if err != nil {
		return nil, 0, err
	}
	return plaintext, env.Generation, nil
}

func (s *Store) writeArtifact(ctx context.Context, name string, kind ArtifactKind, recordID string, sequence, generation uint64, data []byte) error {
	rc := recordContext{
		WorkspaceID: s.workspaceID,
		AccountID:   s.accountID,
		Kind:        kind,
		RecordID:    recordID,
		Generation:  generation,
		Sequence:    sequence,
	}
	raw, err := s.seal(ctx, rc, data)
	if err != nil {
		return err
	}
	return s.writeFileAtomic(name, raw)
}

func (s *Store) readFile(name string) ([]byte, error) {
	f, err := s.rootFS.Open(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		return nil, fmt.Errorf("netsec: open %s: %w", name, err)
	}
	defer f.Close()

	limited := io.LimitReader(f, maxArtifactSize+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("netsec: read %s: %w", name, err)
	}
	if len(raw) > maxArtifactSize {
		return nil, fmt.Errorf("%w: artifact is too large", ErrIntegrity)
	}
	return raw, nil
}

func (s *Store) writeFileAtomic(name string, data []byte) error {
	tmp, err := os.CreateTemp(s.root, "."+filepath.Base(name)+".tmp-*")
	if err != nil {
		return fmt.Errorf("netsec: create temporary artifact: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if err := tmp.Chmod(stateFileMode); err != nil {
		cleanup()
		return fmt.Errorf("netsec: set artifact mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("netsec: write temporary artifact: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("netsec: sync temporary artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("netsec: close temporary artifact: %w", err)
	}

	target := filepath.Join(s.root, name)
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("netsec: publish artifact: %w", err)
	}
	if dir, err := os.Open(s.root); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s *Store) writeFileExclusive(name string, data []byte) error {
	tmp, err := os.CreateTemp(s.root, "."+filepath.Base(name)+".tmp-*")
	if err != nil {
		return fmt.Errorf("netsec: create temporary lock: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if err := tmp.Chmod(stateFileMode); err != nil {
		cleanup()
		return fmt.Errorf("netsec: set lock mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("netsec: write temporary lock: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("netsec: sync temporary lock: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("netsec: close temporary lock: %w", err)
	}

	target := filepath.Join(s.root, name)
	if err := os.Link(tmpName, target); err != nil {
		os.Remove(tmpName)
		if errors.Is(err, fs.ErrExist) {
			return ErrLocked
		}
		return fmt.Errorf("netsec: publish lock: %w", err)
	}
	_ = os.Remove(tmpName)
	return nil
}

func (s *Store) listBackupVersions() ([]uint64, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("netsec: list state root: %w", err)
	}
	prefix := s.prefix + ".backup."
	var versions []uint64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".enc") {
			continue
		}
		number := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".enc")
		version, err := strconv.ParseUint(number, 10, 64)
		if err != nil || version == 0 {
			continue
		}
		if s.backupName(version) != name {
			continue
		}
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions, nil
}

func (s *Store) pruneBackups(ctx context.Context) error {
	if s.backupLimit <= 0 {
		return nil
	}
	versions, err := s.listBackupVersions()
	if err != nil {
		return err
	}
	if len(versions) <= s.backupLimit {
		return nil
	}
	for _, version := range versions[:len(versions)-s.backupLimit] {
		if err := s.rootFS.Remove(s.backupName(version)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func digestHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// LockHandle represents one successfully acquired exclusive lock.
type LockHandle struct {
	store     *Store
	caller    Caller
	token     string
	expiresAt time.Time
}

// ExpiresAt returns the lock expiry time.
func (h *LockHandle) ExpiresAt() time.Time { return h.expiresAt }

// Release releases the lock if this handle still owns it.
func (h *LockHandle) Release(ctx context.Context) error {
	if h == nil || h.store == nil {
		return nil
	}
	unlockRoot := lockRoot(h.store.root)
	defer unlockRoot()
	return h.store.releaseLockLocked(ctx, h)
}

// AcquireLock creates the encrypted exclusive lock. A zero ttl uses two
// minutes. Other callers receive ErrLocked until the lock expires or is
// released.
func (s *Store) AcquireLock(ctx context.Context, caller Caller, ttl time.Duration) (*LockHandle, error) {
	caller, err := s.authorize(ctx, caller, "lock")
	if err != nil {
		return nil, err
	}
	if ttl <= 0 {
		ttl = defaultLockTTL
	}
	unlockRoot := lockRoot(s.root)
	defer unlockRoot()

	handle, err := s.acquireLockLocked(ctx, caller, ttl)
	if err != nil {
		s.audit(ctx, "lock", caller, false, "lock acquisition failed", err)
		return nil, err
	}
	s.mu.Lock()
	s.held[handle.token] = handle
	s.mu.Unlock()
	s.audit(ctx, "lock", caller, true, "lock acquired", nil)
	return handle, nil
}

func (s *Store) ensureWriteLock(ctx context.Context, caller Caller) (*LockHandle, bool, error) {
	s.mu.Lock()
	for _, handle := range s.held {
		if handle.caller.ID == caller.ID &&
			handle.caller.WorkspaceID == caller.WorkspaceID &&
			handle.caller.AccountID == caller.AccountID &&
			s.now().Before(handle.expiresAt) {
			s.mu.Unlock()
			return handle, false, nil
		}
	}
	s.mu.Unlock()

	handle, err := s.acquireLockLocked(ctx, caller, operationLockTTL)
	if err != nil {
		return nil, false, err
	}
	return handle, true, nil
}

func (s *Store) acquireLockLocked(ctx context.Context, caller Caller, ttl time.Duration) (*LockHandle, error) {
	now := s.now()
	existing, _, err := s.readArtifact(ctx, s.lockName(), ArtifactLock, "lock", 0)
	switch {
	case err == nil:
		record, parseErr := parseLockRecord(existing)
		if parseErr != nil {
			return nil, parseErr
		}
		if now.Before(record.ExpiresAt) {
			return nil, fmt.Errorf("%w: held by %s until %s", ErrLocked, record.OwnerID, record.ExpiresAt.UTC().Format(time.RFC3339))
		}
		if err := s.rootFS.Remove(s.lockName()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("netsec: remove expired lock: %w", err)
		}
	case errors.Is(err, ErrNotFound):
	case errors.Is(err, ErrIntegrity):
		if info, statErr := s.rootFS.Stat(s.lockName()); statErr == nil && now.Sub(info.ModTime()) > ttl {
			if removeErr := s.rootFS.Remove(s.lockName()); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				return nil, fmt.Errorf("netsec: remove stale lock: %w", removeErr)
			}
		} else {
			return nil, fmt.Errorf("%w: lock artifact is unreadable", ErrLocked)
		}
	default:
		return nil, err
	}

	generation, err := s.keys.CurrentGeneration(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyGeneration, err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(s.rand, tokenBytes); err != nil {
		return nil, fmt.Errorf("netsec: generate lock token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	record := lockRecord{
		Format:      lockFormat,
		OwnerID:     caller.ID,
		WorkspaceID: s.workspaceID,
		AccountID:   s.accountID,
		Token:       token,
		AcquiredAt:  now.UTC(),
		ExpiresAt:   now.Add(ttl).UTC(),
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("netsec: encode lock: %w", err)
	}
	rc := recordContext{
		WorkspaceID: s.workspaceID,
		AccountID:   s.accountID,
		Kind:        ArtifactLock,
		RecordID:    "lock",
		Generation:  generation,
	}
	raw, err := s.seal(ctx, rc, payload)
	if err != nil {
		return nil, err
	}
	if err := s.writeFileExclusive(s.lockName(), raw); err != nil {
		if errors.Is(err, ErrLocked) {
			return nil, fmt.Errorf("%w: lock was acquired concurrently", ErrLocked)
		}
		return nil, err
	}
	return &LockHandle{
		store:     s,
		caller:    caller,
		token:     token,
		expiresAt: record.ExpiresAt,
	}, nil
}

func parseLockRecord(data []byte) (lockRecord, error) {
	var record lockRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return lockRecord{}, fmt.Errorf("%w: malformed lock record", ErrIntegrity)
	}
	if record.Format != lockFormat || record.OwnerID == "" || record.Token == "" || record.ExpiresAt.IsZero() {
		return lockRecord{}, fmt.Errorf("%w: invalid lock record", ErrIntegrity)
	}
	return record, nil
}

func (s *Store) releaseLockLocked(ctx context.Context, handle *LockHandle) error {
	if handle == nil || handle.token == "" {
		return ErrLockOwnership
	}
	data, _, err := s.readArtifact(ctx, s.lockName(), ArtifactLock, "lock", 0)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.mu.Lock()
			delete(s.held, handle.token)
			s.mu.Unlock()
			return nil
		}
		return err
	}
	record, err := parseLockRecord(data)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(record.Token), []byte(handle.token)) != 1 {
		return ErrLockOwnership
	}
	if err := s.rootFS.Remove(s.lockName()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("netsec: release lock: %w", err)
	}
	s.mu.Lock()
	delete(s.held, handle.token)
	s.mu.Unlock()
	s.audit(ctx, "unlock", handle.caller, true, "lock released", nil)
	return nil
}

// Rotate creates a new key generation with a RotatingKeyProvider and
// re-encrypts the state, version, lock, and every backup under it. Old
// generations remain usable until PruneBefore or RemoveGeneration is called.
func (s *Store) Rotate(ctx context.Context, caller Caller) (uint64, error) {
	caller, err := s.authorize(ctx, caller, "rotate")
	if err != nil {
		return 0, err
	}
	rotator, ok := s.keys.(RotatingKeyProvider)
	if !ok {
		s.audit(ctx, "rotate", caller, false, "key provider cannot rotate", ErrRotationUnsupported)
		return 0, ErrRotationUnsupported
	}

	unlockRoot := lockRoot(s.root)
	defer unlockRoot()

	handle, owned, err := s.ensureWriteLock(ctx, caller)
	if err != nil {
		s.audit(ctx, "rotate", caller, false, "lock acquisition failed", err)
		return 0, err
	}
	if owned {
		defer func() {
			_ = s.releaseLockLocked(context.Background(), handle)
		}()
	}

	generation, err := rotator.Rotate(ctx)
	if err != nil {
		s.audit(ctx, "rotate", caller, false, "key provider rotation failed", err)
		return 0, fmt.Errorf("netsec: rotate key provider: %w", err)
	}
	if err := s.rotateArtifactsLocked(ctx, generation, handle); err != nil {
		s.audit(ctx, "rotate", caller, false, "artifact re-encryption failed", err)
		return 0, err
	}
	s.audit(ctx, "rotate", caller, true, fmt.Sprintf("state re-encrypted under generation %d", generation), nil)
	return generation, nil
}

// RotateKeys is an alias for Rotate.
func (s *Store) RotateKeys(ctx context.Context, caller Caller) (uint64, error) {
	return s.Rotate(ctx, caller)
}

// RotateTo re-encrypts artifacts under an existing generation supplied by an
// external KMS. The generation must already be available from the key
// provider.
func (s *Store) RotateTo(ctx context.Context, caller Caller, generation uint64) error {
	caller, err := s.authorize(ctx, caller, "rotate")
	if err != nil {
		return err
	}
	if generation == 0 {
		return fmt.Errorf("%w: generation 0 is reserved", ErrKeyGeneration)
	}
	if _, err := s.keys.Key(ctx, generation); err != nil {
		return fmt.Errorf("%w: %v", ErrKeyGeneration, err)
	}
	if current, err := s.keys.CurrentGeneration(ctx); err == nil && generation < current {
		return fmt.Errorf("%w: refusing to rotate backwards from generation %d to %d", ErrKeyGeneration, current, generation)
	}

	unlockRoot := lockRoot(s.root)
	defer unlockRoot()

	handle, owned, err := s.ensureWriteLock(ctx, caller)
	if err != nil {
		s.audit(ctx, "rotate", caller, false, "lock acquisition failed", err)
		return err
	}
	if owned {
		defer func() {
			_ = s.releaseLockLocked(context.Background(), handle)
		}()
	}
	if err := s.rotateArtifactsLocked(ctx, generation, handle); err != nil {
		s.audit(ctx, "rotate", caller, false, "artifact re-encryption failed", err)
		return err
	}
	if selector, ok := s.keys.(GenerationSelector); ok {
		if err := selector.SetCurrent(generation); err != nil {
			s.audit(ctx, "rotate", caller, false, "set active generation failed", err)
			return fmt.Errorf("%w: %v", ErrKeyGeneration, err)
		}
	}
	s.audit(ctx, "rotate", caller, true, fmt.Sprintf("state re-encrypted under generation %d", generation), nil)
	return nil
}

func (s *Store) rotateArtifactsLocked(ctx context.Context, generation uint64, handle *LockHandle) error {
	version, hasState, err := s.readVersionRecord(ctx)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if hasState {
		state, err := s.readStateFor(ctx, version)
		if err != nil {
			return fmt.Errorf("%w: cannot rotate unreadable state: %v", ErrIntegrity, err)
		}
		if err := s.writeArtifact(ctx, s.stateName(), ArtifactState, "state", version.Version, generation, state); err != nil {
			return err
		}
		rotatedVersion := version
		rotatedVersion.Generation = generation
		if err := s.writeArtifact(ctx, s.versionName(), ArtifactVersion, "version", 0, generation, mustMarshal(rotatedVersion)); err != nil {
			return err
		}
	}

	versions, err := s.listBackupVersions()
	if err != nil {
		return err
	}
	for _, backupVersion := range versions {
		backup, _, err := s.readArtifact(ctx, s.backupName(backupVersion), ArtifactBackup, s.backupRecordID(backupVersion), backupVersion)
		if err != nil {
			return fmt.Errorf("%w: cannot rotate backup %d: %v", ErrIntegrity, backupVersion, err)
		}
		if err := s.writeArtifact(ctx, s.backupName(backupVersion), ArtifactBackup, s.backupRecordID(backupVersion), backupVersion, generation, backup); err != nil {
			return err
		}
	}

	// Re-encrypt the lock last so a caller holding the lock can keep using the
	// same handle across rotation.
	lockData, _, err := s.readArtifact(ctx, s.lockName(), ArtifactLock, "lock", 0)
	if err == nil {
		record, err := parseLockRecord(lockData)
		if err != nil {
			return err
		}
		if handle != nil && subtle.ConstantTimeCompare([]byte(record.Token), []byte(handle.token)) != 1 {
			return ErrLockOwnership
		}
		if err := s.writeArtifact(ctx, s.lockName(), ArtifactLock, "lock", 0, generation, lockData); err != nil {
			return err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func mustMarshal(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

var rootLocks sync.Map

func lockRoot(root string) func() {
	value, _ := rootLocks.LoadOrStore(root, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}
