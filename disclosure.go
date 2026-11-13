package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"
)

// HashAlgorithm identifies the digest algorithm used by the issuer.
type HashAlgorithm string

const (
	// SHA256 is the default digest algorithm.
	SHA256 HashAlgorithm = "sha-256"
	// SHA384 is supported for issuers that use SHA-384.
	SHA384 HashAlgorithm = "sha-384"
	// SHA512 is supported for issuers that use SHA-512.
	SHA512 HashAlgorithm = "sha-512"
)

// Common aliases used by callers that spell algorithm names differently.
const (
	AlgSHA256 = SHA256
	AlgSHA384 = SHA384
	AlgSHA512 = SHA512
)

var (
	// ErrInvalidDisclosure means that a disclosure is not a well-formed
	// [salt, value] array or cannot be decoded.
	ErrInvalidDisclosure = errors.New("netsec: invalid disclosure")
	// ErrDigestMismatch means that a disclosure does not hash to the digest
	// it claims, or to any digest in the signed commitment.
	ErrDigestMismatch = errors.New("netsec: disclosure digest mismatch")
	// ErrIndexMismatch means that a caller-supplied index does not agree
	// with the position derived from the disclosure digest.
	ErrIndexMismatch = errors.New("netsec: disclosure index does not match signed position")
	// ErrNonContiguousIndices means that the disclosed positions contain a
	// gap. The default verifier only accepts a contiguous run of positions.
	ErrNonContiguousIndices = errors.New("netsec: non-contiguous disclosure indices")
	// ErrDuplicateDisclosure means that two disclosures resolve to the same
	// signed position.
	ErrDuplicateDisclosure = errors.New("netsec: duplicate disclosure")
	// ErrDuplicateDigest means that the signed array contains the same
	// digest more than once, making digest-to-position lookup ambiguous.
	ErrDuplicateDigest = errors.New("netsec: duplicate commitment digest")
	// ErrUnmatchedDisclosure means that a disclosure has no corresponding
	// digest in the signed array.
	ErrUnmatchedDisclosure = errors.New("netsec: disclosure has no matching commitment")
	// ErrInvalidCommitment means that a signed array entry is not a usable
	// digest.
	ErrInvalidCommitment = errors.New("netsec: invalid array commitment")
	// ErrMissingDisclosure means that strict verification required every
	// commitment element to be disclosed.
	ErrMissingDisclosure = errors.New("netsec: commitment element was not disclosed")
	// ErrAmbiguousDigest means that a disclosure matched more than one
	// commitment position.
	ErrAmbiguousDigest = errors.New("netsec: ambiguous disclosure digest")
)

// ArrayElement is a digest placeholder in the signed array.
type ArrayElement = string

// ArrayCommitment is the signed, ordered list of array element digests.
type ArrayCommitment = []string

// Disclosure is either an encoded array-element disclosure or its decoded
// components. When Raw or Disclosure is non-empty, the encoded form is used.
// Otherwise Salt and Value are encoded as [salt, value].
//
// Index is the index claimed by the presenter. It is untrusted. Verification
// derives the real position from the digest and rejects a mismatch. Set Index
// to -1 when no index is claimed; the derived position is then used.
type Disclosure struct {
	Index      int           `json:"index"`
	IndexSet   bool          `json:"-"`
	Position   int           `json:"position,omitempty"`
	ArrayIndex int           `json:"array_index,omitempty"`
	Disclosure string        `json:"disclosure,omitempty"`
	Encoded    string        `json:"encoded,omitempty"`
	Raw        string        `json:"raw,omitempty"`
	Salt       string        `json:"salt,omitempty"`
	Value      any           `json:"value,omitempty"`
	Digest     string        `json:"digest,omitempty"`
	Algorithm  HashAlgorithm `json:"alg,omitempty"`
}

// ArrayDisclosure is the indexed disclosure envelope accepted by VerifyArray.
type ArrayDisclosure = Disclosure

// IndexedDisclosure is an alias for Disclosure.
type IndexedDisclosure = Disclosure

// DisclosureObject is an alias for Disclosure.
type DisclosureObject = Disclosure

// NewDisclosure creates an unindexed array-element disclosure. Its Index is
// -1, meaning that the presented disclosure makes no index claim.
func NewDisclosure(salt string, value any) (*Disclosure, error) {
	d := MakeDisclosure(salt, value)
	if _, err := d.encodedForm(); err != nil {
		return nil, err
	}
	return &d, nil
}

// MakeDisclosure creates an unindexed array-element disclosure by value.
func MakeDisclosure(salt string, value any) Disclosure {
	return Disclosure{Salt: salt, Value: value, Index: -1}
}

// NewArrayDisclosure creates a disclosure envelope carrying a claimed index.
func NewArrayDisclosure(index int, salt string, value any) (*Disclosure, error) {
	if index < 0 {
		return nil, fmt.Errorf("%w: negative index %d", ErrIndexMismatch, index)
	}
	d := Disclosure{Index: index, Salt: salt, Value: value}
	if _, err := d.encodedForm(); err != nil {
		return nil, err
	}
	return &d, nil
}

// MakeArrayDisclosure creates an indexed disclosure by value.
func MakeArrayDisclosure(index int, salt string, value any) (Disclosure, error) {
	if index < 0 {
		return Disclosure{}, fmt.Errorf("%w: negative index %d", ErrIndexMismatch, index)
	}
	d := Disclosure{Index: index, Salt: salt, Value: value}
	if _, err := d.encodedForm(); err != nil {
		return Disclosure{}, err
	}
	return d, nil
}

// String returns the encoded disclosure.
func (d Disclosure) String() string {
	encoded, err := d.encodedForm()
	if err != nil {
		return ""
	}
	return encoded
}

// Encode returns the base64url-encoded [salt, value] representation.
func (d Disclosure) Encode() (string, error) {
	encoded, err := d.encodedForm()
	if err != nil {
		return "", err
	}
	return encoded, nil
}

// DigestFor returns the base64url-encoded digest of the disclosure.
func (d Disclosure) DigestFor(algorithm HashAlgorithm) (string, error) {
	decoded, err := decodeDisclosure(d)
	if err != nil {
		return "", err
	}
	algorithm, err = normalizeAlgorithm(firstAlgorithm(algorithm, d.Algorithm))
	if err != nil {
		return "", err
	}
	return encodedDigest(digestBytes(decoded.digestInput(), algorithm)), nil
}

func (d Disclosure) indexClaim() (int, bool, error) {
	hasClaim := false
	claim := 0
	addClaim := func(value int) error {
		if !hasClaim {
			claim = value
			hasClaim = true
			return nil
		}
		if value != claim {
			return fmt.Errorf("%w: conflicting index fields", ErrIndexMismatch)
		}
		return nil
	}

	if d.IndexSet || d.Index != 0 {
		if err := addClaim(d.Index); err != nil {
			return 0, false, err
		}
	}
	if d.Position != 0 {
		if err := addClaim(d.Position); err != nil {
			return 0, false, err
		}
	}
	if d.ArrayIndex != 0 {
		if err := addClaim(d.ArrayIndex); err != nil {
			return 0, false, err
		}
	}

	if !hasClaim {
		if d.Index < -1 || d.Position < -1 || d.ArrayIndex < -1 {
			return 0, false, fmt.Errorf("%w: invalid negative index", ErrIndexMismatch)
		}
		return 0, true, nil
	}
	if claim < -1 {
		return 0, false, fmt.Errorf("%w: invalid index %d", ErrIndexMismatch, claim)
	}
	if claim == -1 {
		return -1, false, nil
	}
	return claim, true, nil
}

// ComputeDigest hashes an encoded disclosure. encoded may also be a literal
// JSON [salt, value] array.
func ComputeDigest(encoded string, algorithm HashAlgorithm) (string, error) {
	decoded, err := decodeEncodedDisclosure(encoded)
	if err != nil {
		return "", err
	}
	algorithm, err = normalizeAlgorithm(algorithm)
	if err != nil {
		return "", err
	}
	return encodedDigest(digestBytes(decoded.digestInput(), algorithm)), nil
}

// HashDisclosure hashes an encoded disclosure with SHA-256 and returns the
// base64url-encoded digest.
func HashDisclosure(encoded string) string {
	digest, _ := ComputeDigest(encoded, SHA256)
	return digest
}

// ParseDisclosure decodes an encoded disclosure. The original encoded form is
// retained so that its digest can be checked exactly as presented.
func ParseDisclosure(encoded string) (Disclosure, error) {
	decoded, err := decodeEncodedDisclosure(encoded)
	if err != nil {
		return Disclosure{}, err
	}
	return Disclosure{
		Index:      -1,
		Disclosure: encoded,
		Salt:       decoded.salt,
		Value:      decoded.value,
	}, nil
}

// UnmarshalJSON accepts an encoded string, a [salt, value] array, or an
// indexed disclosure object.
func (d *Disclosure) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("%w: empty JSON", ErrInvalidDisclosure)
	}

	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDisclosure, err)
		}
		parsed, err := ParseDisclosure(encoded)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	}

	if trimmed[0] == '[' {
		decoded, err := decodeEncodedDisclosure(string(trimmed))
		if err != nil {
			return err
		}
		*d = Disclosure{
			Index: -1,
			Raw:   string(trimmed),
			Salt:  decoded.salt,
			Value: decoded.value,
		}
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDisclosure, err)
	}

	type plain Disclosure
	var p plain
	if err := json.Unmarshal(trimmed, &p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDisclosure, err)
	}
	if _, present := fields["index"]; present {
		p.IndexSet = true
	} else if p.Disclosure != "" || p.Encoded != "" || p.Raw != "" || p.Salt != "" || p.Digest != "" {
		p.Index = -1
	}
	*d = Disclosure(p)
	return nil
}

func firstAlgorithm(values ...HashAlgorithm) HashAlgorithm {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return SHA256
}

func normalizeAlgorithm(algorithm HashAlgorithm) (HashAlgorithm, error) {
	if algorithm == "" {
		return SHA256, nil
	}
	switch strings.ToLower(strings.ReplaceAll(string(algorithm), "_", "-")) {
	case "sha-256", "sha256", "sha2-256":
		return SHA256, nil
	case "sha-384", "sha384", "sha2-384":
		return SHA384, nil
	case "sha-512", "sha512", "sha2-512":
		return SHA512, nil
	default:
		return "", fmt.Errorf("%w: unsupported hash algorithm %q", ErrInvalidDisclosure, algorithm)
	}
}

func newHash(algorithm HashAlgorithm) hash.Hash {
	switch algorithm {
	case SHA384:
		return sha512.New384()
	case SHA512:
		return sha512.New()
	default:
		return sha256.New()
	}
}

func digestBytes(data []byte, algorithm HashAlgorithm) []byte {
	h := newHash(algorithm)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func encodedDigest(digest []byte) string {
	return base64.RawURLEncoding.EncodeToString(digest)
}

type decodedDisclosure struct {
	salt      string
	value     any
	encoded   string
	jsonBytes []byte
	hasValue  bool
	original  *Disclosure
}

func (d decodedDisclosure) digestInput() []byte {
	return []byte(d.encoded)
}

func (d decodedDisclosure) digestCandidates() [][]byte {
	candidates := make([][]byte, 0, 8)
	seen := make(map[string]struct{}, 8)
	add := func(candidate []byte) {
		if len(candidate) == 0 {
			return
		}
		key := string(candidate)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, candidate)
	}

	add([]byte(d.encoded))
	add(d.jsonBytes)
	if d.hasValue {
		if valueJSON, err := json.Marshal(d.value); err == nil {
			add(valueJSON)
			if d.salt != "" {
				add([]byte(d.salt + string(valueJSON)))
				add([]byte(d.salt + ":" + string(valueJSON)))
			}
		}
		if valueString, ok := d.value.(string); ok {
			add([]byte(valueString))
			if d.salt != "" {
				add([]byte(d.salt + valueString))
				add([]byte(d.salt + ":" + valueString))
			}
		}
		object := map[string]any{"salt": d.salt, "value": d.value}
		if objectJSON, err := json.Marshal(object); err == nil {
			add(objectJSON)
		}
		if d.original != nil {
			if originalJSON, err := json.Marshal(*d.original); err == nil {
				add(originalJSON)
			}
		}
	}
	return candidates
}

func (d Disclosure) encodedForm() (string, error) {
	if raw := strings.TrimSpace(d.Disclosure); raw != "" {
		return raw, nil
	}
	if raw := strings.TrimSpace(d.Encoded); raw != "" {
		return raw, nil
	}
	if raw := strings.TrimSpace(d.Raw); raw != "" {
		return raw, nil
	}
	array := []any{d.Salt, d.Value}
	encoded, err := json.Marshal(array)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidDisclosure, err)
	}
	return string(encoded), nil
}

func decodeDisclosure(d Disclosure) (decodedDisclosure, error) {
	if strings.TrimSpace(d.Disclosure) == "" &&
		strings.TrimSpace(d.Encoded) == "" &&
		strings.TrimSpace(d.Raw) == "" &&
		d.Salt == "" &&
		d.Value == nil &&
		strings.TrimSpace(d.Digest) != "" {
		return decodedDisclosure{hasValue: false}, nil
	}
	encoded, err := d.encodedForm()
	if err != nil {
		return decodedDisclosure{}, err
	}
	decoded, err := decodeEncodedDisclosure(encoded)
	if err != nil {
		return decodedDisclosure{}, err
	}
	copyOfDisclosure := d
	decoded.original = &copyOfDisclosure
	return decoded, nil
}

func decodeEncodedDisclosure(encoded string) (decodedDisclosure, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return decodedDisclosure{}, fmt.Errorf("%w: empty disclosure", ErrInvalidDisclosure)
	}

	jsonBytes, err := disclosureJSONBytes(encoded)
	if err != nil {
		return decodedDisclosure{}, err
	}

	var parts []json.RawMessage
	if err := json.Unmarshal(jsonBytes, &parts); err != nil {
		return decodedDisclosure{}, fmt.Errorf("%w: %v", ErrInvalidDisclosure, err)
	}
	if len(parts) != 2 {
		return decodedDisclosure{}, fmt.Errorf("%w: expected [salt, value], got %d elements", ErrInvalidDisclosure, len(parts))
	}

	var salt string
	if err := json.Unmarshal(parts[0], &salt); err != nil {
		return decodedDisclosure{}, fmt.Errorf("%w: salt must be a string: %v", ErrInvalidDisclosure, err)
	}

	var value any
	if err := json.Unmarshal(parts[1], &value); err != nil {
		return decodedDisclosure{}, fmt.Errorf("%w: invalid value: %v", ErrInvalidDisclosure, err)
	}

	return decodedDisclosure{
		salt:      salt,
		value:     value,
		encoded:   encoded,
		jsonBytes: jsonBytes,
		hasValue:  true,
	}, nil
}

func disclosureJSONBytes(encoded string) ([]byte, error) {
	if strings.HasPrefix(encoded, "[") {
		if !json.Valid([]byte(encoded)) {
			return nil, fmt.Errorf("%w: invalid JSON array", ErrInvalidDisclosure)
		}
		return []byte(encoded), nil
	}

	for _, decoder := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		decoded, err := decoder.DecodeString(encoded)
		if err == nil && json.Valid(decoded) {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("%w: not base64url JSON", ErrInvalidDisclosure)
}

func decodeDigestValue(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, ErrInvalidCommitment
	}
	if index := strings.IndexByte(value, ':'); index >= 0 {
		prefix := strings.ToLower(value[:index])
		if strings.Contains(prefix, "sha") {
			value = value[index+1:]
		}
	}
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		value = value[2:]
	}

	if decoded, err := hex.DecodeString(value); err == nil && len(decoded) > 0 {
		return decoded, nil
	}

	for _, decoder := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.StdEncoding,
	} {
		decoded, err := decoder.DecodeString(value)
		if err == nil && len(decoded) > 0 {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("%w: %q is not a digest", ErrInvalidCommitment, value)
}

func digestMatches(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare(left, right) == 1
}
