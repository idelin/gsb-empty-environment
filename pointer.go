package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// PointerVersion is the only accepted LFS pointer version.
	PointerVersion = "https://git-lfs.github.com/spec/v1"
	// MaxPointerSize is the maximum accepted pointer size in bytes.
	MaxPointerSize = 1024

	sha256Prefix = "sha256:"
)

var (
	// ErrInvalidPointer marks a pointer that is not in canonical LFS form.
	ErrInvalidPointer = errors.New("invalid LFS pointer")
	// ErrPointerTooLarge marks a pointer larger than MaxPointerSize.
	ErrPointerTooLarge = errors.New("LFS pointer exceeds 1024 bytes")
	// ErrOIDNotAuthorized marks an object that is not authorized for the
	// repository whose OID set was supplied to ValidatePointer.
	ErrOIDNotAuthorized = errors.New("LFS oid is not authorized for this repository")
)

// Pointer is a validated Git LFS pointer. OID contains the algorithm prefix,
// for example "sha256:0123...".
type Pointer struct {
	Version string
	OID     string
	Size    int64
}

// OIDSet is the set of object IDs authorized in the current repository.
type OIDSet map[string]struct{}

// NewOIDSet builds an authorization set from fully qualified OIDs.
func NewOIDSet(oids ...string) OIDSet {
	set := make(OIDSet, len(oids))
	for _, oid := range oids {
		set[oid] = struct{}{}
	}
	return set
}

// Contains reports whether oid is in the set. A nil set contains nothing.
func (s OIDSet) Contains(oid string) bool {
	_, ok := s[oid]
	return ok
}

// ReadPointer reads at most MaxPointerSize+1 bytes and parses them as an LFS
// pointer. It does not perform repository authorization.
func ReadPointer(r io.Reader) (Pointer, error) {
	if r == nil {
		return Pointer{}, fmt.Errorf("%w: nil reader", ErrInvalidPointer)
	}

	data, err := io.ReadAll(io.LimitReader(r, MaxPointerSize+1))
	if err != nil {
		return Pointer{}, fmt.Errorf("read LFS pointer: %w", err)
	}
	return ParsePointer(data)
}

// ParsePointer validates the canonical three-line LFS pointer format. No
// repository authorization is performed; use ValidatePointer for that.
func ParsePointer(data []byte) (Pointer, error) {
	if len(data) > MaxPointerSize {
		return Pointer{}, ErrPointerTooLarge
	}
	if len(data) == 0 {
		return Pointer{}, fmt.Errorf("%w: empty pointer", ErrInvalidPointer)
	}
	if bytes.IndexByte(data, '\r') >= 0 {
		return Pointer{}, fmt.Errorf("%w: CR line endings are not allowed", ErrInvalidPointer)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return Pointer{}, fmt.Errorf("%w: NUL byte", ErrInvalidPointer)
	}

	text := string(data)
	if strings.HasSuffix(text, "\n") {
		text = strings.TrimSuffix(text, "\n")
	}
	if text == "" {
		return Pointer{}, fmt.Errorf("%w: empty pointer", ErrInvalidPointer)
	}

	lines := strings.Split(text, "\n")
	if len(lines) != 3 {
		return Pointer{}, fmt.Errorf("%w: expected exactly 3 lines, got %d", ErrInvalidPointer, len(lines))
	}

	version, err := parseField(lines[0], "version")
	if err != nil {
		return Pointer{}, err
	}
	if version != PointerVersion {
		return Pointer{}, fmt.Errorf("%w: unsupported version %q", ErrInvalidPointer, version)
	}

	oid, err := parseField(lines[1], "oid")
	if err != nil {
		return Pointer{}, err
	}
	if !validOID(oid) {
		return Pointer{}, fmt.Errorf("%w: malformed sha256 oid %q", ErrInvalidPointer, oid)
	}

	rawSize, err := parseField(lines[2], "size")
	if err != nil {
		return Pointer{}, err
	}
	size, err := parseSize(rawSize)
	if err != nil {
		return Pointer{}, err
	}

	return Pointer{
		Version: version,
		OID:     oid,
		Size:    size,
	}, nil
}

// ValidatePointer parses an LFS pointer and requires its OID to belong to the
// authorization set for the current repository.
func ValidatePointer(data []byte, allowed OIDSet) (Pointer, error) {
	pointer, err := ParsePointer(data)
	if err != nil {
		return Pointer{}, err
	}
	if !allowed.Contains(pointer.OID) {
		return Pointer{}, fmt.Errorf("%w: %s", ErrOIDNotAuthorized, pointer.OID)
	}
	return pointer, nil
}

func parseField(line, key string) (string, error) {
	prefix := key + " "
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("%w: field %q must be %q", ErrInvalidPointer, key, prefix)
	}

	value := strings.TrimPrefix(line, prefix)
	if value == "" {
		return "", fmt.Errorf("%w: empty %s value", ErrInvalidPointer, key)
	}
	if strings.ContainsAny(value, " \t") {
		return "", fmt.Errorf("%w: whitespace in %s value", ErrInvalidPointer, key)
	}
	return value, nil
}

func parseSize(value string) (int64, error) {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("%w: size is not a decimal integer", ErrInvalidPointer)
		}
	}
	if len(value) > 1 && value[0] == '0' {
		return 0, fmt.Errorf("%w: size has a leading zero", ErrInvalidPointer)
	}

	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: size is out of range", ErrInvalidPointer)
	}
	return size, nil
}

func validOID(oid string) bool {
	const digestHexLength = 64

	if !strings.HasPrefix(oid, sha256Prefix) {
		return false
	}
	digest := strings.TrimPrefix(oid, sha256Prefix)
	if len(digest) != digestHexLength {
		return false
	}
	for i := 0; i < len(digest); i++ {
		if !isLowerHex(digest[i]) {
			return false
		}
	}
	return true
}

func isLowerHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f')
}
