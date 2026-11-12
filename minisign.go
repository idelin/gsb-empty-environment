// Package netsec verifies minisign-signed self-update packages.
//
// A minisign signature file contains:
//
//	untrusted comment: <free-form, NOT authenticated by the file signature>
//	<base64 signature line>
//	trusted comment: <authenticated by the global signature>
//	<base64 global signature line>
//
// The untrusted comment is never covered by any signature and must only be
// used for display. Update decisions (target file name, release timestamp and
// version) are taken exclusively from the trusted comment, whose integrity is
// guaranteed by the global signature. This prevents an attacker from rewriting
// version/timestamp/file fields to roll a client back to a vulnerable release.
package netsec

import (
	"bufio"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Algorithm identifiers used in the two-byte signature/pubkey prefix.
const (
	// AlgPrehashed is Ed25519 over BLAKE2b-512(content), the minisign default.
	AlgPrehashed = "ED"
	// AlgLegacy is Ed25519 over the raw content, supported for compatibility.
	AlgLegacy = "Ed"
)

const (
	keyIDLen         = 8
	pubKeyLineLen    = 2 + keyIDLen + ed25519.PublicKeySize
	signatureLineLen = 2 + keyIDLen + ed25519.SignatureSize
	globalSigLineLen = ed25519.SignatureSize
	untrustedPrefix  = "untrusted comment: "
	trustedPrefix    = "trusted comment: "
	maxCommentBytes  = 1 << 20
	maxReaderBytes   = 1 << 40 // 1 TiB sanity bound for streamed content
)

var (
	// ErrMalformedPublicKey is returned when a public key file cannot be parsed.
	ErrMalformedPublicKey = errors.New("netsec: malformed minisign public key")
	// ErrMalformedSignature is returned when a signature file cannot be parsed.
	ErrMalformedSignature = errors.New("netsec: malformed minisign signature")
	// ErrKeyIDMismatch means the signature was not produced by the given key.
	ErrKeyIDMismatch = errors.New("netsec: signature key id does not match public key")
	// ErrUnsupportedAlgorithm means the signature uses an unknown algorithm id.
	ErrUnsupportedAlgorithm = errors.New("netsec: unsupported signature algorithm")
	// ErrBadSignature means the file signature does not verify.
	ErrBadSignature = errors.New("netsec: file signature verification failed")
	// ErrBadGlobalSignature means the trusted-comment global signature does not verify.
	ErrBadGlobalSignature = errors.New("netsec: trusted comment global signature verification failed")
	// ErrMalformedTrustedComment means the trusted comment lacks required fields.
	ErrMalformedTrustedComment = errors.New("netsec: malformed trusted comment")
	// ErrFileNameMismatch means the signed file name differs from the target name.
	ErrFileNameMismatch = errors.New("netsec: signed file name does not match target")
	// ErrRollback means the signed timestamp is older than the last accepted one.
	ErrRollback = errors.New("netsec: signed timestamp is older than the accepted version")
)

// PublicKey is a parsed minisign public key.
type PublicKey struct {
	Algorithm [2]byte
	KeyID     [8]byte
	Key       ed25519.PublicKey
}

// Signature is a parsed minisign signature file.
type Signature struct {
	UntrustedComment string
	Algorithm        [2]byte
	KeyID            [8]byte
	FileSignature    [ed25519.SignatureSize]byte
	TrustedComment   string
	GlobalSignature  [ed25519.SignatureSize]byte
}

// ParsePublicKey parses a minisign public key file, for example:
//
//	untrusted comment: minisign public key <id>
//	<RW... base64>
func ParsePublicKey(r io.Reader) (*PublicKey, error) {
	lines, err := readLines(r, ErrMalformedPublicKey)
	if err != nil {
		return nil, err
	}
	if len(lines) < 2 || !strings.HasPrefix(lines[0], untrustedPrefix) {
		return nil, ErrMalformedPublicKey
	}
	raw, err := decodeLine(lines[1], pubKeyLineLen)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedPublicKey, err)
	}
	pk := &PublicKey{}
	copy(pk.Algorithm[:], raw[0:2])
	copy(pk.KeyID[:], raw[2:10])
	pk.Key = ed25519.PublicKey(append([]byte(nil), raw[10:]...))
	return pk, nil
}

// ParseSignature parses a minisign signature file. The untrusted comment is
// retained for display only; it is never consulted during verification.
func ParseSignature(r io.Reader) (*Signature, error) {
	lines, err := readLines(r, ErrMalformedSignature)
	if err != nil {
		return nil, err
	}
	if len(lines) < 4 ||
		!strings.HasPrefix(lines[0], untrustedPrefix) ||
		!strings.HasPrefix(lines[2], trustedPrefix) {
		return nil, ErrMalformedSignature
	}

	fileLine, err := decodeLine(lines[1], signatureLineLen)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedSignature, err)
	}
	globalLine, err := decodeLine(lines[3], globalSigLineLen)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedSignature, err)
	}

	sig := &Signature{
		UntrustedComment: strings.TrimPrefix(lines[0], untrustedPrefix),
		TrustedComment:   strings.TrimPrefix(lines[2], trustedPrefix),
	}
	copy(sig.Algorithm[:], fileLine[0:2])
	copy(sig.KeyID[:], fileLine[2:10])
	copy(sig.FileSignature[:], fileLine[10:])
	copy(sig.GlobalSignature[:], globalLine)
	return sig, nil
}

// Verify cryptographically checks sig against pk and content.
//
// Both signatures embedded in a minisign signature file are verified:
//
//  1. The file signature over the content (prehashed with BLAKE2b-512 for the
//     default "ED" algorithm; raw for the legacy "Ed" algorithm).
//  2. The global signature over the file signature concatenated with the
//     trusted comment. This is what authenticates the trusted comment.
//
// The untrusted comment is not part of any signed payload and is ignored here.
// Content is streamed and bounded, so large update packages do not have to be
// fully buffered for the default prehashed algorithm.
func Verify(pk *PublicKey, sig *Signature, content io.Reader) error {
	if pk == nil || sig == nil {
		return ErrMalformedSignature
	}
	if sig.KeyID != pk.KeyID {
		return ErrKeyIDMismatch
	}

	var preimage []byte
	switch string(sig.Algorithm[:]) {
	case AlgPrehashed:
		hasher := newBlake2b(64)
		if _, err := io.Copy(hasher, io.LimitReader(content, maxReaderBytes+1)); err != nil {
			return err
		}
		preimage = append(append([]byte{}, sig.Algorithm[:]...), hasher.Sum(nil)...)
	case AlgLegacy:
		data, err := io.ReadAll(io.LimitReader(content, maxReaderBytes+1))
		if err != nil {
			return err
		}
		preimage = append(append([]byte{}, sig.Algorithm[:]...), data...)
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, string(sig.Algorithm[:]))
	}

	if !ed25519.Verify(pk.Key, preimage, sig.FileSignature[:]) {
		return ErrBadSignature
	}

	globalMessage := make([]byte, 0, ed25519.SignatureSize+len(sig.TrustedComment))
	globalMessage = append(globalMessage, sig.FileSignature[:]...)
	globalMessage = append(globalMessage, sig.TrustedComment...)
	if !ed25519.Verify(pk.Key, globalMessage, sig.GlobalSignature[:]) {
		return ErrBadGlobalSignature
	}
	return nil
}

// TrustedComment is the authenticated update metadata carried in the trusted
// comment line. Every field is covered by the global signature.
type TrustedComment struct {
	// File is the exact package file name the signature was issued for.
	File string
	// Timestamp is the release time claimed by the signer.
	Timestamp time.Time
	// Version is the release version claimed by the signer.
	Version string
}

// ParseTrustedComment parses the canonical trusted comment format:
//
//	file=<name>	timestamp=<unix-seconds>	version=<version>
//
// timestamp also accepts RFC 3339 values. All three fields are mandatory and
// must be non-empty; unknown fields are ignored so the format can be extended.
func ParseTrustedComment(comment string) (*TrustedComment, error) {
	tc := &TrustedComment{}
	seen := map[string]bool{}
	for _, pair := range strings.Split(comment, "\t") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || value == "" {
			return nil, fmt.Errorf("%w: %q", ErrMalformedTrustedComment, pair)
		}
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate key %q", ErrMalformedTrustedComment, key)
		}
		seen[key] = true
		switch key {
		case "file":
			tc.File = value
		case "timestamp":
			ts, err := parseTimestamp(value)
			if err != nil {
				return nil, fmt.Errorf("%w: timestamp: %v", ErrMalformedTrustedComment, err)
			}
			tc.Timestamp = ts
		case "version":
			tc.Version = value
		}
	}

	if tc.File == "" || tc.Version == "" || tc.Timestamp.IsZero() {
		return nil, fmt.Errorf("%w: file, timestamp and version are required", ErrMalformedTrustedComment)
	}
	return tc, nil
}

func parseTimestamp(value string) (time.Time, error) {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return time.Time{}, errors.New("negative unix timestamp")
		}
		return time.Unix(seconds, 0).UTC(), nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts.UTC(), nil
	}
	return time.Time{}, errors.New("expected unix seconds or RFC 3339 value")
}

// VerifiedUpdate is the result of a fully validated update package.
type VerifiedUpdate struct {
	// File is the authenticated package file name.
	File string
	// Version is the authenticated release version.
	Version string
	// Timestamp is the authenticated release timestamp.
	Timestamp time.Time
	// UntrustedComment is shown for display only and must never be trusted.
	UntrustedComment string
}

// Verifier enforces minisign signatures and anti-rollback policy for a self
// update stream. The zero value is not usable; create one with NewVerifier.
type Verifier struct {
	pk             *PublicKey
	lastAccepted   time.Time
	lastAcceptedOK bool
}

// NewVerifier creates a Verifier from the signer's public key. acceptedAt is
// the timestamp of the newest release already installed (load it from durable
// local storage); pass the zero time when no release has been recorded yet.
func NewVerifier(pk *PublicKey, acceptedAt time.Time) *Verifier {
	v := &Verifier{pk: pk}
	if !acceptedAt.IsZero() {
		v.lastAccepted = acceptedAt.UTC()
		v.lastAcceptedOK = true
	}
	return v
}

// LastAccepted returns the timestamp of the newest accepted release and
// whether any release has been recorded. Persist this value after each
// successful update so the anti-rollback check survives restarts.
func (v *Verifier) LastAccepted() (time.Time, bool) {
	return v.lastAccepted, v.lastAcceptedOK
}

// VerifyUpdate runs the full download-completion check:
//
//  1. Verify the file signature and the trusted-comment global signature.
//  2. Parse update metadata solely from the authenticated trusted comment.
//  3. Require the signed file name to equal targetFile (the download target).
//  4. Reject any signed timestamp older than the last accepted release.
//
// On success the verifier advances its anti-rollback watermark to the signed
// timestamp; the caller should only then install the package and persist the
// watermark returned by LastAccepted.
func (v *Verifier) VerifyUpdate(targetFile string, sig *Signature, content io.Reader) (*VerifiedUpdate, error) {
	if v == nil || v.pk == nil {
		return nil, ErrMalformedPublicKey
	}
	if err := Verify(v.pk, sig, content); err != nil {
		return nil, err
	}

	comment, err := ParseTrustedComment(sig.TrustedComment)
	if err != nil {
		return nil, err
	}
	if comment.File != targetFile {
		return nil, fmt.Errorf("%w: signed %q, target %q", ErrFileNameMismatch, comment.File, targetFile)
	}
	if v.lastAcceptedOK && comment.Timestamp.Before(v.lastAccepted) {
		return nil, fmt.Errorf("%w: signed %s, accepted %s", ErrRollback,
			comment.Timestamp.Format(time.RFC3339), v.lastAccepted.Format(time.RFC3339))
	}

	v.lastAccepted = comment.Timestamp
	v.lastAcceptedOK = true
	return &VerifiedUpdate{
		File:             comment.File,
		Version:          comment.Version,
		Timestamp:        comment.Timestamp,
		UntrustedComment: sig.UntrustedComment,
	}, nil
}

// readLines returns the non-empty lines of r with a trailing CR stripped.
func readLines(r io.Reader, empty error) ([]string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, maxCommentBytes+1))
	scanner.Buffer(make([]byte, 0, 4096), maxCommentBytes)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, empty
	}
	return lines, nil
}

// decodeLine base64-decodes one minisign line and enforces its exact length.
func decodeLine(line string, wantLen int) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil {
		return nil, err
	}
	if len(raw) != wantLen {
		return nil, fmt.Errorf("decoded line has %d bytes, want %d", len(raw), wantLen)
	}
	return raw, nil
}
