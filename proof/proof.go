package proof

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

// Wire tags (4 bytes) distinguish record types canonically.
var (
	tagManifest = [4]byte{'M', 'F', 'S', 'T'}
	tagObject   = [4]byte{'O', 'B', 'J', '1'}
	tagChunk    = [4]byte{'C', 'H', 'N', 'K'}
)

// Domain-separation prefixes keep hash contexts from colliding.
var (
	rootPrefix  = []byte("proof/root-summary/v1\n")
	objPrefix   = []byte("proof/object-record/v1\n")
	chunkPrefix = []byte("proof/chunk/v1\n")
)

var (
	ErrRootMismatch    = errors.New("proof: root summary mismatch (old proof or tampered manifest)")
	ErrBadManifest     = errors.New("proof: invalid manifest geometry")
	ErrUnknownObject   = errors.New("proof: unknown object id")
	ErrDuplicateObject = errors.New("proof: duplicate object id")
	ErrDanglingRef     = errors.New("proof: reference points at missing object")
	ErrChunkIndex      = errors.New("proof: chunk index out of range")
	ErrChunkHash       = errors.New("proof: chunk content hash mismatch")
	ErrNotAllChunks    = errors.New("proof: not every chunk has been verified")
	ErrNotComplete     = errors.New("proof: package has not been declared complete")
	ErrAlreadyFinished = errors.New("proof: verifier already finished")
	ErrBadObjectHash   = errors.New("proof: reassembled object record hash mismatch")
	ErrRefGraph        = errors.New("proof: reference graph not closed")
)

// ChunkInfo binds a chunk's position, byte length and content hash.
type ChunkInfo struct {
	Index uint32
	Bytes uint32
	Hash  [32]byte
}

// ObjDesc describes one object: payload placement inside the package payload
// stream, and the sorted set of object ids it references.
type ObjDesc struct {
	ID     uint32
	Offset uint64
	Length uint32
	Kind   string
	Name   string
	Refs   []uint32
}

// Manifest is the object-level metadata. Its canonical encoding is the single
// source of truth committed by the root summary.
type Manifest struct {
	PkgID     string
	Version   uint64
	ChunkSize uint32
	Chunks    []ChunkInfo
	Objects   []*ObjDesc
}

// PayloadLength returns total bytes covered by chunks.
func (m *Manifest) PayloadLength() uint64 {
	var n uint64
	for _, c := range m.Chunks {
		n += uint64(c.Bytes)
	}
	return n
}

// EncodeCanonical returns the unique canonical bytes of a manifest.
func (m *Manifest) EncodeCanonical() []byte {
	b := make([]byte, 0, 256)
	b = appendTag(b, tagManifest)
	b = appendLPStr(b, m.PkgID)
	b = appendU64(b, m.Version)
	b = appendU32(b, m.ChunkSize)
	b = appendU32(b, uint32(len(m.Chunks)))
	for _, c := range m.Chunks {
		b = appendU32(b, c.Index)
		b = appendU32(b, c.Bytes)
		b = append(b, c.Hash[:]...)
	}
	b = appendU32(b, uint32(len(m.Objects)))
	for _, o := range m.Objects {
		b = appendU32(b, o.ID)
		b = appendU64(b, o.Offset)
		b = appendU32(b, o.Length)
		b = appendLPStr(b, o.Kind)
		b = appendLPStr(b, o.Name)
		b = appendU32(b, uint32(len(o.Refs)))
		for _, r := range o.Refs {
			b = appendU32(b, r)
		}
	}
	return b
}

// DecodeManifest parses a canonical manifest for the given scope. The claimed
// length is checked before any slice is allocated, the canonical form must
// round-trip byte-for-byte, and the resulting geometry is validated.
func DecodeManifest(scope Scope, data []byte) (*Manifest, error) {
	r, err := newScopeReader(scope, data)
	if err != nil {
		return nil, err
	}
	if err := r.tag(tagManifest); err != nil {
		return nil, err
	}
	m := &Manifest{}
	if m.PkgID, err = r.lpStr(MaxIDBytes, "pkgid"); err != nil {
		return nil, err
	}
	if m.Version, err = r.u64(); err != nil {
		return nil, err
	}
	if m.ChunkSize, err = r.u32(); err != nil {
		return nil, err
	}
	nChunks, err := r.count32(MaxIDCount, 4+4+32, "chunks")
	if err != nil {
		return nil, err
	}
	m.Chunks = make([]ChunkInfo, nChunks)
	for i := range m.Chunks {
		if m.Chunks[i].Index, err = r.u32(); err != nil {
			return nil, err
		}
		if m.Chunks[i].Bytes, err = r.u32(); err != nil {
			return nil, err
		}
		h, err := r.fixed(32, "chunkhash")
		if err != nil {
			return nil, err
		}
		copy(m.Chunks[i].Hash[:], h)
	}
	nObjs, err := r.count32(MaxIDCount, 4+8+4, "objects")
	if err != nil {
		return nil, err
	}
	m.Objects = make([]*ObjDesc, nObjs)
	for i := range m.Objects {
		o := &ObjDesc{}
		if o.ID, err = r.u32(); err != nil {
			return nil, err
		}
		if o.Offset, err = r.u64(); err != nil {
			return nil, err
		}
		if o.Length, err = r.u32(); err != nil {
			return nil, err
		}
		if o.Kind, err = r.lpStr(MaxIDBytes, "kind"); err != nil {
			return nil, err
		}
		if o.Name, err = r.lpStr(MaxIDBytes, "name"); err != nil {
			return nil, err
		}
		if o.Refs, err = r.u32List(MaxIDCount, "refs"); err != nil {
			return nil, err
		}
		m.Objects[i] = o
	}
	if err := r.end(); err != nil {
		return nil, err
	}
	// Canonical encoding must be unambiguous.
	if string(m.EncodeCanonical()) != string(data) {
		return nil, fmt.Errorf("%w: non-canonical manifest encoding", ErrBadManifest)
	}
	return m, nil
}

// Validate checks structural geometry and reference closure independently of
// any transport. Returns nil only when chunks, objects, order, coverage and the
// reference graph are internally consistent.
func (m *Manifest) Validate() error {
	if m.PkgID == "" {
		return fmt.Errorf("%w: empty package id", ErrBadManifest)
	}
	if m.ChunkSize == 0 {
		return fmt.Errorf("%w: zero chunk size", ErrBadManifest)
	}
	if int64(m.ChunkSize) > int64(MaxChunkBytes) {
		return fmt.Errorf("%w: chunk size too large", ErrBadManifest)
	}

	// Chunks: exact order 0..n-1, exact sizes (last may be short), contiguous.
	var payloadLen uint64
	for i, c := range m.Chunks {
		if uint64(c.Index) != uint64(i) {
			return fmt.Errorf("%w: chunk index permuted (slot %d got %d)", ErrBadManifest, i, c.Index)
		}
		if c.Bytes == 0 {
			return fmt.Errorf("%w: zero-length chunk %d", ErrBadManifest, i)
		}
		isLast := i == len(m.Chunks)-1
		if uint64(c.Bytes) > uint64(m.ChunkSize) || (!isLast && c.Bytes != m.ChunkSize) {
			return fmt.Errorf("%w: chunk %d bad length %d", ErrBadManifest, i, c.Bytes)
		}
		payloadLen += uint64(c.Bytes)
	}

	// Objects: ascending ids, ascending offsets, exact contiguous coverage.
	ids := make(map[uint32]struct{}, len(m.Objects))
	var nextOffset uint64
	lastID := int64(-1)
	for i, o := range m.Objects {
		if o == nil {
			return fmt.Errorf("%w: nil object slot %d", ErrBadManifest, i)
		}
		if int64(o.ID) <= lastID {
			return fmt.Errorf("%w: object ids not strictly ascending at %d", ErrBadManifest, i)
		}
		lastID = int64(o.ID)
		if _, dup := ids[o.ID]; dup {
			return fmt.Errorf("%w: id %d", ErrDuplicateObject, o.ID)
		}
		ids[o.ID] = struct{}{}
		if o.Offset != nextOffset {
			return fmt.Errorf("%w: object %d offset %d, expected %d", ErrBadManifest, o.ID, o.Offset, nextOffset)
		}
		if int64(o.Length) > int64(MaxObjectBytes) {
			return fmt.Errorf("%w: object %d too large", ErrBadManifest, o.ID)
		}
		nextOffset = o.Offset + uint64(o.Length)
	}
	if nextOffset != payloadLen {
		return fmt.Errorf("%w: objects cover %d payload bytes but chunks hold %d", ErrBadManifest, nextOffset, payloadLen)
	}
	// Empty package is legal only when truly empty.
	if len(m.Objects) == 0 && payloadLen != 0 {
		return fmt.Errorf("%w: payload present with no objects", ErrBadManifest)
	}

	// Reference graph: closed, sorted and unique refs.
	for _, o := range m.Objects {
		last := int64(-1)
		seen := make(map[uint32]struct{}, len(o.Refs))
		for _, ref := range o.Refs {
			if int64(ref) <= last {
				return fmt.Errorf("%w: object %d refs not strictly ascending", ErrBadManifest, o.ID)
			}
			last = int64(ref)
			if _, d := seen[ref]; d {
				return fmt.Errorf("%w: object %d duplicate ref %d", ErrRefGraph, o.ID, ref)
			}
			seen[ref] = struct{}{}
			if _, ok := ids[ref]; !ok {
				return fmt.Errorf("%w: object %d -> %d", ErrDanglingRef, o.ID, ref)
			}
		}
	}
	return nil
}

// RootSummary commits package id+version, chunk count/order/hashes, object
// count/order/size and the reference graph, because it hashes the canonical
// manifest which contains exactly those fields.
type RootSummary [32]byte

func RootSummaryOf(m *Manifest) RootSummary {
	h := sha256.New()
	h.Write(rootPrefix)
	h.Write(m.EncodeCanonical())
	var out RootSummary
	copy(out[:], h.Sum(nil))
	return out
}

// ObjectRecord is the serializable, self-describing form of one object.
type ObjectRecord struct {
	ID      uint32
	Kind    string
	Name    string
	Refs    []uint32
	Payload []byte
}

func EncodeObjectRecord(o *ObjectRecord) []byte {
	b := make([]byte, 0, 32+len(o.Payload))
	b = appendTag(b, tagObject)
	b = appendU32(b, o.ID)
	b = appendLPStr(b, o.Kind)
	b = appendLPStr(b, o.Name)
	b = appendU32(b, uint32(len(o.Refs)))
	for _, r := range o.Refs {
		b = appendU32(b, r)
	}
	b = appendLP32(b, o.Payload)
	return b
}

// ObjectRecordHash commits the whole record, including the payload.
func ObjectRecordHash(rec []byte) [32]byte {
	h := sha256.New()
	h.Write(objPrefix)
	h.Write(rec)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func DecodeObjectRecord(scope Scope, data []byte) (*ObjectRecord, error) {
	r, err := newScopeReader(scope, data)
	if err != nil {
		return nil, err
	}
	if err := r.tag(tagObject); err != nil {
		return nil, err
	}
	o := &ObjectRecord{}
	if o.ID, err = r.u32(); err != nil {
		return nil, err
	}
	if o.Kind, err = r.lpStr(MaxIDBytes, "kind"); err != nil {
		return nil, err
	}
	if o.Name, err = r.lpStr(MaxIDBytes, "name"); err != nil {
		return nil, err
	}
	if o.Refs, err = r.u32List(MaxIDCount, "refs"); err != nil {
		return nil, err
	}
	if o.Payload, err = r.lp32(MaxObjectBytes, "payload"); err != nil {
		return nil, err
	}
	if err := r.end(); err != nil {
		return nil, err
	}
	if string(EncodeObjectRecord(o)) != string(data) {
		return nil, fmt.Errorf("%w: non-canonical object record", ErrBadManifest)
	}
	return o, nil
}

// ChunkHash commits index and bytes together: a chunk moved to a different
// position hashes differently, so swapping blocks fails.
func ChunkHash(index uint32, p []byte) [32]byte {
	h := sha256.New()
	h.Write(chunkPrefix)
	var ib [4]byte
	putU32(ib[:], index)
	h.Write(ib[:])
	h.Write(p)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func sortedU32(in []uint32) []uint32 {
	out := append([]uint32(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
