package readproof

import (
	"errors"
	"fmt"
	"sync"
)

// NodeCache stores verified sibling commitments keyed by their structural
// interval. Every entry records the root and version under which it was
// verified, so a cached sibling can never be replayed against a different
// (for example newer) handle.
type NodeCache interface {
	Get(root [32]byte, start, end uint64) (CachedNode, bool)
	Put(root [32]byte, node CachedNode)
}

// CachedNode is a generation-tagged cache entry.
type CachedNode struct {
	Start   uint64
	End     uint64
	Version uint64
	Hash    [32]byte
}

type nodeKey struct {
	start uint64
	end   uint64
}

// MemoryNodeCache is an in-memory NodeCache. A single cache shared across
// versions deliberately keeps entries from old generations; retrieval is
// root-scoped so mismatched generations are rejected.
type MemoryNodeCache struct {
	mu      sync.RWMutex
	entries map[[32]byte]map[nodeKey]CachedNode
}

// NewMemoryNodeCache creates an empty cache.
func NewMemoryNodeCache() *MemoryNodeCache {
	return &MemoryNodeCache{entries: make(map[[32]byte]map[nodeKey]CachedNode)}
}

// Get returns the entry for interval [start,end) only if it was verified
// under root.
func (c *MemoryNodeCache) Get(root [32]byte, start, end uint64) (CachedNode, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	bucket, ok := c.entries[root]
	if !ok {
		return CachedNode{}, false
	}
	node, ok := bucket[nodeKey{start, end}]
	return node, ok
}

// Put stores a generation-tagged entry under its verifying root.
func (c *MemoryNodeCache) Put(root [32]byte, node CachedNode) {
	c.mu.Lock()
	defer c.mu.Unlock()
	bucket, ok := c.entries[root]
	if !ok {
		bucket = make(map[nodeKey]CachedNode)
		c.entries[root] = bucket
	}
	bucket[nodeKey{node.Start, node.End}] = node
}

type snapshot struct {
	manifest *Manifest
	data     []byte
	tree     *Tree
}

// Archive keeps every published snapshot of a file simultaneously, so an
// old file handle keeps reading old content after newer versions arrive.
type Archive struct {
	fileID string
	signer Signer
	// versions maps version number to its snapshot.
	versions map[uint64]*snapshot
}

// NewArchive creates an empty archive for fileID using signer.
func NewArchive(fileID string, signer Signer) *Archive {
	return &Archive{
		fileID:   fileID,
		signer:   signer,
		versions: make(map[uint64]*snapshot),
	}
}

func (a *Archive) snapshot(version uint64) (*snapshot, error) {
	if a.versions == nil {
		return nil, ErrHandleMismatch
	}
	snap, ok := a.versions[version]
	if !ok {
		return nil, ErrHandleMismatch
	}
	return snap, nil
}

// Publish stores data as a new version. The first version must have
// previousRoot == EmptyRoot; every later version must chain to the
// immediately previous root and increment Version by one.
func (a *Archive) Publish(data []byte, version uint64, previousRoot [32]byte) (*Manifest, error) {
	tree := BuildTree(data)
	manifest := &Manifest{
		FileID:       a.fileID,
		Length:       uint64(len(data)),
		Version:      version,
		PreviousRoot: previousRoot,
		Root:         tree.Root(),
	}
	if err := manifest.Sign(a.signer); err != nil {
		return nil, err
	}
	if _, exists := a.versions[version]; exists {
		return nil, fmt.Errorf("readproof: version %d already published", version)
	}
	if version == 1 {
		if previousRoot != EmptyRoot {
			return nil, ErrManifestChain
		}
	} else {
		prev, ok := a.versions[version-1]
		if !ok || prev.manifest.Root != previousRoot {
			return nil, ErrManifestChain
		}
	}
	stored := append([]byte(nil), data...)
	a.versions[version] = &snapshot{manifest: manifest, data: stored, tree: tree}
	return manifest, nil
}

// Handle is a receiver-side read handle pinned to one manifest generation.
type Handle struct {
	archive  *Archive
	manifest *Manifest
	verifier Verifier
	cache    NodeCache
}

// Open pins a handle to manifest. The archive must hold a matching
// snapshot whose root equals manifest.Root.
func (a *Archive) Open(manifest *Manifest, verifier Verifier, cache NodeCache) (*Handle, error) {
	snap, err := a.snapshot(manifest.Version)
	if err != nil {
		return nil, err
	}
	if snap.manifest.Root != manifest.Root {
		return nil, ErrProofMismatch
	}
	return &Handle{
		archive:  a,
		manifest: manifest,
		verifier: verifier,
		cache:    cache,
	}, nil
}

// Read authenticates and returns the blocks of ranges, merging intersecting
// requests into one merged proof. On success the proof's sibling
// commitments are cached under this handle's root; cache entries tagged
// with another generation are never consulted.
func (h *Handle) Read(ranges []Range) (map[uint64][]byte, error) {
	snap, err := h.archive.snapshot(h.manifest.Version)
	if err != nil {
		return nil, err
	}
	n := NumLeaves(h.manifest.Length)
	if n == 0 {
		if len(ranges) != 0 {
			return nil, ErrRangeOutOfBounds
		}
		return map[uint64][]byte{}, nil
	}
	merged, err := MergeRanges(ranges, n)
	if err != nil {
		return nil, err
	}
	proof, err := snap.tree.BuildProof(merged)
	if err != nil {
		return nil, err
	}
	blocks, err := Verify(h.manifest, h.verifier, proof)
	if err != nil {
		return nil, err
	}
	if h.cache != nil {
		for _, node := range proof.Nodes {
			if _, found := h.cache.Get(h.manifest.Root, node.Start, node.End); !found {
				h.cache.Put(h.manifest.Root, CachedNode{
					Start:   node.Start,
					End:     node.End,
					Version: h.manifest.Version,
					Hash:    node.Hash,
				})
			}
		}
	}
	return blocks, nil
}

// Manifest returns the pinned manifest.
func (h *Handle) Manifest() *Manifest { return h.manifest }

// VersionData returns the stored file bytes for version. It is intended for
// reference builders and tests that need ground-truth content.
func (a *Archive) VersionData(version uint64) ([]byte, error) {
	snap, err := a.snapshot(version)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), snap.data...), nil
}

// ErrHandleMismatch is returned when a handle is used against an archive
// that does not contain its generation.
var ErrHandleMismatch = errors.New("readproof: handle generation not held by archive")
