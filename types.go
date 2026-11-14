package netsec

import (
	"fmt"
	"math"
)

const (
	// TypeCacheEntry is the wire identifier for CacheEntry version 1.
	TypeCacheEntry TypeID = "cache.entry"
	// TypeCacheMetadata is the wire identifier for CacheMetadata version 1.
	TypeCacheMetadata TypeID = "cache.metadata"

	// CacheEntryVersion is the only accepted CacheEntry version.
	CacheEntryVersion uint32 = 1
	// CacheMetadataVersion is the only accepted CacheMetadata version.
	CacheMetadataVersion uint32 = 1

	// MaxCacheTTLSeconds is the maximum allowed created/expires interval.
	MaxCacheTTLSeconds int64 = 30 * 24 * 60 * 60

	maxCacheKeyLength     = 256
	maxCacheValueBytes    = 1 << 20
	maxCacheTags          = 32
	maxCacheTagLength     = 64
	maxCacheNamespaceSize = 128
	maxCacheOwnerSize     = 128
)

// CacheMetadata is the allowlisted nested metadata object used by
// CacheEntry. It is only constructed through the registry decoder.
type CacheMetadata struct {
	Namespace string `json:"namespace"`
	Owner     string `json:"owner"`
	Revision  uint64 `json:"revision"`
}

// CacheEntry is a safe sample cache value demonstrating the decoder API.
// Services may define their own structs and register them with RegisterType.
type CacheEntry struct {
	Key       string         `json:"key"`
	Value     string         `json:"value"`
	CreatedAt int64          `json:"created_at"`
	ExpiresAt int64          `json:"expires_at"`
	Metadata  *CacheMetadata `json:"metadata"`
	Tags      []string       `json:"tags"`
}

// NewDefaultRegistry returns a registry containing the built-in cache types.
// It is a convenience for services that do not need custom record types.
func NewDefaultRegistry(opts ...Option) *Registry {
	r := NewRegistry(opts...)
	if err := RegisterType[CacheMetadata](
		r,
		TypeCacheMetadata,
		CacheMetadataVersion,
		[]FieldSpec{
			{Name: "namespace", Required: true, LenRange: &LenRange{Min: 1, Max: maxCacheNamespaceSize}},
			{Name: "owner", LenRange: &LenRange{Min: 0, Max: maxCacheOwnerSize}},
			{Name: "revision", Required: true, IntRange: &IntRange{Min: 1, Max: math.MaxInt64}},
		},
		validateCacheMetadata,
	); err != nil {
		panic(fmt.Sprintf("netsec: invalid built-in metadata type: %v", err))
	}
	if err := RegisterType[CacheEntry](
		r,
		TypeCacheEntry,
		CacheEntryVersion,
		[]FieldSpec{
			{Name: "key", Required: true, LenRange: &LenRange{Min: 1, Max: maxCacheKeyLength}},
			{Name: "value", Required: true, LenRange: &LenRange{Min: 0, Max: maxCacheValueBytes}},
			{Name: "created_at", Required: true, IntRange: &IntRange{Min: 1, Max: math.MaxInt64}},
			{Name: "expires_at", Required: true, IntRange: &IntRange{Min: 1, Max: math.MaxInt64}},
			{Name: "metadata", Required: true},
			{Name: "tags", LenRange: &LenRange{Min: 0, Max: maxCacheTags}},
		},
		validateCacheEntry,
	); err != nil {
		panic(fmt.Sprintf("netsec: invalid built-in entry type: %v", err))
	}
	return r
}

func validateCacheMetadata(metadata *CacheMetadata) error {
	if metadata.Namespace == "" {
		return fmt.Errorf("namespace must not be empty")
	}
	if hasControl(metadata.Namespace) {
		return fmt.Errorf("namespace contains control characters")
	}
	if hasControl(metadata.Owner) {
		return fmt.Errorf("owner contains control characters")
	}
	if metadata.Revision == 0 {
		return fmt.Errorf("revision must be positive")
	}
	return nil
}

func validateCacheEntry(entry *CacheEntry) error {
	if entry.Key == "" {
		return fmt.Errorf("key must not be empty")
	}
	if hasControl(entry.Key) {
		return fmt.Errorf("key contains control characters")
	}
	if entry.ExpiresAt <= entry.CreatedAt {
		return fmt.Errorf("expires_at must be greater than created_at")
	}
	if entry.ExpiresAt-entry.CreatedAt > MaxCacheTTLSeconds {
		return fmt.Errorf("ttl exceeds %d seconds", MaxCacheTTLSeconds)
	}
	if entry.Metadata == nil {
		return fmt.Errorf("metadata must not be nil")
	}
	seen := make(map[string]struct{}, len(entry.Tags))
	for _, tag := range entry.Tags {
		if tag == "" || len(tag) > maxCacheTagLength {
			return fmt.Errorf("tag length must be in [1,%d]", maxCacheTagLength)
		}
		if hasControl(tag) {
			return fmt.Errorf("tag contains control characters")
		}
		if _, duplicate := seen[tag]; duplicate {
			return fmt.Errorf("duplicate tag %q", tag)
		}
		seen[tag] = struct{}{}
	}
	return nil
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
