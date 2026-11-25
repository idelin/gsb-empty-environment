package zoneauth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// RootEnvelope is the signed commitment of one zone revision. Signature
// covers every field except Signature itself.
type RootEnvelope struct {
	Zone          string `json:"zone"`
	Sequence      uint64 `json:"sequence"`
	KeyID         string `json:"key_id"`
	PreviousKeyID string `json:"previous_key_id,omitempty"`
	RootHash      []byte `json:"root_hash"`
	Signature     []byte `json:"signature"`
}

// HandoffEnvelope authenticates a zone key rollover. It is signed by the OLD
// key and binds the new public key to both the old and the new root hashes,
// so a client that trusted the old key can authenticate the new key without
// any out-of-band channel.
type HandoffEnvelope struct {
	Zone        string `json:"zone"`
	Sequence    uint64 `json:"sequence"`
	OldKeyID    string `json:"old_key_id"`
	NewKeyID    string `json:"new_key_id"`
	OldRootHash []byte `json:"old_root_hash"`
	NewRootHash []byte `json:"new_root_hash"`
	NewKey      []byte `json:"new_key"`
	Signature   []byte `json:"signature"`
}

// KeyIDFor derives the stable identifier of a public key.
func KeyIDFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])
}

func rootSigningBytes(e *RootEnvelope) []byte {
	var buf bytes.Buffer
	buf.WriteString("zoneauth-root/v1\n")
	buf.Write(putString(nil, e.Zone))
	buf.Write(putUvarint(nil, e.Sequence))
	buf.Write(putString(nil, e.KeyID))
	buf.Write(putString(nil, e.PreviousKeyID))
	buf.Write(putBytes(nil, e.RootHash))
	return buf.Bytes()
}

func handoffSigningBytes(e *HandoffEnvelope) []byte {
	var buf bytes.Buffer
	buf.WriteString("zoneauth-handoff/v1\n")
	buf.Write(putString(nil, e.Zone))
	buf.Write(putUvarint(nil, e.Sequence))
	buf.Write(putString(nil, e.OldKeyID))
	buf.Write(putString(nil, e.NewKeyID))
	buf.Write(putBytes(nil, e.OldRootHash))
	buf.Write(putBytes(nil, e.NewRootHash))
	buf.Write(putBytes(nil, e.NewKey))
	return buf.Bytes()
}

// SignRootEnvelope signs the envelope with priv and fills KeyID/Signature.
func SignRootEnvelope(e *RootEnvelope, priv ed25519.PrivateKey) {
	pub := priv.Public().(ed25519.PublicKey)
	e.KeyID = KeyIDFor(pub)
	e.Signature = ed25519.Sign(priv, rootSigningBytes(e))
}

// SignHandoff signs the handoff with the OLD zone key and fills its IDs.
func SignHandoff(e *HandoffEnvelope, oldPriv ed25519.PrivateKey) {
	oldPub := oldPriv.Public().(ed25519.PublicKey)
	newPub := ed25519.PublicKey(e.NewKey)
	e.OldKeyID = KeyIDFor(oldPub)
	e.NewKeyID = KeyIDFor(newPub)
	e.Signature = ed25519.Sign(oldPriv, handoffSigningBytes(e))
}

var (
	// ErrUntrustedKey means the signing key is absent from the trusted key set.
	ErrUntrustedKey = errors.New("zoneauth: root signed by untrusted key")
	// ErrBadSignature means envelope signature or key-id binding failed.
	ErrBadSignature = errors.New("zoneauth: invalid root signature")
)

// VerifyEnvelope checks the key-id binding and the Ed25519 signature against
// a key from the trusted key map.
func VerifyEnvelope(e *RootEnvelope, trusted map[string]ed25519.PublicKey) error {
	pub, ok := trusted[e.KeyID]
	if !ok {
		return ErrUntrustedKey
	}
	if KeyIDFor(pub) != e.KeyID {
		return ErrBadSignature
	}
	if !ed25519.Verify(pub, rootSigningBytes(e), e.Signature) {
		return ErrBadSignature
	}
	return nil
}

// VerifyHandoff checks that the handoff was signed by the old key and that the
// embedded new key matches the declared new key id.
func VerifyHandoff(e *HandoffEnvelope, oldPub ed25519.PublicKey) error {
	if KeyIDFor(oldPub) != e.OldKeyID {
		return ErrBadSignature
	}
	if KeyIDFor(ed25519.PublicKey(e.NewKey)) != e.NewKeyID {
		return ErrBadSignature
	}
	if !ed25519.Verify(oldPub, handoffSigningBytes(e), e.Signature) {
		return ErrBadSignature
	}
	return nil
}
