package targetverify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const metadataTypeTargets = "targets"

// HexDigest is a byte slice encoded as a lower-case hexadecimal JSON string.
type HexDigest []byte

// MarshalJSON encodes the digest as a hex string.
func (h HexDigest) MarshalJSON() ([]byte, error) {
	return json.Marshal(hex.EncodeToString(h))
}

// UnmarshalJSON accepts a hex string and stores its raw bytes.
func (h *HexDigest) UnmarshalJSON(data []byte) error {
	var encoded string
	if err := json.Unmarshal(data, &encoded); err != nil {
		return fmt.Errorf("%w: digest is not a JSON string: %v", ErrMalformed, err)
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("%w: digest %q is not valid hexadecimal", ErrMalformed, encoded)
	}
	*h = raw
	return nil
}

// Hex returns the digest as a lower-case hexadecimal string.
func (h HexDigest) Hex() string { return hex.EncodeToString(h) }

func (h HexDigest) String() string { return h.Hex() }

// Hashes holds the cryptographic digests of a target file.
type Hashes struct {
	SHA256 HexDigest `json:"sha256"`
}

// TargetFile describes the immutable content of a target file.
type TargetFile struct {
	Length int64  `json:"length"`
	Hashes Hashes `json:"hashes"`
}

// Custom is the Director-side per-target binding to a concrete ECU.
type Custom struct {
	ECUIdentifier      string `json:"ecuIdentifier"`
	HardwareIdentifier string `json:"hardwareIdentifier"`
}

// TargetMeta is one entry of a targets document.
type TargetMeta struct {
	File   TargetFile `json:"file"`
	Custom Custom     `json:"custom"`
}

// SignedMeta is the signed payload of a targets metadata document.
type SignedMeta struct {
	Type    string                `json:"_type"`
	Version int                   `json:"version"`
	Expires time.Time             `json:"expires"`
	Targets map[string]TargetMeta `json:"targets"`
}

// Signature pairs a key ID with a signature over the raw signed payload.
type Signature struct {
	KeyID string    `json:"keyid"`
	Sig   HexDigest `json:"sig"`
}

// Metadata is a parsed targets document together with the exact signed byte
// sequence that all signatures are checked against.
type Metadata struct {
	Signed     SignedMeta
	RawSigned  []byte
	Signatures []Signature
}

type rawEnvelope struct {
	Signed     json.RawMessage `json:"signed"`
	Signatures []Signature     `json:"signatures"`
}

// ParseMetadata parses a {"signed": ..., "signatures": [...]} envelope and
// validates its structural invariants. Signature thresholds and expiry are
// checked separately by the Verifier.
func ParseMetadata(data []byte) (*Metadata, error) {
	var envelope rawEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("%w: envelope is not valid JSON: %v", ErrMalformed, err)
	}
	if len(envelope.Signed) == 0 {
		return nil, fmt.Errorf("%w: envelope has no signed payload", ErrMalformed)
	}
	if len(envelope.Signatures) == 0 {
		return nil, fmt.Errorf("%w: envelope carries no signatures", ErrMalformed)
	}

	metadata := &Metadata{
		RawSigned:  append([]byte(nil), envelope.Signed...),
		Signatures: envelope.Signatures,
	}
	if err := json.Unmarshal(envelope.Signed, &metadata.Signed); err != nil {
		return nil, fmt.Errorf("%w: signed payload is not valid JSON: %v", ErrMalformed, err)
	}
	if err := metadata.validate(); err != nil {
		return nil, err
	}
	return metadata, nil
}

func (m *Metadata) validate() error {
	if m.Signed.Type != metadataTypeTargets {
		return fmt.Errorf("%w: expected _type %q, got %q", ErrMalformed, metadataTypeTargets, m.Signed.Type)
	}
	if m.Signed.Version < 1 {
		return fmt.Errorf("%w: version must be >= 1, got %d", ErrMalformed, m.Signed.Version)
	}
	if m.Signed.Expires.IsZero() {
		return fmt.Errorf("%w: expires is missing", ErrMalformed)
	}
	for name, target := range m.Signed.Targets {
		if name == "" {
			return fmt.Errorf("%w: target with empty file name", ErrMalformed)
		}
		if target.File.Length <= 0 {
			return fmt.Errorf("%w: target %q has non-positive length", ErrMalformed, name)
		}
		if len(target.File.Hashes.SHA256) != sha256.Size {
			return fmt.Errorf("%w: target %q sha256 must be %d bytes, got %d",
				ErrMalformed, name, sha256.Size, len(target.File.Hashes.SHA256))
		}
	}
	for _, signature := range m.Signatures {
		if signature.KeyID == "" {
			return fmt.Errorf("%w: signature with empty keyid", ErrMalformed)
		}
		if len(signature.Sig) == 0 {
			return fmt.Errorf("%w: signature for key %q is empty", ErrMalformed, signature.KeyID)
		}
	}
	return nil
}
