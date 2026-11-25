// Package namecrypt implements an encrypted file-name layer for a filesystem
// host. Plaintext names are sealed with an AEAD and stored behind keyed
// HMAC lookup keys, while directory policies control case folding, length
// limits and key generation.
package namecrypt

import (
	"bytes"
	"errors"
	"unicode/utf8"
)

// CaseMode selects how plaintext names are canonicalized inside a directory.
type CaseMode uint8

const (
	// CaseSensitive keeps the original UTF-8 bytes unchanged.
	CaseSensitive CaseMode = iota
	// CaseFoldASCII maps only A-Z to a-z; every other byte is preserved.
	CaseFoldASCII
)

// DirectoryID identifies a directory. It is part of the name key derivation,
// so a name sealed in one directory can never be reused as an entry in another.
type DirectoryID string

// InodeID identifies the inode that a directory entry points at.
type InodeID string

// NonceSize is the required length, in bytes, of the per-directory nonce.
const NonceSize = 32

// EntryNonceSize is the required length, in bytes, of the per-entry nonce.
const EntryNonceSize = 16

// LookupKeySize is the length of the on-disk lookup key.
const LookupKeySize = 20

// Policy is the immutable directory policy. The only field that may ever
// change is KeyGeneration, which moves forward exclusively through RotateKey.
// DirectoryNonce and every other field stay fixed for the directory lifetime.
type Policy struct {
	DirectoryID       DirectoryID
	KeyGeneration     uint64
	DirectoryNonce    [NonceSize]byte
	CaseMode          CaseMode
	MaxRawNameBytes   int
	MaxCanonicalBytes int
}

var (
	// ErrInvalidName means the plaintext name failed validation.
	ErrInvalidName = errors.New("namecrypt: invalid plaintext name")
	// ErrInvalidPolicy means a directory policy is malformed.
	ErrInvalidPolicy = errors.New("namecrypt: invalid directory policy")
	// ErrNameTooLong means a name or its canonical form exceeds policy limits.
	ErrNameTooLong = errors.New("namecrypt: name exceeds policy byte limit")
	// ErrNotFound means no entry matches the requested name.
	ErrNotFound = errors.New("namecrypt: entry not found")
	// ErrExists means an entry with the same canonical name is already present.
	ErrExists = errors.New("namecrypt: entry already exists")
	// ErrCanonicalConflict means two distinct raw names map to one canonical
	// name (e.g. "Abc" and "abc" under CaseFoldASCII).
	ErrCanonicalConflict = errors.New("namecrypt: canonical name collision with a different raw name")
	// ErrBadRecord means a stored record is corrupt, from an unknown generation
	// or fails authenticated decryption.
	ErrBadRecord = errors.New("namecrypt: corrupt or unauthenticatable directory record")
	// ErrInodeMismatch means the decrypted entry does not name the expected inode.
	ErrInodeMismatch = errors.New("namecrypt: decrypted entry does not match requested inode identity")
	// ErrRotationInProgress means the directory is mid key-rotation and cannot
	// accept creates or incoming renames until the rotation finishes.
	ErrRotationInProgress = errors.New("namecrypt: key rotation in progress")
)

// Validate rejects malformed directory policies.
func (p Policy) Validate() error {
	if p.DirectoryID == "" {
		return ErrInvalidPolicy
	}
	if p.CaseMode != CaseSensitive && p.CaseMode != CaseFoldASCII {
		return ErrInvalidPolicy
	}
	if p.MaxRawNameBytes <= 0 || p.MaxCanonicalBytes <= 0 {
		return ErrInvalidPolicy
	}
	var zero [NonceSize]byte
	if p.DirectoryNonce == zero {
		return ErrInvalidPolicy
	}
	return nil
}

// ValidateName enforces the plaintext rules: valid UTF-8, non-empty, no slash
// or NUL byte, and not a "." / ".." dot segment.
func ValidateName(name string) error {
	if name == "" {
		return ErrInvalidName
	}
	if !utf8.ValidString(name) {
		return ErrInvalidName
	}
	if name == "." || name == ".." {
		return ErrInvalidName
	}
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case 0, '/':
			return ErrInvalidName
		}
	}
	return nil
}

// Canonicalize returns the canonical form of name. Under CaseSensitive the
// original UTF-8 bytes are returned. Under CaseFoldASCII only ASCII A-Z are
// folded to a-z; non-ASCII bytes are copied byte for byte.
func Canonicalize(name string, mode CaseMode) string {
	if mode != CaseFoldASCII {
		return name
	}
	var out bytes.Buffer
	out.Grow(len(name))
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		out.WriteByte(b)
	}
	return out.String()
}

// Canonical validates name against p and returns its canonical form.
func (p Policy) Canonical(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if len(name) > p.MaxRawNameBytes {
		return "", ErrNameTooLong
	}
	canonical := Canonicalize(name, p.CaseMode)
	if len(canonical) > p.MaxCanonicalBytes {
		return "", ErrNameTooLong
	}
	return canonical, nil
}
