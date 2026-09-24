package cache

import (
	"context"
	"errors"
	"sync"
)

// ErrMiss is returned by a Source when the key does not exist.
var ErrMiss = errors.New("cache: key not found in source")

// Source is the authoritative data source. It is injected into the Cache,
// so tests and demos can substitute any implementation (no remote cache
// dependency is required anywhere in this package).
type Source interface {
	// Load returns the current value and its monotonic version for key.
	Load(ctx context.Context, key string) (value string, version uint64, err error)
}

// SourceFunc adapts a plain function to Source.
type SourceFunc func(ctx context.Context, key string) (string, uint64, error)

// Load implements Source.
func (f SourceFunc) Load(ctx context.Context, key string) (string, uint64, error) {
	return f(ctx, key)
}

// WritableSource is a Source that also accepts writes, assigning a new
// monotonic version per key on every Store.
type WritableSource interface {
	Source
	Store(ctx context.Context, key, value string) (version uint64, err error)
}

// MapSource is an in-memory Source/WritableSource with per-key monotonic
// versions and injectable load failures. It is used by the demo and tests.
type MapSource struct {
	mu      sync.Mutex
	data    map[string]string
	version map[string]uint64
	loads   int
	// LoadErr, when non-nil, is consulted on every Load; returning a
	// non-nil error simulates a source failure.
	LoadErr func(key string) error
}

// NewMapSource returns an empty in-memory source.
func NewMapSource() *MapSource {
	return &MapSource{data: map[string]string{}, version: map[string]uint64{}}
}

// Load implements Source.
func (s *MapSource) Load(_ context.Context, key string) (string, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	if s.LoadErr != nil {
		if err := s.LoadErr(key); err != nil {
			return "", 0, err
		}
	}
	v, ok := s.data[key]
	if !ok {
		return "", 0, ErrMiss
	}
	return v, s.version[key], nil
}

// Store implements WritableSource, bumping the per-key version.
func (s *MapSource) Store(_ context.Context, key, value string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.version[key]++
	s.data[key] = value
	return s.version[key], nil
}

// Loads reports how many Load calls the source has served.
func (s *MapSource) Loads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads
}
