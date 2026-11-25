package admission

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrNotAdmitted is returned by Client.Admit when the publication is not yet
// backed by complete evidence or is known to be inconsistent.
var ErrNotAdmitted = errors.New("publication is not admitted")

// ErrObservedFork is returned when attempting to add a checkpoint whose size
// already has a different accepted root. Existing roots are never replaced
// merely because a new tree is larger.
var ErrObservedFork = errors.New("checkpoint forks an accepted tree")

// TrustState is the durable set of previously accepted checkpoints. Accepted
// checkpoints are retained; they are deliberately not replaced by larger
// checkpoints.
type TrustState struct {
	Checkpoints []Checkpoint `json:"checkpoints"`
}

// CheckpointSet is an alias for TrustState.
type CheckpointSet = TrustState

// Clone returns a deep copy.
func (s TrustState) Clone() TrustState {
	out := TrustState{Checkpoints: make([]Checkpoint, len(s.Checkpoints))}
	for i := range s.Checkpoints {
		out.Checkpoints[i] = s.Checkpoints[i].normalized()
	}
	return out
}

// Contains reports whether cp is already in the accepted set.
func (s TrustState) Contains(cp Checkpoint) bool {
	for _, have := range s.Checkpoints {
		if have.Equal(cp) {
			return true
		}
	}
	return false
}

// AddCheckpoint adds cp without removing any existing checkpoint. It returns
// ErrObservedFork if an existing checkpoint has the same size but a different
// root.
func (s *TrustState) AddCheckpoint(cp Checkpoint) error {
	if !cp.Valid() {
		return fmt.Errorf("checkpoint root must be %d bytes", HashSize)
	}
	if s.Contains(cp) {
		return nil
	}
	for _, have := range s.Checkpoints {
		if have.Size == cp.Size && !have.Equal(cp) {
			return fmt.Errorf("%w: size %d", ErrObservedFork, cp.Size)
		}
	}
	s.Checkpoints = append(s.Checkpoints, cp.normalized())
	return nil
}

// Store persists a trust state.
type Store interface {
	Load() (TrustState, error)
	Save(TrustState) error
}

// FileStore is an atomic JSON-backed store. Save writes a temporary file in
// the same directory, fsyncs it, and renames it over the old file. If any step
// before the rename fails, the old on-disk state remains intact.
type FileStore struct {
	Path string
}

// AtomicJSONStore is an alias for FileStore.
type AtomicJSONStore = FileStore

// NewFileStore returns an atomic JSON store at path.
func NewFileStore(path string) *FileStore { return &FileStore{Path: path} }

// NewAtomicJSONStore is an alias for NewFileStore.
func NewAtomicJSONStore(path string) *FileStore { return NewFileStore(path) }

func (s *FileStore) Load() (TrustState, error) {
	if s == nil || s.Path == "" {
		return TrustState{}, nil
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return TrustState{}, nil
	}
	if err != nil {
		return TrustState{}, err
	}
	var state TrustState
	if len(data) == 0 {
		return TrustState{}, nil
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return TrustState{}, fmt.Errorf("decode trust state: %w", err)
	}
	return state, nil
}

func (s *FileStore) Save(state TrustState) error {
	if s == nil || s.Path == "" {
		return errors.New("trust store has no path")
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode trust state: %w", err)
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".admission-trust-*.tmp")
	if err != nil {
		return err
	}
	tempName := f.Name()
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(tempName)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, s.Path); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

// MemoryStore is a concurrency-safe in-memory store, useful for tests.
type MemoryStore struct {
	mu    sync.Mutex
	state TrustState
}

func (s *MemoryStore) Load() (TrustState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone(), nil
}

func (s *MemoryStore) Save(state TrustState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state.Clone()
	return nil
}

// Client is a persistent admission client. State changes are committed to the
// store before they become visible in memory, so a failed disk commit leaves
// the previous trust state in place.
type Client struct {
	mu     sync.Mutex
	store  Store
	policy Policy
	state  TrustState
}

// NewClient opens a client. The first argument may be a Store, a filesystem
// path, or nil (for a fresh in-memory host). The optional second argument is a
// Policy, an integer threshold, or both.
func NewClient(storeOrPath any, policyOrThreshold ...any) (*Client, error) {
	var store Store
	switch v := storeOrPath.(type) {
	case Store:
		store = v
	case string:
		if v == "" {
			store = &MemoryStore{}
		} else {
			store = NewFileStore(v)
		}
	case *FileStore:
		store = v
	case nil:
		store = &MemoryStore{}
	default:
		return nil, fmt.Errorf("unsupported store type %T", storeOrPath)
	}

	policy := Policy{Threshold: 1}
	if len(policyOrThreshold) != 0 {
		policy = parsePolicyArgs(policyOrThreshold...)
	}
	state, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &Client{store: store, policy: policy, state: state}, nil
}

// NewFileClient opens an atomic file-backed client.
func NewFileClient(path string, policyOrThreshold ...any) (*Client, error) {
	return NewClient(path, policyOrThreshold...)
}

// OpenClient opens a client from a Store without special-casing paths.
func OpenClient(store Store, policyOrThreshold ...any) (*Client, error) {
	return NewClient(store, policyOrThreshold...)
}

// Policy returns the client's witness policy.
func (c *Client) Policy() Policy {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.policy
}

// State returns a deep copy of the last successfully committed trust state.
func (c *Client) State() TrustState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Clone()
}

// Checkpoints returns the accepted checkpoint set.
func (c *Client) Checkpoints() []Checkpoint { return c.State().Checkpoints }

// Evaluate checks a publication against the currently committed state.
func (c *Client) Evaluate(publication Publication, edges []EvidenceEdge, args ...any) Result {
	c.mu.Lock()
	state := c.state.Clone()
	policy := c.policy
	c.mu.Unlock()
	e := Evaluator{Anchors: state.Checkpoints, Policy: policy}
	return e.Evaluate(publication, edges, args...)
}

// EvaluateAll checks publications against the current state.
func (c *Client) EvaluateAll(publications []Publication, edges []EvidenceEdge, args ...any) []Result {
	c.mu.Lock()
	state := c.state.Clone()
	policy := c.policy
	c.mu.Unlock()
	e := Evaluator{Anchors: state.Checkpoints, Policy: policy}
	return e.EvaluateAll(publications, edges, args...)
}

// Admit evaluates a publication and, if admitted, atomically persists the new
// checkpoint. It never removes existing checkpoints. On any persistence error
// c.State() remains the old state.
func (c *Client) Admit(publication Publication, edges []EvidenceEdge, args ...any) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := Evaluator{Anchors: c.state.Checkpoints, Policy: c.policy}
	result := e.Evaluate(publication, edges)
	if result.Status != StatusAdmitted {
		return result, ErrNotAdmitted
	}

	target := publication.checkpoint().normalized()
	next := c.state.Clone()
	if err := next.AddCheckpoint(target); err != nil {
		return result, err
	}
	if err := c.store.Save(next); err != nil {
		return result, fmt.Errorf("commit trust state: %w", err)
	}
	c.state = next
	return result, nil
}

// CommitCheckpoint imports a checkpoint that was established through another
// trusted path. It persists before updating in-memory state and refuses to
// replace a conflicting root at the same size.
func (c *Client) CommitCheckpoint(cp Checkpoint) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.state.Clone()
	if err := next.AddCheckpoint(cp); err != nil {
		return err
	}
	if err := c.store.Save(next); err != nil {
		return fmt.Errorf("commit trust state: %w", err)
	}
	c.state = next
	return nil
}

// Reload replaces the in-memory state with the last durable state.
func (c *Client) Reload() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.store.Load()
	if err != nil {
		return err
	}
	c.state = state
	return nil
}
