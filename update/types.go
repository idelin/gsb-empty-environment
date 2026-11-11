package update

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Role names used by the update protocol.
const (
	RoleRoot      = "root"
	RoleTimestamp = "timestamp"
	RoleSnapshot  = "snapshot"
	RoleTargets   = "targets"
)

// Metadata type tags stored inside each signed document.
const (
	typeRoot      = "root"
	typeTimestamp = "timestamp"
	typeSnapshot  = "snapshot"
	typeTargets   = "targets"
)

// SignatureAlgorithm identifies a signature scheme. Only Ed25519 is supported.
type SignatureAlgorithm string

const (
	Ed25519 SignatureAlgorithm = "ed25519"
)

// HashAlgorithm identifies a digest algorithm used inside snapshot/target data.
type HashAlgorithm string

const (
	SHA256 HashAlgorithm = "sha256"
	SHA512 HashAlgorithm = "sha512"
)

// HexBytes is a raw byte slice serialized as a lower-case hexadecimal string.
type HexBytes []byte

// MarshalJSON implements json.Marshaler.
func (h HexBytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(hex.EncodeToString(h))
}

// UnmarshalJSON implements json.Unmarshaler.
func (h *HexBytes) UnmarshalJSON(data []byte) error {
	var encoded string
	if err := json.Unmarshal(data, &encoded); err != nil {
		return err
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("update: invalid hex digest: %w", err)
	}
	*h = raw
	return nil
}

// PublicKey is one key authorized for signing a role. The Alias is the
// stable identifier under which the key appears in Root.Keys and is the
// unit counted when evaluating a role threshold.
type PublicKey struct {
	// Alias is the unique key identifier (key id) inside a root document.
	Alias string `json:"alias"`

	// Algorithm is always "ed25519".
	Algorithm SignatureAlgorithm `json:"algorithm"`

	// Public is the raw 32-byte Ed25519 public key.
	Public HexBytes `json:"public"`
}

// RoleDef declares which key aliases may sign a role and how many distinct
// aliases must produce a valid signature.
type RoleDef struct {
	Threshold  int      `json:"threshold"`
	KeyAliases []string `json:"key_aliases"`
}

// RootFile is the signed root document. It pins the keys and thresholds of
// every role and therefore anchors the trust chain.
type RootFile struct {
	Type    string               `json:"_type"`
	Version int64                `json:"version"`
	Expires time.Time            `json:"expires"`
	Keys    map[string]PublicKey `json:"keys"`
	Roles   map[string]RoleDef   `json:"roles"`
}

// MetaRef is a parent role's reference to a child metadata document or
// target file. It pins version, length and one or more digests.
type MetaRef struct {
	Version int64                      `json:"version"`
	Length  int64                      `json:"length,omitempty"`
	Hashes  map[HashAlgorithm]HexBytes `json:"hashes,omitempty"`
}

// TimestampFile pins the exact bytes of the snapshot document.
type TimestampFile struct {
	Type    string    `json:"_type"`
	Version int64     `json:"version"`
	Expires time.Time `json:"expires"`
	Meta    MetaRef   `json:"meta"`
}

// SnapshotFile pins the exact bytes of every delegated targets document.
type SnapshotFile struct {
	Type    string             `json:"_type"`
	Version int64              `json:"version"`
	Expires time.Time          `json:"expires"`
	Meta    map[string]MetaRef `json:"meta"`
}

// TargetFile describes an individual update payload.
type TargetFile struct {
	Type    string                     `json:"_type"`
	Version int64                      `json:"version"`
	Expires time.Time                  `json:"expires"`
	Length  int64                      `json:"length"`
	Hashes  map[HashAlgorithm]HexBytes `json:"hashes"`
}

// Signature is one signature over an envelope's Signed payload.
type Signature struct {
	// KeyID is the Root.Keys alias of the signing key.
	KeyID     string             `json:"key_id"`
	Algorithm SignatureAlgorithm `json:"algorithm"`
	Signature HexBytes           `json:"signature"`
}

// Envelope wraps a signed document together with the signatures over its
// canonical bytes. An envelope is the immutable unit exchanged with mirrors
// and persisted by the client.
type Envelope struct {
	Signed     json.RawMessage `json:"signed"`
	Signatures []Signature     `json:"signatures"`

	// raw is the exact canonical encoding of the envelope. It is cached
	// after ParseEnvelope and used for digest comparisons.
	raw []byte
}

// ParseEnvelope decodes an envelope and requires canonical, deterministic
// encoding. The returned envelope remembers its canonical bytes.
func ParseEnvelope(data []byte) (*Envelope, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var envelope Envelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("update: decode envelope: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, ErrTrailingData
		}
		return nil, fmt.Errorf("update: decode envelope: %w", err)
	}
	// Round-trip the typed payload so time.Time values lose their
	// monotonic clock component; struct field order remains the canonical
	// key order and is preserved throughout.
	typed, err := decodeCanonicalTyped(envelope.Signed)
	if err != nil {
		return nil, err
	}
	envelope.Signed, err = json.Marshal(typed)
	if err != nil {
		return nil, err
	}

	canonical, err := json.Marshal(&envelope)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(canonical, data) {
		return nil, ErrNonCanonical
	}
	envelope.raw = canonical
	return &envelope, nil
}

// decodeCanonicalTyped re-encodes a signed payload through its concrete
// document type. This strips time.Time monotonic readings without reordering
// struct fields, which define the canonical key order.
func decodeCanonicalTyped(raw json.RawMessage) (any, error) {
	var head struct {
		Type string `json:"_type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("update: decode signed payload: %w", err)
	}
	var doc any
	switch head.Type {
	case typeRoot:
		doc = &RootFile{}
	case typeTimestamp:
		doc = &TimestampFile{}
	case typeSnapshot:
		doc = &SnapshotFile{}
	case typeTargets:
		doc = &TargetFile{}
	default:
		return nil, fmt.Errorf("%w: unknown _type %q", ErrTypeMismatch, head.Type)
	}
	if err := json.Unmarshal(raw, doc); err != nil {
		return nil, fmt.Errorf("update: decode signed payload: %w", err)
	}
	return doc, nil
}

// Bytes returns the canonical serialization of the envelope.
func (e *Envelope) Bytes() []byte {
	if e == nil {
		return nil
	}
	return e.raw
}

// SignedBytes returns the canonical bytes covered by the signatures.
func (e *Envelope) SignedBytes() []byte {
	if e == nil {
		return nil
	}
	return append([]byte(nil), e.Signed...)
}

// decodeTyped parses the Signed payload and requires its _type field to
// equal wantType.
func (e *Envelope) decodeTyped(wantType string, out any) error {
	if err := json.Unmarshal(e.Signed, out); err != nil {
		return fmt.Errorf("update: decode %s document: %w", wantType, err)
	}
	typed, ok := out.(interface{ getType() string })
	if ok && typed.getType() != wantType {
		return fmt.Errorf("%w: want %s, got %s", ErrTypeMismatch, wantType, typed.getType())
	}
	return nil
}

func (r *RootFile) getType() string      { return r.Type }
func (t *TimestampFile) getType() string { return t.Type }
func (s *SnapshotFile) getType() string  { return s.Type }
func (t *TargetFile) getType() string    { return t.Type }

func parseRoot(envelope *Envelope) (*RootFile, error) {
	root := &RootFile{}
	if err := envelope.decodeTyped(typeRoot, root); err != nil {
		return nil, err
	}
	if root.Type != typeRoot {
		return nil, fmt.Errorf("%w: want %s, got %s", ErrTypeMismatch, typeRoot, root.Type)
	}
	if root.Version < 1 {
		return nil, fmt.Errorf("update: root version must be positive")
	}
	if root.Keys == nil || root.Roles == nil {
		return nil, fmt.Errorf("update: root must define keys and roles")
	}
	for alias, key := range root.Keys {
		if key.Alias != alias {
			return nil, fmt.Errorf("update: key alias %q does not match map key %q", key.Alias, alias)
		}
		if key.Algorithm != Ed25519 || len(key.Public) != ed25519PublicKeySize {
			return nil, fmt.Errorf("update: root key %q uses unsupported key material", alias)
		}
	}
	for name, role := range root.Roles {
		if role.Threshold < 1 {
			return nil, fmt.Errorf("update: role %q threshold must be positive", name)
		}
		seen := make(map[string]struct{}, len(role.KeyAliases))
		for _, alias := range role.KeyAliases {
			if _, ok := root.Keys[alias]; !ok {
				return nil, fmt.Errorf("update: role %q references unknown key %q", name, alias)
			}
			if _, dup := seen[alias]; dup {
				return nil, fmt.Errorf("update: role %q lists key alias %q more than once", name, alias)
			}
			seen[alias] = struct{}{}
		}
	}
	for _, required := range []string{RoleRoot, RoleTimestamp, RoleSnapshot, RoleTargets} {
		if _, ok := root.Roles[required]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingRole, required)
		}
	}
	return root, nil
}

func parseTimestamp(envelope *Envelope) (*TimestampFile, error) {
	doc := &TimestampFile{}
	if err := envelope.decodeTyped(typeTimestamp, doc); err != nil {
		return nil, err
	}
	if doc.Version < 1 {
		return nil, fmt.Errorf("update: timestamp version must be positive")
	}
	if doc.Meta.Version < 1 {
		return nil, fmt.Errorf("update: timestamp must pin a snapshot version")
	}
	return doc, nil
}

func parseSnapshot(envelope *Envelope) (*SnapshotFile, error) {
	doc := &SnapshotFile{}
	if err := envelope.decodeTyped(typeSnapshot, doc); err != nil {
		return nil, err
	}
	if doc.Version < 1 || doc.Meta == nil {
		return nil, fmt.Errorf("update: snapshot version must be positive and meta must be present")
	}
	return doc, nil
}

func parseTargets(envelope *Envelope) (*TargetFile, error) {
	doc := &TargetFile{}
	if err := envelope.decodeTyped(typeTargets, doc); err != nil {
		return nil, err
	}
	if doc.Version < 1 {
		return nil, fmt.Errorf("update: targets version must be positive")
	}
	if doc.Length < 0 || doc.Hashes == nil || len(doc.Hashes) == 0 {
		return nil, fmt.Errorf("update: targets must pin payload length and hashes")
	}
	return doc, nil
}

// EnvelopeFromSigned builds a canonical envelope for a typed document and
// attaches the supplied signatures. At most one signature per key alias is
// retained (a public-key alias can never cast more than one vote), and the
// remaining signatures are sorted by key alias for stable encoding.
func EnvelopeFromSigned(doc any, signatures []Signature) (*Envelope, error) {
	signed, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]Signature, len(signatures))
	for _, signature := range signatures {
		unique[signature.KeyID] = signature
	}
	ordered := make([]Signature, 0, len(unique))
	for _, signature := range unique {
		ordered = append(ordered, signature)
	}
	sortSignatures(ordered)
	envelope := &Envelope{Signed: json.RawMessage(signed), Signatures: ordered}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	envelope.raw = raw
	return envelope, nil
}

func sortSignatures(signatures []Signature) {
	for i := 1; i < len(signatures); i++ {
		for j := i; j > 0 && signatures[j-1].KeyID > signatures[j].KeyID; j-- {
			signatures[j-1], signatures[j] = signatures[j], signatures[j-1]
		}
	}
}
