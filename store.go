package canonicalkey

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"sync"
	"time"
)

type CatalogEntry struct {
	Canonical []byte
	Key       string
	Sequence  uint64
	CreatedAt time.Time
}

type Catalog interface {
	GetOrInsert(ctx context.Context, canonical []byte, now time.Time) (CatalogEntry, bool, error)
}

type HashFunc func() hash.Hash

func DefaultHash() hash.Hash {
	return sha256.New()
}

type MemoryCatalog struct {
	mu       sync.Mutex
	hash     HashFunc
	entries  map[string][]CatalogEntry
	sequence uint64
	closed   bool
}

func NewMemoryCatalog(hash HashFunc) *MemoryCatalog {
	if hash == nil {
		hash = DefaultHash
	}
	return &MemoryCatalog{
		hash:    hash,
		entries: make(map[string][]CatalogEntry),
	}
}

func (catalog *MemoryCatalog) GetOrInsert(ctx context.Context, canonical []byte, now time.Time) (CatalogEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return CatalogEntry{}, false, err
	}
	digest := catalog.digest(canonical)
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.closed {
		return CatalogEntry{}, false, ErrCatalogClosed
	}
	for _, entry := range catalog.entries[digest] {
		if string(entry.Canonical) == string(canonical) {
			return entry, false, nil
		}
	}
	catalog.sequence++
	entry := CatalogEntry{
		Canonical: append([]byte(nil), canonical...),
		Key:       formatKey(digest, catalog.sequence),
		Sequence:  catalog.sequence,
		CreatedAt: now,
	}
	catalog.entries[digest] = append(catalog.entries[digest], entry)
	return entry, true, nil
}

func (catalog *MemoryCatalog) Close() error {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	catalog.closed = true
	return nil
}

func (catalog *MemoryCatalog) digest(canonical []byte) string {
	hasher := catalog.hash()
	hasher.Write(canonical)
	return hex.EncodeToString(hasher.Sum(nil))
}

func formatKey(digest string, sequence uint64) string {
	return fmt.Sprintf("ck:%s:%020d", digest, sequence)
}

func DigestKey(canonical []byte, hash HashFunc) string {
	if hash == nil {
		hash = DefaultHash
	}
	hasher := hash()
	hasher.Write(canonical)
	return fmt.Sprintf("ck:%s", hex.EncodeToString(hasher.Sum(nil)))
}
