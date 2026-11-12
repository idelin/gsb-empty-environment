package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"unicode/utf8"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// foreignBOMs are byte-order marks for encodings that this package does not
// accept. Four-byte signatures must be listed before their two-byte prefixes
// so a UTF-32 LE BOM is not misreported as UTF-16 LE.
var foreignBOMs = []struct {
	name   string
	prefix []byte
}{
	{"UTF-32 BE", []byte{0x00, 0x00, 0xFE, 0xFF}},
	{"UTF-32 LE", []byte{0xFF, 0xFE, 0x00, 0x00}},
	{"UTF-32 BE (2143)", []byte{0xFE, 0xFF, 0x00, 0x00}},
	{"UTF-32 LE (3412)", []byte{0x00, 0x00, 0xFF, 0xFE}},
	{"UTF-16 BE", []byte{0xFE, 0xFF}},
	{"UTF-16 LE", []byte{0xFF, 0xFE}},
}

// Normalized is policy source after the one permitted leading UTF-8 BOM has
// been removed. Bytes is an owned copy of the exact input to the digest.
type Normalized struct {
	Bytes []byte
	Hash  [sha256.Size]byte
}

// DigestHex returns the SHA-256 of Bytes as lowercase hexadecimal.
func (n Normalized) DigestHex() string {
	return hex.EncodeToString(n.Hash[:])
}

// BOMError reports a disallowed byte-order mark.
type BOMError struct {
	Encoding string
	Offset   int
}

func (e *BOMError) Error() string {
	return fmt.Sprintf("policy: %s BOM at byte %d is not allowed; only one leading UTF-8 BOM may be stripped", e.Encoding, e.Offset)
}

// Normalize validates BOM placement and returns the exact bytes that must be
// hashed and tokenized.
//
// The only accepted normalization is removal of one UTF-8 BOM at offset zero.
// After that single removal the remaining bytes are scanned in full. This
// rejects a second leading BOM, a BOM at a line start or inside a key, and all
// UTF-16/UTF-32 BOM signatures. The digest covers the stripped bytes only.
func Normalize(src []byte) (Normalized, error) {
	body := src
	stripped := 0
	if bytes.HasPrefix(src, utf8BOM) {
		body = src[len(utf8BOM):]
		stripped = len(utf8BOM)
	}

	if offset, encoding := findBOM(body); offset >= 0 {
		return Normalized{}, &BOMError{
			Encoding: encoding,
			Offset:   stripped + offset,
		}
	}
	if !utf8.Valid(body) {
		return Normalized{}, fmt.Errorf("policy: source is not valid UTF-8")
	}

	// Copy so callers cannot mutate the bytes that produced Hash.
	normalized := append([]byte(nil), body...)
	return Normalized{
		Bytes: normalized,
		Hash:  sha256.Sum256(normalized),
	}, nil
}

func findBOM(body []byte) (int, string) {
	for offset := 0; offset < len(body); offset++ {
		rest := body[offset:]
		if bytes.HasPrefix(rest, utf8BOM) {
			return offset, "UTF-8"
		}
		for _, bom := range foreignBOMs {
			if bytes.HasPrefix(rest, bom.prefix) {
				return offset, bom.name
			}
		}
	}
	return -1, ""
}
