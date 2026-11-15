package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// PolicyVersion is the fscrypt policy format version.
type PolicyVersion uint32

const (
	PolicyVersionV1 PolicyVersion = 1
	PolicyVersionV2 PolicyVersion = 2
)

// ContentsMode is the encryption mode used for file contents.
type ContentsMode string

const (
	ContentsModeAES256XTS      ContentsMode = "AES-256-XTS"
	ContentsModeAES256CTS      ContentsMode = "AES-256-CTS-CBC"
	ContentsModeAES128CBCESSIV ContentsMode = "AES-128-CBC-ESSIV"
	ContentsModeAES128CTS      ContentsMode = "AES-128-CTS-CBC"
	ContentsModeAdiantum       ContentsMode = "Adiantum"
	ContentsModeAES256HCTR2    ContentsMode = "AES-256-HCTR2"
)

// FilenamesMode is the encryption mode used for directory entry names.
type FilenamesMode string

const (
	FilenamesModeAES256CTS   FilenamesMode = "AES-256-CTS-CBC"
	FilenamesModeAES128CTS   FilenamesMode = "AES-128-CTS-CBC"
	FilenamesModeAdiantum    FilenamesMode = "Adiantum"
	FilenamesModeAES256HCTR2 FilenamesMode = "AES-256-HCTR2"
)

// Flags contains the fscrypt policy flags. Flags are compared as part of the
// complete policy identity.
type Flags uint32

const (
	FlagDirectKey Flags = 1 << iota
	FlagIVInodeLblk64
	FlagIVInodeLblk32
	FlagPad4
	FlagNoKey
)

// Policy is one complete fscrypt policy.
//
// Policy is comparable. In particular, two policies are interchangeable only
// when every field is equal; sharing a master key identifier is not enough.
type Policy struct {
	Version             PolicyVersion
	MasterKeyIdentifier string
	ContentsMode        ContentsMode
	FilenamesMode       FilenamesMode
	Flags               Flags
}

// Equal reports whether p and other describe exactly the same policy.
func (p Policy) Equal(other Policy) bool {
	return p == other
}

// Validate checks the fields that identify a usable policy. The mode values
// are treated as opaque identifiers, so callers may use modes added by newer
// kernels without changing this package.
func (p Policy) Validate() error {
	switch p.Version {
	case PolicyVersionV1, PolicyVersionV2:
	default:
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidPolicy, p.Version)
	}
	if strings.TrimSpace(p.MasterKeyIdentifier) == "" {
		return fmt.Errorf("%w: empty master key identifier", ErrInvalidPolicy)
	}
	if strings.TrimSpace(string(p.ContentsMode)) == "" {
		return fmt.Errorf("%w: empty contents mode", ErrInvalidPolicy)
	}
	if strings.TrimSpace(string(p.FilenamesMode)) == "" {
		return fmt.Errorf("%w: empty filenames mode", ErrInvalidPolicy)
	}
	return nil
}

func clonePolicy(p *Policy) *Policy {
	if p == nil {
		return nil
	}
	cloned := *p
	return &cloned
}

func policiesEqual(a, b *Policy) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

var (
	// ErrNotExist means that a path component does not exist.
	ErrNotExist = errors.New("fscrypt: path does not exist")
	// ErrExist means that a directory entry already exists.
	ErrExist = errors.New("fscrypt: path already exists")
	// ErrNotDirectory means that an operation required a directory.
	ErrNotDirectory = errors.New("fscrypt: not a directory")
	// ErrIsDirectory means that an operation cannot be applied to a directory.
	ErrIsDirectory = errors.New("fscrypt: is a directory")
	// ErrInvalidPath means that a path or directory entry name is invalid.
	ErrInvalidPath = errors.New("fscrypt: invalid path")
	// ErrInvalidPolicy means that a policy is incomplete or unsupported.
	ErrInvalidPolicy = errors.New("fscrypt: invalid policy")
	// ErrPolicyAlreadySet means that a directory already has a policy.
	ErrPolicyAlreadySet = errors.New("fscrypt: directory already has a policy")
	// ErrNotEmpty means that a directory is not empty.
	ErrNotEmpty = errors.New("fscrypt: directory is not empty")
	// ErrPolicyMismatch means that the source and destination policies differ,
	// or that an item's policy differs from the destination directory.
	ErrPolicyMismatch = errors.New("fscrypt: policy mismatch")
	// ErrPlaintextIntoEncrypted means that a plaintext item would enter an
	// encrypted directory.
	ErrPlaintextIntoEncrypted = errors.New("fscrypt: plaintext item cannot enter an encrypted directory")
	// ErrEncryptedIntoPlaintext means that an encrypted item would enter a
	// plaintext directory.
	ErrEncryptedIntoPlaintext = errors.New("fscrypt: encrypted item cannot enter a plaintext directory")
	// ErrInvalidMove means that a rename or exchange would create a cycle or
	// otherwise move a directory below itself.
	ErrInvalidMove = errors.New("fscrypt: invalid directory move")
)
