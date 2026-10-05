// Package dedup implements a content-addressed block store with
// deduplication, reference counting and transactional commits.
//
// Blocks are bucketed by a weak digest (fast, collision-prone). Equality
// is confirmed with a strong digest plus the block length and, as the
// final arbiter, a full content comparison. If a fault produces two
// different contents with the same strong digest, both copies are kept
// under distinct variants and a Conflict is recorded; data is never
// silently overwritten.
package dedup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/adler32"
	"sync"
)

// ErrUnknownRoot is returned when operating on an object root that has
// not been committed (or has already been released).
var ErrUnknownRoot = errors.New("dedup: unknown object root")

// StrongHash computes the strong digest of a block's content.
type StrongHash func(content []byte) string

// WeakHash computes the bucket digest of a block's content.
type WeakHash func(content []byte) uint32

// SHA256 is the default strong hash.
func SHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Adler32 is the default weak (bucket) hash.
func Adler32(content []byte) uint32 {
	return adler32.Checksum(content)
}

func blockKey(strong string, variant int) string {
	return fmt.Sprintf("%s#%d", strong, variant)
}

// Conflict records an isolation event: an incoming block shared a strong
// digest with existing data but its content differed, so it was stored
// under a new variant key instead of overwriting.
type Conflict struct {
	Strong string // shared strong digest
	Key    string // variant key the incoming content was isolated under
	Length int    // length of the incoming content
}

type blockMeta struct {
	key     string
	weak    uint32
	strong  string
	variant int
	length  int
	refs    int
}

// Object is a named set of block contents identified by its root.
type Object struct {
	Root   string
	Blocks [][]byte
}

// CommitResult describes the outcome of a successful Commit.
type CommitResult struct {
	Root      string
	Keys      []string // canonical block keys, one per input block
	NewBlocks int      // blocks physically added by this commit
	Duplicate bool     // true if this txID had already been committed
}

// Store is a concurrency-safe deduplicating block store.
type Store struct {
	mu        sync.Mutex
	strong    StrongHash
	weak      WeakHash
	buckets   map[uint32][]*blockMeta // weak digest -> candidates (fast path)
	byStrong  map[string][]*blockMeta // strong digest -> variants (authoritative)
	blocks    map[string]*blockMeta   // canonical key -> meta
	data      map[string][]byte       // canonical key -> content
	objects   map[string][]string     // object root -> block keys
	commits   map[string]CommitResult // txID -> result (idempotency)
	conflicts []Conflict

	// commitFailHook, when set, is invoked before each commit step; a
	// non-nil return aborts the commit and rolls it back. Test hook for
	// fault injection.
	commitFailHook func(step int) error
}

// Option configures a Store.
type Option func(*Store)

// WithStrongHash overrides the strong hash (fault injection, tests).
func WithStrongHash(h StrongHash) Option {
	return func(s *Store) { s.strong = h }
}

// WithWeakHash overrides the weak bucket hash (fault injection, tests).
func WithWeakHash(h WeakHash) Option {
	return func(s *Store) { s.weak = h }
}

// New creates an empty Store.
func New(opts ...Option) *Store {
	s := &Store{
		strong:   SHA256,
		weak:     Adler32,
		buckets:  make(map[uint32][]*blockMeta),
		byStrong: make(map[string][]*blockMeta),
		blocks:   make(map[string]*blockMeta),
		data:     make(map[string][]byte),
		objects:  make(map[string][]string),
		commits:  make(map[string]CommitResult),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SetCommitFailHook installs a hook invoked before each commit step.
// A non-nil error aborts the commit and triggers rollback. Pass nil to
// clear. Intended for tests and fault injection.
func (s *Store) SetCommitFailHook(hook func(step int) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitFailHook = hook
}

// putLocked inserts content, returning its canonical key. Deduplicates
// on (weak bucket, strong digest, length, full content). On a strong
// digest match with differing content the incoming copy is isolated
// under a new variant and a Conflict is recorded; existing data is
// never overwritten.
func (s *Store) putLocked(content []byte) (key string, created bool) {
	weak := s.weak(content)
	strong := s.strong(content)
	// Fast path: confirm equality inside the weak bucket.
	for _, m := range s.buckets[weak] {
		if m.strong == strong && m.length == len(content) && bytes.Equal(s.data[m.key], content) {
			return m.key, false
		}
	}
	// Authoritative path: even if a weak-hash fault scattered equal
	// content across buckets, variants of the same strong digest are
	// compared before a new key is minted.
	variants := s.byStrong[strong]
	for _, m := range variants {
		if m.length == len(content) && bytes.Equal(s.data[m.key], content) {
			return m.key, false
		}
	}
	variant := len(variants)
	key = blockKey(strong, variant)
	if variant > 0 {
		s.conflicts = append(s.conflicts, Conflict{Strong: strong, Key: key, Length: len(content)})
	}
	m := &blockMeta{key: key, weak: weak, strong: strong, variant: variant, length: len(content)}
	s.buckets[weak] = append(s.buckets[weak], m)
	s.byStrong[strong] = append(s.byStrong[strong], m)
	s.blocks[key] = m
	s.data[key] = append([]byte(nil), content...)
	return key, true
}

func (s *Store) removeBlockLocked(key string) {
	m, ok := s.blocks[key]
	if !ok {
		return
	}
	delete(s.blocks, key)
	delete(s.data, key)
	s.buckets[m.weak] = removeMeta(s.buckets[m.weak], key)
	if len(s.buckets[m.weak]) == 0 {
		delete(s.buckets, m.weak)
	}
	s.byStrong[m.strong] = removeMeta(s.byStrong[m.strong], key)
	if len(s.byStrong[m.strong]) == 0 {
		delete(s.byStrong, m.strong)
	}
}

func removeMeta(list []*blockMeta, key string) []*blockMeta {
	for i, m := range list {
		if m.key == key {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// Commit atomically stores obj's blocks and registers the object root.
// Reference counts are incremented per block; if any step fails the
// whole transaction is rolled back. Committing the same txID twice is
// idempotent: the original result is returned with Duplicate set and no
// state changes.
func (s *Store) Commit(txID string, obj Object) (CommitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.commits[txID]; ok {
		prev.Duplicate = true
		return prev, nil
	}
	if obj.Root == "" {
		return CommitResult{}, errors.New("dedup: empty object root")
	}
	if _, exists := s.objects[obj.Root]; exists {
		return CommitResult{}, fmt.Errorf("dedup: object root %q already committed", obj.Root)
	}
	type appliedOp struct {
		key     string
		created bool
	}
	var ops []appliedOp
	rollback := func() {
		for i := len(ops) - 1; i >= 0; i-- {
			op := ops[i]
			m := s.blocks[op.key]
			if m == nil {
				continue
			}
			m.refs--
			if op.created && m.refs == 0 {
				s.removeBlockLocked(op.key)
			}
		}
	}
	keys := make([]string, 0, len(obj.Blocks))
	newBlocks := 0
	for step, content := range obj.Blocks {
		if s.commitFailHook != nil {
			if err := s.commitFailHook(step); err != nil {
				rollback()
				return CommitResult{}, fmt.Errorf("dedup: commit %s aborted at step %d: %v", txID, step, err)
			}
		}
		key, created := s.putLocked(content)
		s.blocks[key].refs++
		if created {
			newBlocks++
		}
		ops = append(ops, appliedOp{key: key, created: created})
		keys = append(keys, key)
	}
	s.objects[obj.Root] = keys
	res := CommitResult{Root: obj.Root, Keys: keys, NewBlocks: newBlocks}
	s.commits[txID] = res
	return res, nil
}

// Release detaches an object root and decrements its blocks' reference
// counts. It refuses to drive any count negative: the whole release is
// validated before any count is touched.
func (s *Store) Release(root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, ok := s.objects[root]
	if !ok {
		return ErrUnknownRoot
	}
	for _, key := range keys {
		m := s.blocks[key]
		if m == nil || m.refs <= 0 {
			return fmt.Errorf("dedup: release %q would drive block %s refcount negative", root, key)
		}
	}
	for _, key := range keys {
		s.blocks[key].refs--
	}
	delete(s.objects, root)
	return nil
}

// Sweep deletes every block with a zero reference count and returns
// their keys. It holds the store lock, so a concurrent Commit either
// resurrects the content (re-put) or raises the count before Sweep
// observes it.
func (s *Store) Sweep() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var collected []string
	for key, m := range s.blocks {
		if m.refs == 0 {
			s.removeBlockLocked(key)
			collected = append(collected, key)
		}
	}
	return collected
}

// RecountReport summarizes a Recount pass.
type RecountReport struct {
	Adjusted map[string][2]int   // block key -> {old, new} for corrected counts
	Missing  map[string][]string // object root -> referenced but absent block keys
}

// Recount independently recomputes every block's reference count from
// the committed object roots and repairs any drift. Blocks referenced by
// an object but absent from the store are reported in Missing.
func (s *Store) Recount() RecountReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := make(map[string]int)
	missing := make(map[string][]string)
	for root, keys := range s.objects {
		for _, key := range keys {
			if s.blocks[key] == nil {
				missing[root] = append(missing[root], key)
				continue
			}
			want[key]++
		}
	}
	adjusted := make(map[string][2]int)
	for key, m := range s.blocks {
		if m.refs != want[key] {
			adjusted[key] = [2]int{m.refs, want[key]}
			m.refs = want[key]
		}
	}
	return RecountReport{Adjusted: adjusted, Missing: missing}
}

// Get returns the block contents of a committed object, in order.
func (s *Store) Get(root string) ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys, ok := s.objects[root]
	if !ok {
		return nil, ErrUnknownRoot
	}
	out := make([][]byte, 0, len(keys))
	for _, key := range keys {
		content, ok := s.data[key]
		if !ok {
			return nil, fmt.Errorf("dedup: block %s missing for root %q", key, root)
		}
		out = append(out, append([]byte(nil), content...))
	}
	return out, nil
}

// Refs returns the current reference count of a block key.
func (s *Store) Refs(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.blocks[key]; ok {
		return m.refs
	}
	return 0
}

// BlockCount returns the number of physically stored blocks.
func (s *Store) BlockCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.blocks)
}

// Conflicts returns a copy of all recorded isolation events.
func (s *Store) Conflicts() []Conflict {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Conflict(nil), s.conflicts...)
}
