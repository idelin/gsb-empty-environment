package erasureproof

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"math/big"
	"sync"
)

// Sentinel errors (compare with ==).
var (
	ErrRecordNotFound  = errors.New("erasureproof: record not found")
	ErrRecordErased    = errors.New("erasureproof: record already erased")
	ErrDuplicateRecord = errors.New("erasureproof: duplicate record id")
	ErrIndexOutOfRange = errors.New("erasureproof: leaf index out of range")
)

const (
	erasureContext  = "erasureproof/erase/v1"
	recordLeafLabel = "RECORD"
	eraseLeafLabel  = "ERASED"
)

func appendLengthPrefixed(buf []byte, data []byte) []byte {
	var lb [8]byte
	binary.BigEndian.PutUint64(lb[:], uint64(len(data)))
	buf = append(buf, lb[:]...)
	return append(buf, data...)
}

// RecordLeafHash commits a live record (id + personal data) into a leaf.
func RecordLeafHash(recordID string, data []byte) Digest {
	payload := make([]byte, 0, 16+len(recordID)+len(data))
	payload = append(payload, recordLeafLabel...)
	payload = appendLengthPrefixed(payload, []byte(recordID))
	payload = append(payload, data...)
	return LeafHash(payload)
}

// MarkerHash derives the Erase_Marker leaf that replaces an erased record.
// It is publicly recomputable from (recordID, signature) alone, so anyone
// can audit an erasure without ever seeing the erased personal data.
func MarkerHash(recordID string, signature []byte) Digest {
	payload := make([]byte, 0, 16+len(recordID)+len(signature))
	payload = append(payload, eraseLeafLabel...)
	payload = appendLengthPrefixed(payload, []byte(recordID))
	payload = append(payload, signature...)
	return LeafHash(payload)
}

// deletionMessage is the exact byte string the compliance authority signs
// to authorize an erasure: it binds the record id to the hash of the leaf
// being destroyed.
func deletionMessage(recordID string, oldLeaf Digest) []byte {
	msg := make([]byte, 0, len(erasureContext)+8+len(recordID)+sha256.Size)
	msg = append(msg, erasureContext...)
	msg = appendLengthPrefixed(msg, []byte(recordID))
	msg = append(msg, oldLeaf[:]...)
	return msg
}

// seedReader is a deterministic SHA-256 counter-mode byte stream, used to
// derive reproducible signing keys for demos and tests.
type seedReader struct {
	seed    []byte
	counter uint64
	buf     []byte
}

func (r *seedReader) Read(p []byte) (int, error) {
	for len(r.buf) < len(p) {
		var ctr [8]byte
		binary.BigEndian.PutUint64(ctr[:], r.counter)
		block := sha256.Sum256(append(append([]byte{}, r.seed...), ctr[:]...))
		r.buf = append(r.buf, block[:]...)
		r.counter++
	}
	n := copy(p, r.buf)
	r.buf = r.buf[n:]
	return n, nil
}

// ErasureProof proves that the leaf for RecordID is now an Erase_Marker
// carrying a valid deletion signature from the compliance authority.
type ErasureProof struct {
	RecordID   string
	ErasedLeaf Digest // hash of the destroyed leaf (the data itself is gone)
	Signature  []byte // authority's signature over (RecordID, ErasedLeaf)
	Proof      Proof  // Merkle path of the Erase_Marker in the current tree
}

// InclusionProof proves that a live record is committed in the tree.
type InclusionProof struct {
	RecordID string
	Data     []byte
	Proof    Proof
}

// RootTransition is the auditable record of one erasure: it proves the root
// evolved from OldRoot to NewRoot by replacing exactly one leaf (RecordID's)
// with its Erase_Marker, authorized by Signature.
type RootTransition struct {
	Epoch       uint64
	RecordID    string
	OldRoot     Digest
	NewRoot     Digest
	OldLeaf     Digest
	OldProof    Proof // path of the original leaf under OldRoot
	MarkerProof Proof // path of the Erase_Marker under NewRoot
	Signature   []byte
}

// Prover holds the live record store, the Merkle tree and the compliance
// authority's signing key. It issues inclusion proofs and performs
// verifiable, physically destructive erasures.
type Prover struct {
	mu       sync.Mutex
	key      *ecdsa.PrivateKey
	ids      []string          // leaf order (append-only; erased ids keep their slot)
	index    map[string]int    // record id -> leaf index
	records  map[string][]byte // live personal data (wiped on erasure)
	erasures map[string]*ErasureProof
	tree     *Tree
	history  []RootTransition
}

// NewProver creates a Prover with a freshly generated P-256 signing key.
func NewProver() (*Prover, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return newProver(key), nil
}

// NewProverFromSeed creates a Prover with a deterministic key, for
// reproducible demos and tests.
func NewProverFromSeed(seed []byte) (*Prover, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), &seedReader{seed: seed})
	if err != nil {
		return nil, err
	}
	return newProver(key), nil
}

func newProver(key *ecdsa.PrivateKey) *Prover {
	return &Prover{
		key:      key,
		index:    make(map[string]int),
		records:  make(map[string][]byte),
		erasures: make(map[string]*ErasureProof),
		tree:     NewTree(nil),
	}
}

// PublicKey returns the compliance authority's verification key.
func (p *Prover) PublicKey() *ecdsa.PublicKey { return &p.key.PublicKey }

// Add inserts a record. The Prover takes ownership of data: it will be
// securely zeroed in place if the record is later erased.
func (p *Prover) Add(recordID string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.index[recordID]; ok {
		return ErrDuplicateRecord
	}
	p.index[recordID] = len(p.ids)
	p.ids = append(p.ids, recordID)
	p.records[recordID] = data
	p.rebuildLocked()
	return nil
}

// Root returns the current Merkle root.
func (p *Prover) Root() Digest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tree.Root()
}

// History returns a copy of the erasure transition log (the audit trail of
// the root's smooth evolution).
func (p *Prover) History() []RootTransition {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]RootTransition, len(p.history))
	copy(out, p.history)
	return out
}

// Prove returns an inclusion proof for a live record.
func (p *Prover) Prove(recordID string) (*InclusionProof, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx, ok := p.index[recordID]
	if !ok {
		return nil, ErrRecordNotFound
	}
	data, ok := p.records[recordID]
	if !ok {
		return nil, ErrRecordErased
	}
	proof, err := p.tree.Prove(idx)
	if err != nil {
		return nil, err
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return &InclusionProof{RecordID: recordID, Data: cp, Proof: proof}, nil
}

// Erase destroys a record on user request (right to be forgotten):
//  1. the authority signs the deletion of (recordID, oldLeafHash),
//  2. the personal data is physically zeroed and dropped from the store,
//  3. the leaf is rewritten to the Erase_Marker and the root evolves,
//  4. a RootTransition is appended to the audit log.
func (p *Prover) Erase(recordID string) (*RootTransition, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx, ok := p.index[recordID]
	if !ok {
		return nil, ErrRecordNotFound
	}
	data, ok := p.records[recordID]
	if !ok {
		return nil, ErrRecordErased
	}

	oldRoot := p.tree.Root()
	oldLeaf := RecordLeafHash(recordID, data)
	oldProof, err := p.tree.Prove(idx)
	if err != nil {
		return nil, err
	}

	digest := sha256.Sum256(deletionMessage(recordID, oldLeaf))
	sig, err := signDeletion(rand.Reader, p.key, digest[:])
	if err != nil {
		return nil, err
	}
	// Physical erasure: overwrite every byte of the personal data, then
	// drop the only reference the prover holds.
	secureZero(data)
	delete(p.records, recordID)

	ep := &ErasureProof{
		RecordID:   recordID,
		ErasedLeaf: oldLeaf,
		Signature:  sig,
	}
	p.erasures[recordID] = ep

	p.rebuildLocked()
	markerProof, err := p.tree.Prove(idx)
	if err != nil {
		return nil, err
	}
	ep.Proof = markerProof

	transition := RootTransition{
		Epoch:       uint64(len(p.history)),
		RecordID:    recordID,
		OldRoot:     oldRoot,
		NewRoot:     p.tree.Root(),
		OldLeaf:     oldLeaf,
		OldProof:    oldProof,
		MarkerProof: markerProof,
		Signature:   sig,
	}
	p.history = append(p.history, transition)
	cp := transition
	return &cp, nil
}

// ErasureProof returns the verifiable erasure proof for an erased record.
func (p *Prover) ErasureProof(recordID string) (*ErasureProof, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.index[recordID]; !ok {
		return nil, ErrRecordNotFound
	}
	ep, ok := p.erasures[recordID]
	if !ok {
		return nil, errors.New("erasureproof: record is still live")
	}
	cp := *ep
	return &cp, nil
}

// Record returns a copy of the live data for recordID, or an error if the
// record is unknown or has been erased.
func (p *Prover) Record(recordID string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.index[recordID]; !ok {
		return nil, ErrRecordNotFound
	}
	data, ok := p.records[recordID]
	if !ok {
		return nil, ErrRecordErased
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

func (p *Prover) rebuildLocked() {
	leaves := make([]Digest, len(p.ids))
	for i, id := range p.ids {
		if ep, erased := p.erasures[id]; erased {
			leaves[i] = MarkerHash(ep.RecordID, ep.Signature)
		} else {
			leaves[i] = RecordLeafHash(id, p.records[id])
		}
	}
	p.tree = NewTree(leaves)
}

// secureZero overwrites b in place so the personal data is unrecoverable
// from the prover's memory.
func secureZero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// VerifyInclusion checks a live record's inclusion proof against root.
func VerifyInclusion(root Digest, ip *InclusionProof) bool {
	if ip == nil {
		return false
	}
	return ip.Proof.Verify(RecordLeafHash(ip.RecordID, ip.Data), root)
}

// VerifyErasure checks that under root the leaf for the record is an
// Erase_Marker carrying a valid deletion signature from pub.
func VerifyErasure(pub *ecdsa.PublicKey, root Digest, ep *ErasureProof) bool {
	if pub == nil || ep == nil {
		return false
	}
	digest := sha256.Sum256(deletionMessage(ep.RecordID, ep.ErasedLeaf))
	if !verifyDeletion(pub, digest[:], ep.Signature) {
		return false
	}
	return ep.Proof.Verify(MarkerHash(ep.RecordID, ep.Signature), root)
}

// VerifyTransition audits one root evolution step: the same leaf position
// held OldLeaf under OldRoot and now holds the signed Erase_Marker under
// NewRoot, and the deletion was authorized by pub.
func VerifyTransition(pub *ecdsa.PublicKey, t *RootTransition) bool {
	if pub == nil || t == nil {
		return false
	}
	digest := sha256.Sum256(deletionMessage(t.RecordID, t.OldLeaf))
	if !verifyDeletion(pub, digest[:], t.Signature) {
		return false
	}
	if t.OldProof.Index != t.MarkerProof.Index {
		return false
	}
	if !t.OldProof.Verify(t.OldLeaf, t.OldRoot) {
		return false
	}
	return t.MarkerProof.Verify(MarkerHash(t.RecordID, t.Signature), t.NewRoot)
}

// ensure io.Reader conformance for the deterministic seed stream
var _ io.Reader = (*seedReader)(nil)

// signDeletion produces a fixed-width R||S P-256 signature (2 x 32 bytes).
func signDeletion(rand io.Reader, key *ecdsa.PrivateKey, digest []byte) ([]byte, error) {
	r, s, err := ecdsa.Sign(rand, key, digest)
	if err != nil {
		return nil, err
	}
	size := (key.Curve.Params().N.BitLen() + 7) / 8
	sig := make([]byte, 2*size)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[size-len(rb):size], rb)
	copy(sig[2*size-len(sb):], sb)
	return sig, nil
}

// verifyDeletion checks a fixed-width R||S P-256 signature.
func verifyDeletion(pub *ecdsa.PublicKey, digest, sig []byte) bool {
	size := (pub.Curve.Params().N.BitLen() + 7) / 8
	if len(sig) != 2*size {
		return false
	}
	r := new(big.Int).SetBytes(sig[:size])
	s := new(big.Int).SetBytes(sig[size:])
	return ecdsa.Verify(pub, digest, r, s)
}
