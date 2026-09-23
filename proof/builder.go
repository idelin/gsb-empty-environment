package proof

import (
	"errors"
	"fmt"
)

var ErrBuilder = errors.New("proof: cannot build package")

// Object is a builder input.
type Object struct {
	Kind    string
	Name    string
	Refs    []uint32 // references to object IDs (forward refs allowed)
	Payload []byte
}

// Package is a fully built, verified-at-build-time package.
type Package struct {
	Manifest *Manifest
	Root     RootSummary
	// Payload is the concatenation of canonical object records.
	Payload []byte
}

// Build packages objects with assigned ids 0..n-1 in call order. The returned
// manifest passes Validate, so the reference graph is already closed; verifiers
// still re-check it independently.
func Build(pkgID string, version uint64, chunkSize uint32, objs []Object) (*Package, error) {
	if pkgID == "" {
		return nil, fmt.Errorf("%w: empty package id", ErrBuilder)
	}
	if chunkSize == 0 || int64(chunkSize) > int64(MaxChunkBytes) {
		return nil, fmt.Errorf("%w: bad chunk size %d", ErrBuilder, chunkSize)
	}

	payload := make([]byte, 0)
	descs := make([]*ObjDesc, 0, len(objs))
	ids := make(map[uint32]struct{}, len(objs))
	for id, in := range objs {
		oid := uint32(id)
		if _, dup := ids[oid]; dup {
			return nil, fmt.Errorf("%w: %w id=%d", ErrBuilder, ErrDuplicateObject, oid)
		}
		ids[oid] = struct{}{}
		if len(in.Kind) > MaxIDBytes || len(in.Name) > MaxIDBytes {
			return nil, fmt.Errorf("%w: object %d metadata too long", ErrBuilder, oid)
		}
		refs := sortedU32(in.Refs)
		rec := EncodeObjectRecord(&ObjectRecord{
			ID:      oid,
			Kind:    in.Kind,
			Name:    in.Name,
			Refs:    refs,
			Payload: in.Payload,
		})
		if len(rec) > int(MaxObjectBytes)+(1<<20) {
			return nil, fmt.Errorf("%w: object %d too large", ErrBuilder, oid)
		}
		descs = append(descs, &ObjDesc{
			ID:     oid,
			Offset: uint64(len(payload)),
			Length: uint32(len(rec)),
			Kind:   in.Kind,
			Name:   in.Name,
			Refs:   refs,
		})
		payload = append(payload, rec...)
	}
	// Reference closure (independent of Validate; build fails fast).
	for _, o := range descs {
		for _, ref := range o.Refs {
			if _, ok := ids[ref]; !ok {
				return nil, fmt.Errorf("%w: %w object %d -> %d", ErrBuilder, ErrDanglingRef, o.ID, ref)
			}
		}
	}

	chunks := make([]ChunkInfo, 0, (len(payload)/int(chunkSize))+1)
	if len(payload) == 0 {
		// A package with zero objects has no chunks.
	} else {
		for off, idx := 0, uint32(0); off < len(payload); off, idx = off+int(chunkSize), idx+1 {
			end := off + int(chunkSize)
			if end > len(payload) {
				end = len(payload)
			}
			part := payload[off:end]
			chunks = append(chunks, ChunkInfo{
				Index: idx,
				Bytes: uint32(len(part)),
				Hash:  ChunkHash(idx, part),
			})
		}
	}

	m := &Manifest{
		PkgID:     pkgID,
		Version:   version,
		ChunkSize: chunkSize,
		Chunks:    chunks,
		Objects:   descs,
	}
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBuilder, err)
	}
	if len(m.EncodeCanonical()) > MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest too large", ErrBuilder)
	}
	return &Package{Manifest: m, Root: RootSummaryOf(m), Payload: payload}, nil
}
