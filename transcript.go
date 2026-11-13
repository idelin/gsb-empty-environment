package netsec

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"time"
)

const transcriptDomain = "example.org/netsec/q0154/transcript/v1"

// Transcript binds both identities, the ephemeral key, every prekey ID and the
// directory proof digest. Its canonical bytes are the AEAD associated data.
type Transcript struct {
	Version              uint16
	Initiator            PublicIdentity
	Receiver             PublicIdentity
	EphemeralKey         []byte
	SignedPreKeyID       uint32
	SignedPreKey         []byte
	HasOneTimePreKey     bool
	OneTimePreKeyID      uint32
	OneTimePreKey        []byte
	BundleExpiresAt      time.Time
	DirectoryProofDigest [32]byte
}

// Validate checks all fields and point encodings in the transcript.
func (t Transcript) Validate() error {
	if t.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidTranscript, t.Version)
	}
	if err := t.Initiator.Validate(); err != nil {
		return fmt.Errorf("%w: initiator: %v", ErrInvalidTranscript, err)
	}
	if err := t.Receiver.Validate(); err != nil {
		return fmt.Errorf("%w: receiver: %v", ErrInvalidTranscript, err)
	}
	if _, err := parseP256Public(t.EphemeralKey); err != nil {
		return fmt.Errorf("%w: ephemeral key: %v", ErrInvalidTranscript, err)
	}
	if t.SignedPreKeyID == 0 {
		return fmt.Errorf("%w: missing signed prekey ID", ErrInvalidTranscript)
	}
	if _, err := parseP256Public(t.SignedPreKey); err != nil {
		return fmt.Errorf("%w: signed prekey: %v", ErrInvalidTranscript, err)
	}
	if t.HasOneTimePreKey {
		if t.OneTimePreKeyID == 0 {
			return fmt.Errorf("%w: missing one-time prekey ID", ErrInvalidTranscript)
		}
		if _, err := parseP256Public(t.OneTimePreKey); err != nil {
			return fmt.Errorf("%w: one-time prekey: %v", ErrInvalidTranscript, err)
		}
	} else if t.OneTimePreKeyID != 0 || len(t.OneTimePreKey) != 0 {
		return fmt.Errorf("%w: one-time prekey presence mismatch", ErrInvalidTranscript)
	}
	if t.BundleExpiresAt.IsZero() {
		return fmt.Errorf("%w: missing bundle expiry", ErrInvalidTranscript)
	}
	return nil
}

// MarshalBinary returns canonical transcript bytes.
func (t Transcript) MarshalBinary() ([]byte, error) {
	return t.encode(), nil
}

func (t Transcript) encode() []byte {
	w := &wireBuilder{}
	w.text(transcriptDomain)
	w.u16(t.Version)
	writePublicIdentity(w, t.Initiator)
	writePublicIdentity(w, t.Receiver)
	w.blob(t.EphemeralKey)
	w.u32(t.SignedPreKeyID)
	w.blob(t.SignedPreKey)
	w.boolean(t.HasOneTimePreKey)
	w.u32(t.OneTimePreKeyID)
	w.blob(t.OneTimePreKey)
	w.i64(t.BundleExpiresAt.UTC().UnixNano())
	w.raw(t.DirectoryProofDigest[:])
	return w.bytes()
}

// Hash returns the SHA-256 digest of the canonical transcript.
func (t Transcript) Hash() [32]byte {
	return sha256.Sum256(t.encode())
}

// ParseTranscript strictly decodes a canonical transcript.
func ParseTranscript(data []byte) (*Transcript, error) {
	if len(data) > maxMessageBytes {
		return nil, fmt.Errorf("%w: transcript too large", ErrInvalidTranscript)
	}
	r := newWireReader(data)
	if domain := r.text(maxWireIDLen); domain != transcriptDomain {
		return nil, fmt.Errorf("%w: wrong domain", ErrInvalidTranscript)
	}
	t := &Transcript{Version: r.u16()}
	initiator, err := readPublicIdentity(r)
	if err != nil {
		return nil, err
	}
	t.Initiator = initiator
	receiver, err := readPublicIdentity(r)
	if err != nil {
		return nil, err
	}
	t.Receiver = receiver
	t.EphemeralKey = r.blob(65)
	t.SignedPreKeyID = r.u32()
	t.SignedPreKey = r.blob(65)
	t.HasOneTimePreKey = r.boolean()
	t.OneTimePreKeyID = r.u32()
	t.OneTimePreKey = r.blob(65)
	t.BundleExpiresAt = time.Unix(0, r.i64()).UTC()
	digest := r.fixed(len(t.DirectoryProofDigest))
	copy(t.DirectoryProofDigest[:], digest)
	if err := r.done(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidTranscript, err)
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	canonical := t.encode()
	if !bytes.Equal(canonical, data) {
		return nil, fmt.Errorf("%w: non-canonical encoding", ErrInvalidTranscript)
	}
	return t, nil
}

func writePublicIdentity(w *wireBuilder, p PublicIdentity) {
	w.text(p.AccountID)
	w.text(p.DeviceID)
	w.blob(p.SigningKey)
	if p.IdentityKey == nil {
		w.blob(nil)
		return
	}
	w.blob(p.IdentityKey.Bytes())
}

func readPublicIdentity(r *wireReader) (PublicIdentity, error) {
	var p PublicIdentity
	p.AccountID = r.text(maxWireIDLen)
	p.DeviceID = r.text(maxWireIDLen)
	p.SigningKey = r.blob(ed25519PublicKeySize)
	encodedIdentity := r.blob(65)
	if err := r.err; err != nil {
		return p, fmt.Errorf("%w: %v", ErrInvalidTranscript, err)
	}
	if len(p.SigningKey) != ed25519PublicKeySize {
		return p, fmt.Errorf("%w: invalid signing key length", ErrInvalidTranscript)
	}
	identityKey, err := parseP256Public(encodedIdentity)
	if err != nil {
		return p, fmt.Errorf("%w: %v", ErrInvalidTranscript, err)
	}
	p.IdentityKey = identityKey
	if err := p.Validate(); err != nil {
		return p, fmt.Errorf("%w: %v", ErrInvalidTranscript, err)
	}
	return p, nil
}

const ed25519PublicKeySize = 32
