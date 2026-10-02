package erasable

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"runtime"
)

// eraseDomain separates deletion-signature messages from any other use.
const eraseDomain = "ERASABLE-VAULT-ERASE-v1"

// ErrAlreadyErased is returned when erasing a leaf twice.
var ErrAlreadyErased = errors.New("erasable: record already erased")

// ErrNotFound is returned when a record id is unknown.
var ErrNotFound = errors.New("erasable: record id not found")

// EraseMarker is the tombstone committed into the tree in place of an
// erased record. It binds the leaf index and the original leaf hash to a
// deletion signature issued by the compliance officer, so any auditor can
// verify that the erasure was authorized without learning the erased data.
type EraseMarker struct {
	Index            int
	OriginalLeafHash []byte
	Signature        []byte
}

// Hash computes the Erase_Marker leaf hash committed into the tree:
//
//	H(0xE1 || "ERASED" || index || originalLeafHash || signature)
func (m *EraseMarker) Hash() []byte {
	h := sha256.New()
	h.Write([]byte{prefixMarker})
	h.Write([]byte("ERASED"))
	h.Write(encodeUint64(uint64(m.Index)))
	h.Write(m.OriginalLeafHash)
	h.Write(m.Signature)
	return h.Sum(nil)
}

// signingMessage is the exact byte string the compliance officer signs.
func signingMessage(index int, originalLeafHash []byte) []byte {
	msg := make([]byte, 0, len(eraseDomain)+8+len(originalLeafHash))
	msg = append(msg, eraseDomain...)
	msg = append(msg, encodeUint64(uint64(index))...)
	msg = append(msg, originalLeafHash...)
	return msg
}

// Verify checks the deletion signature on the marker against pub.
// The signature is the 64-byte big-endian r||s encoding of an ECDSA
// P-256 signature over SHA-256 of the signing message.
func (m *EraseMarker) Verify(pub *ecdsa.PublicKey) bool {
	if len(m.Signature) != 64 {
		return false
	}
	digest := sha256.Sum256(signingMessage(m.Index, m.OriginalLeafHash))
	r := new(big.Int).SetBytes(m.Signature[:32])
	s := new(big.Int).SetBytes(m.Signature[32:])
	return ecdsa.Verify(pub, digest[:], r, s)
}

// Vault stores customer records and commits them into a Merkle tree.
// It supports verifiable erasure: deletion replaces the record's leaf
// with a signed Erase_Marker and physically zeroes the plaintext.
//
// The Vault takes ownership of the data slices passed to Insert and
// overwrites them with zeros upon Erase (secure physical erasure).
type Vault struct {
	tree    *Tree
	signer  *ecdsa.PrivateKey
	pub     *ecdsa.PublicKey
	byID    map[string]int // record id -> leaf index
	records map[int][]byte // leaf index -> live plaintext (nil after erasure)
	markers map[int]*EraseMarker
}

// NewVault creates a Vault with a freshly generated compliance-officer
// signing key (ECDSA P-256). The public key is exposed via PublicKey
// for auditors.
func NewVault() (*Vault, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("erasable: key generation: %v", err)
	}
	return &Vault{
		tree:    NewTree(),
		signer:  priv,
		pub:     &priv.PublicKey,
		byID:    make(map[string]int),
		records: make(map[int][]byte),
		markers: make(map[int]*EraseMarker),
	}, nil
}

// PublicKey returns the compliance officer's verification key.
func (v *Vault) PublicKey() *ecdsa.PublicKey { return v.pub }

// Tree exposes the underlying commitment tree (read-only usage).
func (v *Vault) Tree() *Tree { return v.tree }

// Root returns the current commitment root of the vault.
func (v *Vault) Root() []byte { return v.tree.Root() }

// Insert commits a record and returns its leaf index. The Vault takes
// ownership of data; callers must not retain sensitive copies.
func (v *Vault) Insert(id string, data []byte) (int, error) {
	if _, dup := v.byID[id]; dup {
		return 0, fmt.Errorf("erasable: duplicate record id %q", id)
	}
	idx := v.tree.Append(LeafHash(data))
	v.byID[id] = idx
	v.records[idx] = data
	return idx, nil
}

// IndexOf returns the leaf index of a record id.
func (v *Vault) IndexOf(id string) (int, bool) {
	idx, ok := v.byID[id]
	return idx, ok
}

// Get returns the live plaintext for a leaf index, or nil if the record
// was erased or never existed.
func (v *Vault) Get(index int) []byte { return v.records[index] }

// Erase performs a verifiable deletion of the record at index:
//
//  1. the compliance officer signs the deletion (index + original leaf);
//  2. the leaf is rewritten in place to the Erase_Marker hash;
//  3. the plaintext is physically overwritten with zeros and dropped.
//
// The returned marker is the public, auditable proof of erasure.
func (v *Vault) Erase(index int) (*EraseMarker, error) {
	if _, gone := v.markers[index]; gone {
		return nil, ErrAlreadyErased
	}
	orig, err := v.tree.Leaf(index)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(signingMessage(index, orig))
	r, s, err := ecdsa.Sign(rand.Reader, v.signer, digest[:])
	if err != nil {
		return nil, fmt.Errorf("erasable: signing deletion: %v", err)
	}
	sig := make([]byte, 64)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):64], sb)
	m := &EraseMarker{Index: index, OriginalLeafHash: orig, Signature: sig}
	if err := v.tree.SetLeaf(index, m.Hash()); err != nil {
		return nil, err
	}
	// Physical erasure: overwrite the plaintext in place before
	// releasing it, so no sensitive bytes linger in memory.
	if data, ok := v.records[index]; ok {
		secureZero(data)
		delete(v.records, index)
	}
	v.markers[index] = m
	return m, nil
}

// EraseByID erases the record with the given id.
func (v *Vault) EraseByID(id string) (*EraseMarker, error) {
	idx, ok := v.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	return v.Erase(idx)
}

// Marker returns the erasure marker for an index, if erased.
func (v *Vault) Marker(index int) (*EraseMarker, bool) {
	m, ok := v.markers[index]
	return m, ok
}

// ProveRecord builds an inclusion proof for a live record: it returns
// the record's leaf hash together with its Merkle path. The proof is
// verified against the current root with proof.Verify(leaf, v.Root()).
func (v *Vault) ProveRecord(index int) (*Proof, []byte, error) {
	if _, gone := v.markers[index]; gone {
		return nil, nil, ErrAlreadyErased
	}
	data, ok := v.records[index]
	if !ok {
		return nil, nil, ErrIndexOutOfRange
	}
	p, err := v.tree.Prove(index)
	if err != nil {
		return nil, nil, err
	}
	return p, LeafHash(data), nil
}

// ProveErasure builds an auditable erasure proof: the marker plus a
// Merkle path showing the marker hash is committed at its index under
// the current root. Verify it with VerifyErasure.
func (v *Vault) ProveErasure(index int) (*Proof, *EraseMarker, error) {
	m, ok := v.markers[index]
	if !ok {
		return nil, nil, errors.New("erasable: record not erased")
	}
	p, err := v.tree.Prove(index)
	if err != nil {
		return nil, nil, err
	}
	return p, m, nil
}

// VerifyErasure checks an erasure proof against a root and the
// compliance officer's public key: the marker must be genuinely
// committed in the tree AND carry a valid deletion signature.
func VerifyErasure(p *Proof, m *EraseMarker, root []byte, pub *ecdsa.PublicKey) bool {
	if p == nil || m == nil || p.Index != m.Index {
		return false
	}
	if !m.Verify(pub) {
		return false
	}
	return p.Verify(m.Hash(), root)
}

// secureZero overwrites b with zeros. KeepAlive prevents the compiler
// from eliding the writes as dead stores.
func secureZero(b []byte) {
	for i := range b {
		b[i] = 0
	}
	runtime.KeepAlive(b)
}
