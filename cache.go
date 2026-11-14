package netsec

import (
	"context"
	"fmt"
	"sync"
)

// CacheKey is physically partitioned by SourceID before module path and
// version. A cache entry for the public source can never be returned for a
// private-source lookup.
type CacheKey struct {
	SourceID   string `json:"source_id"`
	ModulePath string `json:"module_path"`
	Version    string `json:"version"`
}

func (k CacheKey) validate() error {
	if err := validateIdentifier("cache source ID", k.SourceID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCacheKey, err)
	}
	if err := validateModulePath(k.ModulePath); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCacheKey, err)
	}
	if !goVersionPattern.MatchString(k.Version) {
		return fmt.Errorf("%w: version %q", ErrInvalidCacheKey, k.Version)
	}
	return nil
}

// Cache is a source-isolated artifact cache. Implementations must treat
// CacheKey.SourceID as part of the key; Resolver also re-verifies every cache
// hit.
type Cache interface {
	Get(ctx context.Context, key CacheKey) (artifact Artifact, hit bool, err error)
	Put(ctx context.Context, key CacheKey, artifact Artifact) error
}

// MemoryCache is an in-memory, source-partitioned Cache implementation.
type MemoryCache struct {
	mu      sync.RWMutex
	sources map[string]map[moduleVersion]Artifact
}

type moduleVersion struct {
	modulePath string
	version    string
}

// NewMemoryCache creates an empty source-isolated cache.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{sources: make(map[string]map[moduleVersion]Artifact)}
}

// Get returns a defensive copy of the artifact cached under key.
func (c *MemoryCache) Get(_ context.Context, key CacheKey) (Artifact, bool, error) {
	if err := key.validate(); err != nil {
		return Artifact{}, false, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	partition := c.sources[key.SourceID]
	if partition == nil {
		return Artifact{}, false, nil
	}
	artifact, ok := partition[moduleVersion{modulePath: key.ModulePath, version: key.Version}]
	if !ok {
		return Artifact{}, false, nil
	}
	return cloneArtifact(artifact), true, nil
}

// Put stores a defensive copy of the artifact under key.
func (c *MemoryCache) Put(_ context.Context, key CacheKey, artifact Artifact) error {
	if err := key.validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	partition := c.sources[key.SourceID]
	if partition == nil {
		partition = make(map[moduleVersion]Artifact)
		c.sources[key.SourceID] = partition
	}
	partition[moduleVersion{modulePath: key.ModulePath, version: key.Version}] = cloneArtifact(artifact)
	return nil
}

// SourceLen reports the number of entries in one source partition. It is
// useful for asserting physical cache isolation.
func (c *MemoryCache) SourceLen(sourceID string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.sources[sourceID])
}

func cloneArtifact(artifact Artifact) Artifact {
	clone := artifact
	if artifact.Content != nil {
		clone.Content = append([]byte(nil), artifact.Content...)
	}
	if artifact.Signature != nil {
		signature := *artifact.Signature
		signature.Value = append([]byte(nil), artifact.Signature.Value...)
		clone.Signature = &signature
	}
	if artifact.Metadata != nil {
		clone.Metadata = make(map[string]string, len(artifact.Metadata))
		for key, value := range artifact.Metadata {
			clone.Metadata[key] = value
		}
	}
	return clone
}
