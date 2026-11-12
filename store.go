package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Store is the versioned object service. The zero value is not usable; create
// one with Open.
type Store struct {
	root string

	// mu serializes Commit so that version allocation and the visibility
	// switch are transactional. Readers never take this lock.
	mu sync.Mutex

	// approvals holds the single outstanding, single-use approval per object.
	// Approvals are process-local policy tokens; committed objects, digests
	// and labels are durable on disk.
	approvals map[string]DowngradeApproval

	// Test-only transaction hooks.
	failAt failStage
	gate   *publishGate
	hookMu sync.Mutex
}

// Open opens (creating if needed) a store rooted at root.
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "objects"), 0o755); err != nil {
		return nil, fmt.Errorf("netsec: initialize store: %w", err)
	}
	return &Store{root: root, approvals: make(map[string]DowngradeApproval)}, nil
}

// Root returns the on-disk root of the store.
func (s *Store) Root() string { return s.root }

func validateName(object string) error {
	if object == "" || object == "." || object == ".." ||
		strings.ContainsAny(object, `/\`) || strings.ContainsRune(object, 0) {
		return fmt.Errorf("netsec: invalid object name %q", object)
	}
	return nil
}

func (s *Store) objectRoot(object string) string {
	return filepath.Join(s.root, "objects", object)
}

func (s *Store) currentPath(object string) string {
	return filepath.Join(s.objectRoot(object), "current")
}

func (s *Store) versionDir(object string, version int64) string {
	return filepath.Join(s.objectRoot(object), "versions", "v"+strconv.FormatInt(version, 10))
}

// hook runs the one-shot failure injection armed for testing.
func (s *Store) hook(stage failStage) error {
	s.hookMu.Lock()
	shouldFail := s.failAt == stage
	if shouldFail {
		s.failAt = failStageNone
	}
	s.hookMu.Unlock()

	if shouldFail {
		return errInjectedFailure
	}
	return nil
}

// setFailAt arms a one-shot injected failure at the given stage (test support).
func (s *Store) setFailAt(stage failStage) {
	s.hookMu.Lock()
	s.failAt = stage
	s.hookMu.Unlock()
}

// setPublishGate installs a gate that parks each Commit before it takes the
// write lock (test support). This widens the window in which concurrent
// readers can only observe the complete previous version.
func (s *Store) setPublishGate(gate *publishGate) {
	s.hookMu.Lock()
	s.gate = gate
	s.hookMu.Unlock()
}

// publishGate is a test helper implementing a count-based barrier. Each parked
// Commit calls wait() once (registering an arrival); the test waits for an
// expected number of arrivals via WaitFor, releases every waiter with Allow,
// then arms a fresh barrier with Reset.
type publishGate struct {
	mu      sync.Mutex
	arrived int
	allow   chan struct{}
	changed chan struct{}
}

func newPublishGate() *publishGate {
	return &publishGate{
		allow:   make(chan struct{}),
		changed: make(chan struct{}),
	}
}

func (g *publishGate) wait() {
	g.mu.Lock()
	g.arrived++
	if g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
	allow := g.allow
	g.mu.Unlock()
	<-allow
}

// WaitFor blocks until want commits have arrived at the current barrier.
func (g *publishGate) WaitFor(want int) {
	for {
		g.mu.Lock()
		if g.arrived >= want {
			g.mu.Unlock()
			return
		}
		ch := g.changed
		if ch == nil {
			ch = make(chan struct{})
			g.changed = ch
		}
		g.mu.Unlock()
		<-ch
	}
}

// Allow releases every currently parked Commit.
func (g *publishGate) Allow() {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.allow)
}

// Reset prepares the gate for the next Commit.
func (g *publishGate) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.arrived = 0
	g.allow = make(chan struct{})
	g.changed = make(chan struct{})
}

// atomicWriteFile writes data to path via a same-directory temp file and an
// atomic rename, so readers never see a partially written file.
func atomicWriteFile(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.RemoveAll(tmpName)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func copyLabel(in *Label) *Label {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// GrantDowngradeApproval registers a single-use approval. It is consumed by the
// next matching Commit and must name the exact current version and levels.
func (s *Store) GrantDowngradeApproval(approval DowngradeApproval) error {
	if err := validateName(approval.Object); err != nil {
		return err
	}
	if approval.FromVersion < 1 {
		return fmt.Errorf("netsec: approval FromVersion must be positive")
	}
	if approval.ToLevel >= approval.FromLevel {
		return fmt.Errorf("netsec: approval must describe a strict downgrade")
	}
	if approval.Approver == "" {
		return fmt.Errorf("netsec: approval Approver is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals[approval.Object] = approval
	return nil
}

// CurrentVersion returns the version number the current pointer resolves to.
func (s *Store) CurrentVersion(object string) (int64, error) {
	if err := validateName(object); err != nil {
		return 0, err
	}
	return s.currentVersion(object)
}

// Commit publishes a new immutable version. The content digest and the label
// become visible together: either the complete new version is current or the
// previous version stays current. A nil label is rejected, and a lower label
// level requires a matching caller-issued DowngradeApproval.
func (s *Store) Commit(ctx context.Context, in CommitInput) (handle *Handle, err error) {
	if err = validateName(in.Object); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if in.Label == nil {
		return nil, ErrLabelRequired
	}

	// Test-only gate. Parked before taking the write lock so readers and the
	// test's handshake are never blocked by a held lock.
	s.hookMu.Lock()
	gate := s.gate
	s.hookMu.Unlock()
	if gate != nil {
		gate.wait()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, err := s.readCurrentManifest(in.Object)
	if err != nil && !errors.Is(err, ErrVersionNotFound) {
		return nil, err
	}

	nextVersion := int64(1)
	if current != nil {
		nextVersion = current.Version + 1
		// A downgrade is only defined against a version that has a label.
		if current.Label != nil && in.Label.Level < current.Label.Level {
			approval, ok := s.approvals[in.Object]
			if !ok {
				return nil, ErrDowngradeApproval
			}
			if !approval.matches(in.Object, current.Version, current.Label.Level, in.Label.Level) {
				return nil, ErrApprovalRejected
			}
			// Consume the single-use approval only once it is fully validated.
			delete(s.approvals, in.Object)
		}
	}

	objectRoot := s.objectRoot(in.Object)
	stagingRoot := filepath.Join(objectRoot, "staging")
	if err = os.MkdirAll(stagingRoot, 0o755); err != nil {
		return nil, err
	}
	stagingDir, err := os.MkdirTemp(stagingRoot, "tx-*")
	if err != nil {
		return nil, err
	}
	targetDir := s.versionDir(in.Object, nextVersion)
	published := false

	// On any failure, remove partial state. A staged (not yet renamed)
	// transaction is invisible; an unpublished version dir is removed so the
	// current pointer is the sole source of visibility.
	defer func() {
		if err == nil {
			return
		}
		if published {
			_ = os.RemoveAll(targetDir)
		} else {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	contentPath := filepath.Join(stagingDir, "content.bin")
	if err = s.hook(failStageContent); err != nil {
		return nil, err
	}
	if err = atomicWriteFile(contentPath, in.Content, 0o600); err != nil {
		return nil, err
	}

	record := manifest{
		Version:   nextVersion,
		Object:    in.Object,
		Digest:    sha256Hex(in.Content),
		Label:     copyLabel(in.Label),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	manifestBytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = s.hook(failStageManifest); err != nil {
		return nil, err
	}
	if err = atomicWriteFile(filepath.Join(stagingDir, "manifest.json"), manifestBytes, 0o600); err != nil {
		return nil, err
	}

	// Single atomic rename makes the complete version directory visible.
	if err = os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return nil, err
	}
	if err = s.hook(failStagePublish); err != nil {
		return nil, err
	}
	if err = os.Rename(stagingDir, targetDir); err != nil {
		return nil, err
	}
	published = true

	// Atomically move the current pointer; until this succeeds readers keep
	// observing the complete previous version.
	if err = s.hook(failStageCurrent); err != nil {
		return nil, err
	}
	pointer := []byte("v" + strconv.FormatInt(nextVersion, 10))
	if err = atomicWriteFile(s.currentPath(in.Object), pointer, 0o600); err != nil {
		return nil, err
	}

	return s.openVersion(in.Object, nextVersion)
}
