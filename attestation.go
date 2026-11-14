package netsec

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"fmt"
	"sort"
	"time"
)

// NodeIdentity is the result of verifying a node attestation document.
type NodeIdentity struct {
	NodeID      string
	TrustDomain TrustDomain
	Selectors   map[string]string
	ExpiresAt   time.Time
}

func (n NodeIdentity) clone() NodeIdentity {
	n.Selectors = cloneStringMap(n.Selectors)
	return n
}

// NodeAttestation binds a node and a workload ID to a connection challenge.
// Signature covers every field other than Signature. IssuedAt and ExpiresAt
// must be non-zero; Nonce must carry the control plane's fresh challenge unless
// AllowEmptyAttestationNonce was explicitly enabled.
type NodeAttestation struct {
	TrustDomain TrustDomain
	NodeID      string
	WorkloadID  SPIFFEID
	Nonce       []byte
	IssuedAt    time.Time
	ExpiresAt   time.Time
	Selectors   map[string]string
	Signature   []byte
}

// SigningPayload returns the deterministic byte string signed by Sign.
func (doc NodeAttestation) SigningPayload() []byte {
	var out []byte
	out = appendAttestationString(out, "workload-node-attestation-v1")
	out = appendAttestationString(out, string(doc.TrustDomain))
	out = appendAttestationString(out, doc.NodeID)
	out = appendAttestationString(out, doc.WorkloadID.String())
	out = appendAttestationBytes(out, doc.Nonce)
	out = appendAttestationString(out, doc.IssuedAt.UTC().Format(time.RFC3339Nano))
	out = appendAttestationString(out, doc.ExpiresAt.UTC().Format(time.RFC3339Nano))

	keys := make([]string, 0, len(doc.Selectors))
	for key := range doc.Selectors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out = appendAttestationString(out, fmt.Sprintf("%d", len(keys)))
	for _, key := range keys {
		out = appendAttestationString(out, key)
		out = appendAttestationString(out, doc.Selectors[key])
	}
	return out
}

// Sign signs the canonical attestation payload with an Ed25519 key.
func (doc *NodeAttestation) Sign(privateKey ed25519.PrivateKey) error {
	if doc == nil {
		return fmt.Errorf("%w: nil attestation", ErrAttestation)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: invalid Ed25519 private key", ErrAttestation)
	}
	doc.Signature = ed25519.Sign(privateKey, doc.SigningPayload())
	return nil
}

// VerifySignature verifies the attestation with an Ed25519 public key.
func (doc NodeAttestation) VerifySignature(publicKey ed25519.PublicKey) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: invalid Ed25519 public key", ErrAttestation)
	}
	if !ed25519.Verify(publicKey, doc.SigningPayload(), doc.Signature) {
		return fmt.Errorf("%w: invalid attestation signature", ErrAttestation)
	}
	return nil
}

// NodeAttestor verifies an attestation document and returns the attested node.
// Implementations must authenticate the document; Authority applies the
// workload, trust-domain, freshness, nonce, and selector bindings afterwards.
type NodeAttestor interface {
	VerifyAttestation(ctx context.Context, doc NodeAttestation) (*NodeIdentity, error)
}

// NodeAttestorFunc adapts a function to NodeAttestor.
type NodeAttestorFunc func(ctx context.Context, doc NodeAttestation) (*NodeIdentity, error)

func (f NodeAttestorFunc) VerifyAttestation(ctx context.Context, doc NodeAttestation) (*NodeIdentity, error) {
	return f(ctx, doc)
}

// Ed25519Attestor verifies attestation documents using a node-ID key registry.
type Ed25519Attestor struct {
	keys map[string]ed25519.PublicKey
}

// NewEd25519Attestor constructs an attestor from a node ID to public key map.
func NewEd25519Attestor(keys map[string]ed25519.PublicKey) *Ed25519Attestor {
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for nodeID, key := range keys {
		copied[nodeID] = append(ed25519.PublicKey(nil), key...)
	}
	return &Ed25519Attestor{keys: copied}
}

func (a *Ed25519Attestor) VerifyAttestation(ctx context.Context, doc NodeAttestation) (*NodeIdentity, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAttestation, err)
	}
	if doc.NodeID == "" {
		return nil, fmt.Errorf("%w: empty node ID", ErrAttestation)
	}
	key, ok := a.keys[doc.NodeID]
	if !ok {
		return nil, fmt.Errorf("%w: unknown node %q", ErrAttestation, doc.NodeID)
	}
	if err := doc.VerifySignature(key); err != nil {
		return nil, err
	}
	return &NodeIdentity{
		NodeID:      doc.NodeID,
		TrustDomain: doc.TrustDomain,
		Selectors:   cloneStringMap(doc.Selectors),
		ExpiresAt:   doc.ExpiresAt,
	}, nil
}

func appendAttestationString(dst []byte, value string) []byte {
	return appendAttestationBytes(dst, []byte(value))
}

func appendAttestationBytes(dst []byte, value []byte) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	dst = append(dst, length[:]...)
	dst = append(dst, value...)
	return dst
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
