package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxLabelKeyBytes bounds a single label key.
	MaxLabelKeyBytes = 128
	// MaxLabelValueBytes bounds a single label value.
	MaxLabelValueBytes = 1024
	// MaxLabels bounds the number of labels attached to an object.
	MaxLabels = 64
)

// LabelDigest is the SHA-256 digest of the canonical encoding of a final
// label set. Permission decisions are bound to this value, not to labels from
// an individual source.
type LabelDigest [sha256.Size]byte

// Hex returns the lowercase hexadecimal representation of the digest.
func (d LabelDigest) Hex() string {
	return hex.EncodeToString(d[:])
}

// String returns the lowercase hexadecimal representation of the digest.
func (d LabelDigest) String() string {
	return d.Hex()
}

// IsZero reports whether the digest is the zero value.
func (d LabelDigest) IsZero() bool {
	return d == LabelDigest{}
}

// Labels is a set of access labels attached to an object.
//
// Labels are treated as immutable snapshots by the host: callers never receive
// a map they can use to change the labels that were authorized.
type Labels map[string]string

// Clone returns a deep copy of the label set.
func (l Labels) Clone() Labels {
	if l == nil {
		return nil
	}
	out := make(Labels, len(l))
	for k, v := range l {
		out[k] = v
	}
	return out
}

// Equal reports whether two label sets contain exactly the same entries.
func (l Labels) Equal(other Labels) bool {
	if len(l) != len(other) {
		return false
	}
	for k, v := range l {
		if other[k] != v {
			return false
		}
	}
	return true
}

// Validate checks label syntax and size limits. It accepts nil and empty sets.
func (l Labels) Validate() error {
	if len(l) > MaxLabels {
		return &InvalidLabelError{Reason: fmt.Sprintf("too many labels: %d > %d", len(l), MaxLabels)}
	}
	for k, v := range l {
		if err := validateLabelKey(k); err != nil {
			return err
		}
		if len(v) > MaxLabelValueBytes {
			return &InvalidLabelError{
				Key:    k,
				Reason: fmt.Sprintf("value exceeds %d bytes", MaxLabelValueBytes),
			}
		}
		if !utf8.ValidString(v) {
			return &InvalidLabelError{Key: k, Reason: "value is not valid UTF-8"}
		}
		for _, r := range v {
			if unicode.IsControl(r) {
				return &InvalidLabelError{Key: k, Reason: "value contains a control character"}
			}
		}
	}
	return nil
}

func validateLabelKey(k string) error {
	if k == "" {
		return &InvalidLabelError{Reason: "empty key"}
	}
	if len(k) > MaxLabelKeyBytes {
		return &InvalidLabelError{Key: k, Reason: fmt.Sprintf("key exceeds %d bytes", MaxLabelKeyBytes)}
	}
	if !utf8.ValidString(k) {
		return &InvalidLabelError{Key: k, Reason: "key is not valid UTF-8"}
	}
	for i, r := range k {
		if i == 0 {
			if !isLabelKeyStart(r) {
				return &InvalidLabelError{Key: k, Reason: "key must start with an ASCII letter or digit"}
			}
			continue
		}
		if !isLabelKeyPart(r) {
			return &InvalidLabelError{Key: k, Reason: "key contains an invalid character"}
		}
	}
	return nil
}

func isLabelKeyStart(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

func isLabelKeyPart(r rune) bool {
	if isLabelKeyStart(r) {
		return true
	}
	switch r {
	case '.', '_', ':', '/', '-':
		return true
	default:
		return false
	}
}

// Canonical returns a deterministic, length-prefixed encoding of the label
// set. Length prefixes make the encoding unambiguous even when keys or values
// contain delimiters.
func (l Labels) Canonical() []byte {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("netsec.labels.v1\x00")
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(keys)))
	buf.Write(size[:])
	for _, k := range keys {
		v := l[k]
		binary.BigEndian.PutUint32(size[:], uint32(len(k)))
		buf.Write(size[:])
		buf.WriteString(k)
		binary.BigEndian.PutUint32(size[:], uint32(len(v)))
		buf.Write(size[:])
		buf.WriteString(v)
	}
	return buf.Bytes()
}

// Digest computes the digest of the canonical label encoding.
func (l Labels) Digest() LabelDigest {
	return LabelDigest(sha256.Sum256(l.Canonical()))
}

// MergeLabels validates the requested, inherited, and non-overridable label
// sources and produces the final label set.
//
// Precedence is mandatory > requested > inherited. Requested values must not
// contradict a mandatory value: such an attempt is rejected instead of being
// silently narrowed or broadened. Mandatory values also must not contradict an
// inherited value, which catches inconsistent host configuration. Supplying the
// same key and value in more than one source is allowed.
func MergeLabels(inherited, requested, mandatory Labels) (Labels, error) {
	sources := []struct {
		name   string
		labels Labels
	}{
		{name: "requested", labels: requested},
		{name: "inherited", labels: inherited},
		{name: "mandatory", labels: mandatory},
	}
	for _, source := range sources {
		if err := source.labels.Validate(); err != nil {
			return nil, fmt.Errorf("%s labels: %w", source.name, err)
		}
	}

	for k, required := range mandatory {
		if got, ok := requested[k]; ok && got != required {
			return nil, &ImmutableLabelError{
				Key:      k,
				Source:   "requested",
				Value:    got,
				Required: required,
			}
		}
		if got, ok := inherited[k]; ok && got != required {
			return nil, &ImmutableLabelError{
				Key:      k,
				Source:   "inherited",
				Value:    got,
				Required: required,
			}
		}
	}

	final := make(Labels, len(inherited)+len(requested)+len(mandatory))
	for k, v := range inherited {
		final[k] = v
	}
	for k, v := range requested {
		final[k] = v
	}
	for k, v := range mandatory {
		final[k] = v
	}
	if len(final) > MaxLabels {
		return nil, &InvalidLabelError{
			Reason: fmt.Sprintf("merged labels exceed limit: %d > %d", len(final), MaxLabels),
		}
	}
	if err := final.Validate(); err != nil {
		return nil, err
	}
	return final, nil
}

// mergeInherited overlays parent labels on top of base defaults without applying
// mandatory constraints. It is used while resolving inherited labels;
// MergeLabels performs the authoritative validation afterwards.
func mergeInherited(base, parent Labels) Labels {
	out := make(Labels, len(base)+len(parent))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range parent {
		out[k] = v
	}
	return out
}

// ParseLabelPair builds a one-entry label set from a "key=value" string.
func ParseLabelPair(pair string) (Labels, error) {
	k, v, ok := strings.Cut(pair, "=")
	if !ok || k == "" {
		return nil, &InvalidLabelError{Reason: "label pair must be key=value"}
	}
	labels := Labels{k: v}
	if err := labels.Validate(); err != nil {
		return nil, err
	}
	return labels, nil
}
