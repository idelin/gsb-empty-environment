package policy

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

// Line is one normalized logical line. Number is one-based. Text excludes the
// line terminator and a trailing carriage return from CRLF input.
type Line struct {
	Number int
	Text   string
}

// Document is the verifier-side view of a policy file: normalized bytes,
// their digest, and the lines that may be read. It never contains a BOM.
type Document struct {
	Bytes []byte
	Hash  [32]byte
	Lines []Line
}

// DigestHex returns the SHA-256 of the normalized bytes.
func (d *Document) DigestHex() string {
	return hex.EncodeToString(d.Hash[:])
}

// Lex normalizes source and splits it into lines. This is the entry point for
// signature verification: the digest and line reading use the same bytes.
func Lex(src []byte) (*Document, error) {
	normalized, err := Normalize(src)
	if err != nil {
		return nil, err
	}
	return &Document{
		Bytes: normalized.Bytes,
		Hash:  normalized.Hash,
		Lines: splitLines(normalized.Bytes),
	}, nil
}

// LexFile reads and lexes a policy file.
func LexFile(path string) (*Document, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("policy: read %s: %w", path, err)
	}
	return Lex(src)
}

func splitLines(src []byte) []Line {
	if len(src) == 0 {
		return nil
	}

	parts := bytes.Split(src, []byte{'\n'})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}

	lines := make([]Line, len(parts))
	for i, part := range parts {
		text := strings.TrimSuffix(string(part), "\r")
		lines[i] = Line{Number: i + 1, Text: text}
	}
	return lines
}
