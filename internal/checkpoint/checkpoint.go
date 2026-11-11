// Package checkpoint defines signed transparent-log tree heads ("checkpoints")
// and their verification.
//
// A checkpoint binds an origin string, a tree size and a tree root together and
// authenticates them with an Ed25519 signature. The wire format is a compact,
// human-readable text envelope so a signed checkpoint can be stored verbatim as
// tamper evidence and re-verified at any time, fully offline.
//
// Envelope layout (all lines terminated by '\n'):
//
//	<origin>
//	<tree size in decimal>
//	<base64(root hash, 32 bytes)>
//
//	— <key name> <base64(64-byte Ed25519 signature)>
//
// The signature is computed over exactly the three header lines (including
// their newlines), never over the signature line itself.
package checkpoint

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"example.org/offlinecli/internal/tlog"
)

// Origin identifies the log a checkpoint belongs to. A checkpoint from one
// origin can never be accepted as a head for a different origin.
type Origin string

// Checkpoint is a parsed, signer-agnostic tree head.
type Checkpoint struct {
	Origin Origin
	Size   int64
	Hash   tlog.Hash
}

// header renders the three canonical, signed header lines.
func (c Checkpoint) header() []byte {
	return []byte(fmt.Sprintf("%s\n%d\n%s\n", c.Origin, c.Size, base64.StdEncoding.EncodeToString(c.Hash[:])))
}

// Envelope is a signed checkpoint. Text is the exact bytes accepted or
// produced by a Signer, including the signature line, so it can be persisted
// byte-for-byte as evidence.
type Envelope struct {
	Checkpoint
	Text []byte
}

// Signer signs checkpoints with a single Ed25519 key.
type Signer struct {
	name      string
	origin    Origin
	key       ed25519.PrivateKey
	publicKey ed25519.PublicKey
}

// NewSigner wraps an Ed25519 private key for the given origin and key name.
func NewSigner(name string, origin Origin, key ed25519.PrivateKey) (*Signer, error) {
	if name == "" {
		return nil, errors.New("checkpoint: empty key name")
	}
	if strings.ContainsAny(name, " \n\t") {
		return nil, errors.New("checkpoint: key name must not contain whitespace")
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("checkpoint: invalid Ed25519 private key")
	}
	return &Signer{name: name, origin: origin, key: key, publicKey: key.Public().(ed25519.PublicKey)}, nil
}

// Origin reports the origin this signer is bound to.
func (s *Signer) Origin() Origin { return s.origin }

// Name reports the verifier key name embedded in produced envelopes.
func (s *Signer) Name() string { return s.name }

// PublicKey returns the Ed25519 public key verifiers must trust.
func (s *Signer) PublicKey() ed25519.PublicKey {
	out := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(out, s.publicKey)
	return out
}

// Sign produces a signed envelope for the given tree head. The origin is taken
// from the signer so a key can never sign a head for a foreign log.
func (s *Signer) Sign(size int64, hash tlog.Hash) (*Envelope, error) {
	if size < 1 {
		return nil, errors.New("checkpoint: tree size must be positive")
	}
	cp := Checkpoint{Origin: s.origin, Size: size, Hash: hash}
	header := cp.header()
	sig := ed25519.Sign(s.key, header)
	text := append([]byte{}, header...)
	text = append(text, '\n')
	text = append(text, []byte("— "+s.name+" "+base64.StdEncoding.EncodeToString(sig)+"\n")...)
	return &Envelope{Checkpoint: cp, Text: text}, nil
}

// Verifier checks envelopes against a trusted Ed25519 key and origin.
type Verifier struct {
	name      string
	origin    Origin
	publicKey ed25519.PublicKey
}

// NewVerifier builds a verifier bound to one origin and one trusted key name.
func NewVerifier(name string, origin Origin, publicKey ed25519.PublicKey) (*Verifier, error) {
	if name == "" {
		return nil, errors.New("checkpoint: empty key name")
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("checkpoint: invalid Ed25519 public key")
	}
	pk := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(pk, publicKey)
	return &Verifier{name: name, origin: origin, publicKey: pk}, nil
}

// Origin reports the origin this verifier is bound to.
func (v *Verifier) Origin() Origin { return v.origin }

// Parse verifies the envelope's signature and returns its tree head. It rejects
// foreign origins, unknown keys, malformed text and invalid signatures.
func (v *Verifier) Parse(text []byte) (*Envelope, error) {
	if len(text) > 1<<20 {
		return nil, errors.New("checkpoint: envelope too large")
	}

	// Split the signed header from the signature block on the blank line.
	sep := []byte("\n\n")
	idx := bytes.Index(text, sep)
	if idx < 0 {
		return nil, errors.New("checkpoint: malformed envelope: missing signature block")
	}
	header := text[:idx+1] // retain the single trailing newline of the header
	sigBlock := text[idx+len(sep):]

	cp, err := parseHeader(header)
	if err != nil {
		return nil, err
	}
	if cp.Origin != v.origin {
		return nil, fmt.Errorf("checkpoint: origin mismatch: got %q want %q", cp.Origin, v.origin)
	}

	sig, err := v.parseSignature(sigBlock)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(v.publicKey, header, sig) {
		return nil, errors.New("checkpoint: signature verification failed")
	}

	return &Envelope{Checkpoint: cp, Text: append([]byte{}, text...)}, nil
}

// parseHeader parses and validates the three signed header lines.
func parseHeader(header []byte) (Checkpoint, error) {
	// header must end in exactly one newline and contain three lines.
	if len(header) == 0 || header[len(header)-1] != '\n' {
		return Checkpoint{}, errors.New("checkpoint: malformed header")
	}
	body := string(header[:len(header)-1])
	lines := strings.Split(body, "\n")
	if len(lines) != 3 {
		return Checkpoint{}, errors.New("checkpoint: malformed header: expected three lines")
	}
	origin := Origin(lines[0])
	if origin == "" {
		return Checkpoint{}, errors.New("checkpoint: empty origin")
	}
	size, err := strconv.ParseInt(lines[1], 10, 64)
	if err != nil || size < 1 || lines[1] != strconv.FormatInt(size, 10) {
		return Checkpoint{}, errors.New("checkpoint: malformed tree size")
	}
	rawHash, err := base64.StdEncoding.DecodeString(lines[2])
	if err != nil || len(rawHash) != len(tlog.Hash{}) {
		return Checkpoint{}, errors.New("checkpoint: malformed tree hash")
	}
	var hash tlog.Hash
	copy(hash[:], rawHash)
	return Checkpoint{Origin: origin, Size: size, Hash: hash}, nil
}

// parseSignature extracts and decodes the single expected signature line.
func (v *Verifier) parseSignature(block []byte) ([]byte, error) {
	block = bytes.TrimSuffix(block, []byte("\n"))
	lines := strings.Split(string(block), "\n")
	if len(lines) != 1 {
		return nil, errors.New("checkpoint: expected exactly one signature line")
	}
	line := lines[0]
	prefix := "— " + v.name + " "
	if !strings.HasPrefix(line, prefix) {
		return nil, errors.New("checkpoint: signature from unknown key")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, prefix))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, errors.New("checkpoint: malformed signature")
	}
	return sig, nil
}
