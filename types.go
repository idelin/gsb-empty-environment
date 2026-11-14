package netsec

import (
	"bytes"
	"context"
	"time"
)

// CipherSuite identifies an MLS cipher suite. The component treats it as an
// opaque, comparable identifier; all cryptographic interpretation is delegated
// to Crypto.
type CipherSuite uint16

// Credential is the credential carried by a ratchet-tree leaf.
type Credential struct {
	Type      uint16
	Identity  []byte
	PublicKey []byte
}

func (c Credential) IsZero() bool {
	return len(c.Identity) == 0 && len(c.PublicKey) == 0
}

func (c Credential) Equal(other Credential) bool {
	return c.Type == other.Type &&
		bytes.Equal(c.Identity, other.Identity) &&
		bytes.Equal(c.PublicKey, other.PublicKey)
}

// Extension is an MLS extension. Data is opaque to this package.
type Extension struct {
	Type uint16
	Data []byte
}

// Capabilities describes the capabilities advertised by a leaf.
type Capabilities struct {
	Versions     []uint16
	CipherSuites []CipherSuite
	Extensions   []uint16
	Proposals    []uint16
	Credentials  []uint16
}

// LeafNode is one leaf of a ratchet tree. A blank leaf has Blank set and no
// usable credential or keys.
type LeafNode struct {
	Index         uint32
	Blank         bool
	Credential    Credential
	SignatureKey  []byte
	EncryptionKey []byte
	Capabilities  Capabilities
	Extensions    []Extension
}

// RatchetTree is the decoded form of a ratchet tree. Extensions here are the
// group-context extensions bound by the tree hash and must agree with the
// GroupInfo extensions.
type RatchetTree struct {
	Leaves     []LeafNode
	Extensions []Extension
}

// GroupInfo carries the signed group context for a Welcome.
type GroupInfo struct {
	GroupID          []byte
	Epoch            uint64
	CipherSuite      CipherSuite
	TreeHash         []byte
	TranscriptHash   []byte
	Extensions       []Extension
	RequiredPSKIDs   [][]byte
	Signer           uint32
	SignerCredential *Credential
	Signature        []byte
}

// EncryptedGroupSecrets is one recipient's encrypted GroupSecrets.
type EncryptedGroupSecrets struct {
	KeyPackageRef []byte
	Ciphertext    []byte
}

// GroupSecrets is the plaintext recovered from EncryptedGroupSecrets. Real MLS
// only carries JoinerSecret, PathSecret and PSKs here; GroupID, Epoch,
// CipherSuite and KeyPackageRef are included so an application can make the
// complete candidate binding explicit and reject mixed ciphertexts.
type GroupSecrets struct {
	GroupID       []byte
	Epoch         uint64
	CipherSuite   CipherSuite
	KeyPackageRef []byte
	JoinerSecret  []byte
	PathSecret    []byte
	PSKIDs        [][]byte
}

// KeyPackage is the client's one-time key package plus the leaf that must be
// present in the welcome's ratchet tree.
type KeyPackage struct {
	Reference   []byte
	GroupID     []byte // optional target-group binding
	CipherSuite CipherSuite
	LeafIndex   uint32
	Leaf        LeafNode
	NotBefore   time.Time
	NotAfter    time.Time
}

// Expired reports whether the key package is outside its validity period.
func (kp KeyPackage) Expired(now time.Time) bool {
	return !kp.NotAfter.IsZero() && !now.Before(kp.NotAfter)
}

// NotYetValid reports whether the key package's validity period has not begun.
func (kp KeyPackage) NotYetValid(now time.Time) bool {
	return !kp.NotBefore.IsZero() && now.Before(kp.NotBefore)
}

// LocalParticipant is the state held by the joining client. PrivateKey is
// opaque to this package and is interpreted by Crypto. PSKs is keyed by PSK ID
// (the raw bytes converted to a string; see PSKKey). ExpectedGroupID, when
// non-empty, pins the only group this client is willing to join.
type LocalParticipant struct {
	KeyPackage      KeyPackage
	PrivateKey      any
	PSKs            map[string][]byte
	ExpectedGroupID []byte
}

// PSKKey returns the map key used for a raw PSK ID.
func PSKKey(id []byte) string {
	return string(id)
}

// RatchetTreeReference identifies an out-of-band ratchet tree. Binding is an
// application nonce bound to this Welcome; when empty the component uses the
// computed Welcome ID.
type RatchetTreeReference struct {
	GroupID  []byte
	Epoch    uint64
	TreeHash []byte
	Binding  []byte
}

// Welcome is the admission input. Exactly one of RatchetTree (inline bytes) or
// TreeReference (external fetch) must be supplied. Tree may optionally contain
// the already decoded inline tree; otherwise Crypto decodes RatchetTree.
//
// PriorTranscriptHash is the confirmed transcript hash of the client's current
// state (or the value carried by the Welcome for a fresh joiner).
type Welcome struct {
	GroupInfo           GroupInfo
	Secrets             []EncryptedGroupSecrets
	RatchetTree         []byte
	Tree                *RatchetTree
	TreeReference       *RatchetTreeReference
	PriorTranscriptHash []byte
	ConfirmationTag     []byte
}

// TreeFetchRequest is passed to the external tree callback. All fields are
// bound to the Welcome being admitted. TreeHash is the digest that the returned
// material must have, and Binding must be echoed by the response.
type TreeFetchRequest struct {
	GroupID     []byte
	Epoch       uint64
	CipherSuite CipherSuite
	TreeHash    []byte
	WelcomeID   []byte
	Binding     []byte
}

// TreeMaterial is the result of an external tree fetch. Bytes and Digest must
// both describe the tree selected for this Welcome; Binding must equal the
// requested Binding. Tree is optional and is decoded from Bytes when nil.
type TreeMaterial struct {
	Bytes   []byte
	Digest  []byte
	Binding []byte
	Tree    *RatchetTree
}

// RatchetTreeFetcher fetches a ratchet tree. Implementations must not consult
// mutable shared state after returning: the caller copies and validates Bytes
// against Digest immediately.
type RatchetTreeFetcher interface {
	FetchRatchetTree(ctx context.Context, req TreeFetchRequest) (TreeMaterial, error)
}

// RatchetTreeFetcherFunc adapts a function to RatchetTreeFetcher.
type RatchetTreeFetcherFunc func(context.Context, TreeFetchRequest) (TreeMaterial, error)

func (f RatchetTreeFetcherFunc) FetchRatchetTree(ctx context.Context, req TreeFetchRequest) (TreeMaterial, error) {
	return f(ctx, req)
}

// TranscriptInput is the complete group state over which the confirmed
// transcript hash is computed.
type TranscriptInput struct {
	GroupID             []byte
	Epoch               uint64
	CipherSuite         CipherSuite
	PriorTranscriptHash []byte
	TreeHash            []byte
	Extensions          []Extension
	ConfirmationTag     []byte
}

// JoinerSecretInput binds a decrypted joiner secret to the selected group
// state, key package, PSKs and cipher suite.
type JoinerSecretInput struct {
	GroupID        []byte
	Epoch          uint64
	CipherSuite    CipherSuite
	KeyPackageRef  []byte
	JoinerSecret   []byte
	PSKIDs         [][]byte
	PSKs           map[string][]byte
	TreeHash       []byte
	TranscriptHash []byte
	Extensions     []Extension
}

// GroupState is the durable group state advanced by a successful admission.
type GroupState struct {
	GroupID        []byte
	Epoch          uint64
	CipherSuite    CipherSuite
	TreeHash       []byte
	TranscriptHash []byte
	Extensions     []Extension
	RatchetTree    []byte
	Signer         uint32
}

// Admission is returned only after the Welcome and its one-time consumptions
// have been committed atomically.
type Admission struct {
	WelcomeID      []byte
	GroupID        []byte
	Epoch          uint64
	CipherSuite    CipherSuite
	KeyPackageRef  []byte
	JoinerSecret   []byte
	PathSecret     []byte
	PSKIDs         [][]byte
	TreeHash       []byte
	TranscriptHash []byte
	Extensions     []Extension
	Signer         uint32
	Tree           RatchetTree
}

// State returns the durable state committed for this admission.
func (a *Admission) State() GroupState {
	if a == nil {
		return GroupState{}
	}
	return GroupState{
		GroupID:        cloneBytes(a.GroupID),
		Epoch:          a.Epoch,
		CipherSuite:    a.CipherSuite,
		TreeHash:       cloneBytes(a.TreeHash),
		TranscriptHash: cloneBytes(a.TranscriptHash),
		Extensions:     cloneExtensions(a.Extensions),
		Signer:         a.Signer,
	}
}
