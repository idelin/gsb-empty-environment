package proof

import (
	"fmt"
	"sort"
	"sync"
)

// Status is the verifier state. Nothing is advertised as a complete package
// until Finish succeeds.
type Status uint8

const (
	// StatusLocal means zero or more chunks have been verified locally; any
	// claim is strictly about those chunks/objects.
	StatusLocal Status = iota
	// StatusComplete means the whole package was verified: exact chunk count,
	// chunk order/hashes, object order/count, every object record and the
	// reference graph all match the trusted root summary.
	StatusComplete
)

func (s Status) String() string {
	if s == StatusComplete {
		return "complete"
	}
	return "local"
}

// Report is returned by Finish.
type Report struct {
	Status       Status
	ChunksTotal  int
	ChunksSeen   int
	ObjectsTotal int
	ObjectSeen   int
}

// Verifier independently checks a package against a trusted root summary.
// The manifest is untrusted input: its geometry is validated and its hash must
// equal trustedRoot; otherwise the verifier refuses to start.
type Verifier struct {
	mu          sync.Mutex
	manifest    *Manifest
	trustedRoot RootSummary
	seen        map[uint32]bool
	payload     []byte // assembled as verified chunks arrive
	status      Status
}

// NewVerifier validates the manifest independently and pins it to trustedRoot.
// Order, count, geometry and reference-closure checks happen here, before any
// chunk is accepted. A manifest missing a necessary object fails because the
// reference graph is not closed; partial chunks never upgrade that to success.
func NewVerifier(m *Manifest, trustedRoot RootSummary) (*Verifier, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil manifest", ErrBadManifest)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if RootSummaryOf(m) != trustedRoot {
		return nil, ErrRootMismatch
	}
	return &Verifier{
		manifest:    m,
		trustedRoot: trustedRoot,
		seen:        make(map[uint32]bool),
		payload:     make([]byte, m.PayloadLength()),
	}, nil
}

// NewVerifierFromEnvelope uses the envelope manifest but requires the caller to
// supply the trusted root (the on-disk advertised root is not trusted).
func NewVerifierFromEnvelope(e *Envelope, trustedRoot RootSummary) (*Verifier, error) {
	return NewVerifier(e.Manifest(), trustedRoot)
}

func (v *Verifier) Status() Status {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.status
}

func (v *Verifier) ChunkCount() int  { return len(v.manifest.Chunks) }
func (v *Verifier) ObjectCount() int { return len(v.manifest.Objects) }

// AcceptChunk verifies one chunk locally: index in range, byte length matches
// the manifest geometry, and ChunkHash(index, bytes) matches. On success the
// bytes are placed at their committed offset. State stays StatusLocal.
func (v *Verifier) AcceptChunk(index uint32, p []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.status == StatusComplete {
		return ErrAlreadyFinished
	}
	if int(index) >= len(v.manifest.Chunks) {
		return fmt.Errorf("%w: %d", ErrChunkIndex, index)
	}
	info := v.manifest.Chunks[index]
	if len(p) != int(info.Bytes) {
		return fmt.Errorf("%w: chunk %d length %d want %d", ErrChunkHash, index, len(p), info.Bytes)
	}
	if ChunkHash(index, p) != info.Hash {
		return fmt.Errorf("%w: chunk %d", ErrChunkHash, index)
	}
	var off uint64
	for _, c := range v.manifest.Chunks[:index] {
		off += uint64(c.Bytes)
	}
	copy(v.payload[off:off+uint64(info.Bytes)], p)
	v.seen[index] = true
	return nil
}

// VerifiedChunks returns sorted indices locally verified so far.
func (v *Verifier) VerifiedChunks() []uint32 {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]uint32, 0, len(v.seen))
	for i := range v.seen {
		out = append(out, i)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// VerifiedObjectIDs returns objects whose entire canonical record is covered by
// locally verified chunks and whose record re-decodes correctly. Local evidence
// about some objects never implies the package is complete.
func (v *Verifier) VerifiedObjectIDs() ([]uint32, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.locallySeenObjectsLocked()
}

func (v *Verifier) locallySeenObjectsLocked() ([]uint32, error) {
	var out []uint32
	for _, o := range v.manifest.Objects {
		ok, err := v.objectCoverLocked(o)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, o.ID)
		}
	}
	return out, nil
}

func (v *Verifier) objectCoverLocked(o *ObjDesc) (bool, error) {
	start := o.Offset
	end := o.Offset + uint64(o.Length)
	if end > uint64(len(v.payload)) {
		return false, nil
	}
	cs := uint64(v.manifest.ChunkSize)
	first := start / cs
	last := (end - 1) / cs
	for i := first; i <= last; i++ {
		if !v.seen[uint32(i)] {
			return false, nil
		}
	}
	rec := v.payload[start:end]
	if _, err := DecodeObjectRecord(ScopeObject, rec); err != nil {
		return false, fmt.Errorf("%w: object %d: %v", ErrBadObjectHash, o.ID, err)
	}
	return true, nil
}

// Finish attempts to declare the package complete. It fails if any chunk is
// missing (deleting the last chunk fails even when every earlier chunk was
// perfect), re-decodes every object record, and checks the exact object count.
// The root summary was pinned at construction, so success proves order + count
// + reference graph + every byte.
func (v *Verifier) Finish() (*Report, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.status == StatusComplete {
		return v.reportLocked(), nil
	}

	if len(v.seen) != len(v.manifest.Chunks) {
		return v.reportLocked(), fmt.Errorf("%w: %d of %d chunks verified",
			ErrNotAllChunks, len(v.seen), len(v.manifest.Chunks))
	}
	for i := range v.manifest.Chunks {
		if !v.seen[uint32(i)] {
			return v.reportLocked(), fmt.Errorf("%w: missing chunk %d", ErrNotAllChunks, i)
		}
	}

	seenObjs := 0
	for _, o := range v.manifest.Objects {
		start, end := o.Offset, o.Offset+uint64(o.Length)
		if end > uint64(len(v.payload)) {
			return v.reportLocked(), fmt.Errorf("%w: object %d beyond payload", ErrBadObjectHash, o.ID)
		}
		rec := v.payload[start:end]
		dec, err := DecodeObjectRecord(ScopeObject, rec)
		if err != nil {
			return v.reportLocked(), fmt.Errorf("%w: object %d: %v", ErrBadObjectHash, o.ID, err)
		}
		if dec.ID != o.ID || dec.Kind != o.Kind || dec.Name != o.Name {
			return v.reportLocked(), fmt.Errorf("%w: object %d metadata mismatch", ErrBadObjectHash, o.ID)
		}
		if len(dec.Refs) != len(o.Refs) {
			return v.reportLocked(), fmt.Errorf("%w: object %d refs mismatch", ErrBadObjectHash, o.ID)
		}
		for i, r := range dec.Refs {
			if r != o.Refs[i] {
				return v.reportLocked(), fmt.Errorf("%w: object %d refs mismatch", ErrBadObjectHash, o.ID)
			}
		}
		seenObjs++
	}
	if seenObjs != len(v.manifest.Objects) {
		return v.reportLocked(), fmt.Errorf("%w: saw %d objects, manifest commits %d",
			ErrRefGraph, seenObjs, len(v.manifest.Objects))
	}

	// Defense in depth: re-validate geometry and root against the trusted value.
	if err := v.manifest.Validate(); err != nil {
		return v.reportLocked(), err
	}
	if RootSummaryOf(v.manifest) != v.trustedRoot {
		return v.reportLocked(), ErrRootMismatch
	}

	v.status = StatusComplete
	return v.reportLocked(), nil
}

func (v *Verifier) reportLocked() *Report {
	objs, _ := v.locallySeenObjectsLocked()
	return &Report{
		Status:       v.status,
		ChunksTotal:  len(v.manifest.Chunks),
		ChunksSeen:   len(v.seen),
		ObjectsTotal: len(v.manifest.Objects),
		ObjectSeen:   len(objs),
	}
}

// MustBeComplete returns ErrNotComplete unless Finish already succeeded.
func (v *Verifier) MustBeComplete() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.status != StatusComplete {
		return ErrNotComplete
	}
	return nil
}

// ObjectPayload returns verified payload bytes for one object; it works locally
// (partial package) when all covering chunks are present and verified, and
// refuses otherwise.
func (v *Verifier) ObjectPayload(id uint32) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, o := range v.manifest.Objects {
		if o.ID != id {
			continue
		}
		ok, err := v.objectCoverLocked(o)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: object %d not fully covered yet", ErrNotComplete, id)
		}
		rec := v.payload[o.Offset : o.Offset+uint64(o.Length)]
		dec, err := DecodeObjectRecord(ScopeObject, rec)
		if err != nil {
			return nil, err
		}
		return dec.Payload, nil
	}
	return nil, fmt.Errorf("%w: id %d", ErrUnknownObject, id)
}
