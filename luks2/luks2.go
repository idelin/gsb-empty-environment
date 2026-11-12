// Package luks2 validates LUKS2 on-disk metadata.
//
// It treats header metadata as attacker-controlled input: duplicate JSON
// object keys are rejected, keyslot storage areas are bounds- and
// overlap-checked, digest/keyslot/segment bindings must be consistent in
// both directions, PBKDF parameters are capped by caller-supplied limits,
// and the binary header checksum is recomputed after zeroing the checksum
// field.
package luks2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// TypeRetained marks a recycled keyslot, digest or segment. Retained entries
// hold no material and are exempt from area, KDF and binding checks.
const TypeRetained = "retained"

// Sentinel errors. Callers can use errors.Is to classify failures.
var (
	// ErrDuplicateKey is returned when a JSON object defines the same key twice.
	ErrDuplicateKey = errors.New("luks2: duplicate JSON object key")
	// ErrMalformedJSON is returned when the metadata area is not valid JSON.
	ErrMalformedJSON = errors.New("luks2: malformed metadata JSON")
	// ErrMalformedMetadata is returned when a required metadata field is missing or invalid.
	ErrMalformedMetadata = errors.New("luks2: malformed metadata")
	// ErrAreaOutOfBounds is returned when a keyslot area escapes the keyslots area.
	ErrAreaOutOfBounds = errors.New("luks2: keyslot area out of bounds")
	// ErrAreaOverlap is returned when two keyslot areas overlap.
	ErrAreaOverlap = errors.New("luks2: overlapping keyslot areas")
	// ErrDanglingReference is returned when a digest points at a missing entry.
	ErrDanglingReference = errors.New("luks2: dangling digest reference")
	// ErrBindingInconsistent is returned when the reverse bindings are incomplete.
	ErrBindingInconsistent = errors.New("luks2: inconsistent digest bindings")
	// ErrKDFLimit is returned when a PBKDF parameter exceeds the caller limit.
	ErrKDFLimit = errors.New("luks2: PBKDF parameter exceeds limit")
)

// uint64Field accepts both JSON numbers and the quoted decimal strings that
// LUKS2 uses for offsets and sizes (for example "16777216").
type uint64Field uint64

func (f *uint64Field) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		raw = strings.TrimSpace(s)
	}
	if raw == "" {
		return errors.New("empty numeric field")
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid numeric field %q: %w", raw, err)
	}
	*f = uint64Field(n)
	return nil
}

// Area describes the on-disk storage region of a keyslot.
type Area struct {
	Type       string      `json:"type"`
	Offset     uint64Field `json:"offset"`
	Size       uint64Field `json:"size"`
	Encryption string      `json:"encryption"`
	KeySize    uint64Field `json:"key_size"`
}

// KDF holds the password-based key derivation parameters of a keyslot.
// Argon2 uses Time, Memory and CPUs; PBKDF2 uses Iterations.
type KDF struct {
	Type       string      `json:"type"`
	Time       uint64Field `json:"time"`
	Memory     uint64Field `json:"memory"`
	CPUs       uint64Field `json:"cpus"`
	Iterations uint64Field `json:"iterations"`
	Salt       string      `json:"salt"`
	Hash       string      `json:"hash"`
}

// Keyslot is one entry of the "keyslots" object.
type Keyslot struct {
	Type     string          `json:"type"`
	KeySize  uint64Field     `json:"key_size"`
	Area     *Area           `json:"area"`
	KDF      *KDF            `json:"kdf"`
	AF       json.RawMessage `json:"af"`
	Priority json.RawMessage `json:"priority"`
}

// Digest is one entry of the "digests" object.
type Digest struct {
	Type       string      `json:"type"`
	Keyslots   []string    `json:"keyslots"`
	Segments   []string    `json:"segments"`
	Salt       string      `json:"salt"`
	Digest     string      `json:"digest"`
	Hash       string      `json:"hash"`
	Iterations uint64Field `json:"iterations"`
}

// Segment is one entry of the "segments" object. Size may be a number or the
// string "dynamic", so it is retained as raw JSON.
type Segment struct {
	Type       string          `json:"type"`
	Offset     uint64Field     `json:"offset"`
	Size       json.RawMessage `json:"size"`
	Encryption string          `json:"encryption"`
	SectorSize uint64Field     `json:"sector_size"`
	IVTweak    uint64Field     `json:"iv_tweak"`
}

// Header models the LUKS2 JSON metadata. Unknown top-level objects such as
// tokens and config are retained but not semantically validated.
type Header struct {
	Keyslots map[string]Keyslot `json:"keyslots"`
	Digests  map[string]Digest  `json:"digests"`
	Segments map[string]Segment `json:"segments"`
	Tokens   json.RawMessage    `json:"tokens"`
	Config   json.RawMessage    `json:"config"`
}

// KDFLimits caps PBKDF cost factors. A zero field means the corresponding
// parameter is not limited. Memory uses the LUKS2 unit (KiB).
type KDFLimits struct {
	MaxTime       uint64
	MaxMemory     uint64
	MaxCPUs       uint64
	MaxIterations uint64
}

// ParseMetadata strictly decodes the LUKS2 JSON metadata. A JSON object that
// repeats any key, at any nesting depth, is rejected outright instead of
// silently accepting the last occurrence.
func ParseMetadata(data []byte) (*Header, error) {
	payload := bytes.TrimRight(data, "\x00")
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, fmt.Errorf("%w: empty metadata area", ErrMalformedJSON)
	}
	if err := rejectDuplicateKeys(payload); err != nil {
		return nil, err
	}
	var header Header
	if err := json.Unmarshal(payload, &header); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	return &header, nil
}

// rejectDuplicateKeys walks every JSON token and rejects objects containing
// repeated keys, including objects nested inside arrays.
func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSON(dec, nil); err != nil {
		return err
	}
	for _, b := range data[dec.InputOffset():] {
		switch b {
		case ' ', '\t', '\n', '\r':
		default:
			return fmt.Errorf("%w: trailing data after metadata JSON", ErrMalformedJSON)
		}
	}
	return nil
}

func scanJSON(dec *json.Decoder, path []string) error {
	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%w: unexpected end of JSON", ErrMalformedJSON)
		}
		return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("%w: expected object key", ErrMalformedJSON)
			}
			if seen[key] {
				return fmt.Errorf("%w: %s", ErrDuplicateKey, joinPath(path, key))
			}
			seen[key] = true
			if err := scanJSON(dec, append(path, key)); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
		}
	case '[':
		index := 0
		for dec.More() {
			if err := scanJSON(dec, append(path, "["+strconv.Itoa(index)+"]")); err != nil {
				return err
			}
			index++
		}
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
		}
	}
	return nil
}

func joinPath(path []string, key string) string {
	if len(path) == 0 {
		return "$." + key
	}
	return "$." + strings.Join(path, ".") + "." + key
}

func sortedKeys[V any](m map[string]V) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
