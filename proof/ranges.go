package proof

import (
	"fmt"
)

// ChunkFetcher supplies raw chunk bytes by index from an untrusted source
// (envelope file, network, ...).
type ChunkFetcher interface {
	FetchChunk(index uint32) ([]byte, error)
}

// ChunkEvidence is the per-chunk proof produced by a ranged read.
type ChunkEvidence struct {
	Index     uint32
	Hash      [32]byte
	FromCache bool
}

// RangeEvidence proves one ranged read: every chunk touching [offset, offset+len)
// was hash-verified against the manifest, and the manifest is pinned to Root.
type RangeEvidence struct {
	Root   RootSummary
	Chunks []ChunkEvidence
}

// ObtainChunk returns verified chunk bytes, consulting the version-bound cache
// first and falling back to the untrusted fetcher. Cache bytes are re-hashed;
// fetched bytes are hashed, cached, and accepted by the verifier. Verification
// before Finish is strictly local.
func ObtainChunk(v *Verifier, f ChunkFetcher, cache *ChunkCache, index uint32) ([]byte, ChunkEvidence, error) {
	m := v.manifest
	if int(index) >= len(m.Chunks) {
		return nil, ChunkEvidence{}, fmt.Errorf("%w: %d", ErrChunkIndex, index)
	}
	info := m.Chunks[index]
	ev := ChunkEvidence{Index: index, Hash: info.Hash}

	if cache != nil {
		if p, ok := cache.Get(m.PkgID, m.Version, index, info.Hash); ok {
			if err := v.AcceptChunk(index, p); err != nil {
				return nil, ev, err
			}
			ev.FromCache = true
			return p, ev, nil
		}
	}
	if f == nil {
		return nil, ev, fmt.Errorf("%w: chunk %d unavailable (no fetcher, no cache hit)", ErrNotAllChunks, index)
	}
	p, err := f.FetchChunk(index)
	if err != nil {
		return nil, ev, err
	}
	if err := v.AcceptChunk(index, p); err != nil {
		return nil, ev, err
	}
	if cache != nil {
		_ = cache.Put(m.PkgID, m.Version, index, info.Hash, p)
	}
	return p, ev, nil
}

// ReadRange returns verified payload bytes for [offset, offset+length). It works
// on an arbitrary subset: only the chunks covering the range are fetched and
// locally verified. The returned evidence binds those chunks to the root
// without claiming the whole package is complete.
func ReadRange(v *Verifier, f ChunkFetcher, cache *ChunkCache, offset, length uint64) ([]byte, RangeEvidence, error) {
	m := v.manifest
	total := m.PayloadLength()
	if offset > total || length > total-offset {
		return nil, RangeEvidence{Root: v.trustedRoot}, fmt.Errorf("%w: range [%d,%d) outside payload %d",
			ErrBadManifest, offset, offset+length, total)
	}
	if length == 0 {
		return []byte{}, RangeEvidence{Root: v.trustedRoot}, nil
	}
	cs := uint64(m.ChunkSize)
	first := offset / cs
	last := (offset + length - 1) / cs

	ev := RangeEvidence{Root: v.trustedRoot}
	out := make([]byte, 0, length)
	for i := first; i <= last; i++ {
		p, cev, err := ObtainChunk(v, f, cache, uint32(i))
		if err != nil {
			return nil, ev, err
		}
		ev.Chunks = append(ev.Chunks, cev)
		chunkStart := i * cs
		lo := uint64(0)
		if offset > chunkStart {
			lo = offset - chunkStart
		}
		hi := uint64(len(p))
		rangeEnd := offset + length
		if chunkStart+hi > rangeEnd {
			hi = rangeEnd - chunkStart
		}
		if lo >= hi {
			continue
		}
		out = append(out, p[lo:hi]...)
	}
	return out, ev, nil
}

// VerifiedObject is a ranged, object-level read with its proof.
type VerifiedObject struct {
	ID      uint32
	Record  []byte // canonical record bytes
	Payload []byte
	Refs    []uint32
	Kind    string
	Name    string
}

// ReadObject verifies exactly the chunks covering one object's record, re-decodes
// the object, and returns it. It succeeds on an arbitrary subset; it never
// implies other objects (or the reference graph closure) have been verified.
func ReadObject(v *Verifier, f ChunkFetcher, cache *ChunkCache, id uint32) (*VerifiedObject, RangeEvidence, error) {
	m := v.manifest
	var o *ObjDesc
	for _, cand := range m.Objects {
		if cand.ID == id {
			o = cand
			break
		}
	}
	if o == nil {
		return nil, RangeEvidence{Root: v.trustedRoot}, fmt.Errorf("%w: id %d", ErrUnknownObject, id)
	}
	rec, ev, err := ReadRange(v, f, cache, o.Offset, uint64(o.Length))
	if err != nil {
		return nil, ev, err
	}
	dec, err := DecodeObjectRecord(ScopeObject, rec)
	if err != nil {
		return nil, ev, fmt.Errorf("%w: object %d: %v", ErrBadObjectHash, id, err)
	}
	return &VerifiedObject{
		ID:      dec.ID,
		Record:  rec,
		Payload: dec.Payload,
		Refs:    dec.Refs,
		Kind:    dec.Kind,
		Name:    dec.Name,
	}, ev, nil
}
