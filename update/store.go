package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Persisted state key conventions.
const (
	stateRoot      = "role:root"
	stateTimestamp = "role:timestamp"
	stateSnapshot  = "role:snapshot"

	// stateTargetsPrefix namespaces cached, verified targets metadata.
	stateTargetsPrefix = "targets:"
)

// state is the complete trusted state of a client. Every value is the
// canonical encoding of a verified metadata envelope. The whole state is one
// JSON document replaced atomically, so readers observe either the previous
// generation or the next one, never a mixture.
type state map[string][]byte

func newState() state {
	return make(state)
}

func targetsKey(targetPath string) string {
	return stateTargetsPrefix + targetPath
}

// Tx is a serializable, all-or-nothing view over the trusted state. Reads see
// the committed generation; writes are staged until Commit. Discard releases
// the transaction without persisting anything.
type Tx interface {
	Get(key string) ([]byte, bool)
	Put(key string, value []byte)
	Delete(key string)

	// Commit atomically persists every staged Put/Delete together.
	Commit(context.Context) error

	// Discard abandons the transaction.
	Discard()
}

// Store is the durable, interruptible persistence layer for trusted roots and
// derived metadata.
type Store interface {
	// Load returns the committed state. It returns ErrNoState when the
	// store has never been bootstrapped.
	Load(context.Context) (*state, error)

	// Begin opens a transaction on the currently committed generation.
	Begin(context.Context) (Tx, error)
}

// CrashPoint identifies an individual storage operation at which power loss
// can be simulated.
type CrashPoint string

const (
	// CrashBeforeWrite fires before the temp state file is created.
	CrashBeforeWrite CrashPoint = "before_write"

	// CrashAfterTempWrite fires after bytes and fsync land in the temp
	// file but before the atomic rename.
	CrashAfterTempWrite CrashPoint = "after_temp_write"

	// CrashAfterRename fires after the rename that publishes the new
	// generation but before the directory fsync completes.
	CrashAfterRename CrashPoint = "after_rename"
)

// FileStore persists trusted state as a single JSON file replaced via
// temp-file + fsync + rename + directory fsync.
type FileStore struct {
	dir      string
	fileName string
	tempName string

	// publishFn is the durability seam. The interruptible store swaps it
	// for a crash-injecting implementation without changing transaction
	// semantics.
	publishFn func(context.Context, []byte) error
}

// NewFileStore opens (or creates) a store rooted at dir.
func NewFileStore(dir string) *FileStore {
	store := &FileStore{
		dir:      dir,
		fileName: filepath.Join(dir, "trusted-state.json"),
		tempName: filepath.Join(dir, "trusted-state.json.tmp"),
	}
	store.publishFn = store.defaultPublish
	return store
}

// Load implements Store.
func (s *FileStore) Load(ctx context.Context) (*state, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.fileName)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoState
	}
	if err != nil {
		return nil, fmt.Errorf("update: read trusted state: %w", err)
	}
	loaded := newState()
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return nil, fmt.Errorf("update: corrupt trusted state: %w", err)
	}
	return &loaded, nil
}

// Begin implements Store.
func (s *FileStore) Begin(ctx context.Context) (Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	committed, err := s.Load(ctx)
	if err != nil && !errors.Is(err, ErrNoState) {
		return nil, err
	}
	if committed == nil {
		empty := newState()
		committed = &empty
	}
	return &fileTx{store: s, base: *committed, staged: newState(), deleted: map[string]struct{}{}}, nil
}

type fileTx struct {
	store   *FileStore
	base    state
	staged  state
	deleted map[string]struct{}
	done    bool
}

func (t *fileTx) Get(key string) ([]byte, bool) {
	if t.done {
		return nil, false
	}
	if _, removed := t.deleted[key]; removed {
		return nil, false
	}
	if value, ok := t.staged[key]; ok {
		return append([]byte(nil), value...), true
	}
	value, ok := t.base[key]
	return append([]byte(nil), value...), ok
}

func (t *fileTx) Put(key string, value []byte) {
	if t.done {
		return
	}
	delete(t.deleted, key)
	t.staged[key] = append([]byte(nil), value...)
}

func (t *fileTx) Delete(key string) {
	if t.done {
		return
	}
	delete(t.staged, key)
	t.deleted[key] = struct{}{}
}

func (t *fileTx) Discard() {
	t.done = true
}

func (t *fileTx) Commit(ctx context.Context) error {
	if t.done {
		return fmt.Errorf("update: transaction already finished")
	}
	t.done = true
	if err := ctx.Err(); err != nil {
		return err
	}

	next := newState()
	for key, value := range t.base {
		next[key] = value
	}
	for key := range t.deleted {
		delete(next, key)
	}
	for key, value := range t.staged {
		next[key] = value
	}

	raw, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("update: encode trusted state: %w", err)
	}
	return t.store.publishFn(ctx, raw)
}

// defaultPublish performs the durable atomic write.
func (s *FileStore) defaultPublish(ctx context.Context, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("update: create state directory: %w", err)
	}

	temp, err := os.OpenFile(s.tempName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("update: create temp state: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return fmt.Errorf("update: write temp state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("update: fsync temp state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("update: close temp state: %w", err)
	}

	if err := os.Rename(s.tempName, s.fileName); err != nil {
		return fmt.Errorf("update: publish state: %w", err)
	}
	return syncDir(s.dir)
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("update: open state directory: %w", err)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("update: fsync state directory: %w", err)
	}
	return nil
}

// InterruptibleStore wraps a FileStore and injects simulated power loss at
// precise durability points. After a crash the on-disk store is reopened;
// either the old generation is still intact (crash before rename) or the new
// generation is fully published (crash after rename), never a partial write.
type InterruptibleStore struct {
	*FileStore

	// crashAt is fired once when the named point is reached.
	crashAt CrashPoint

	// crashed records whether injection has already happened so the same
	// store instance cannot fire twice.
	crashed bool
}

// NewInterruptibleStore creates a crash-injecting store backed by dir.
func NewInterruptibleStore(dir string, crashAt CrashPoint) *InterruptibleStore {
	base := NewFileStore(dir)
	store := &InterruptibleStore{FileStore: base, crashAt: crashAt}
	base.publishFn = store.publish
	return store
}

// Disarm removes any pending crash injection, e.g. after a successful retry.
func (s *InterruptibleStore) Disarm() {
	s.crashed = true
}

// Arm enables or moves the crash point for a subsequent commit.
func (s *InterruptibleStore) Arm(point CrashPoint) {
	s.crashAt = point
	s.crashed = false
}

func (s *InterruptibleStore) publish(ctx context.Context, raw []byte) error {
	if !s.crashed && s.crashAt == CrashBeforeWrite {
		s.crashed = true
		return ErrSimulatedPowerLoss
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("update: create state directory: %w", err)
	}
	temp, err := os.OpenFile(s.tempName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("update: create temp state: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return fmt.Errorf("update: write temp state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("update: fsync temp state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("update: close temp state: %w", err)
	}

	if !s.crashed && s.crashAt == CrashAfterTempWrite {
		s.crashed = true
		// Abrupt power loss with the temp file durable but no rename:
		// the previously committed file must remain the live state.
		return ErrSimulatedPowerLoss
	}

	if err := os.Rename(s.tempName, s.fileName); err != nil {
		return fmt.Errorf("update: publish state: %w", err)
	}

	if !s.crashed && s.crashAt == CrashAfterRename {
		s.crashed = true
		// The rename has published the new generation; only the directory
		// fsync remains. On a local FS this models post-rename power loss.
		return ErrSimulatedPowerLoss
	}
	return syncDir(s.dir)
}

var (
	_ Store = (*FileStore)(nil)
	_ Store = (*InterruptibleStore)(nil)
)
