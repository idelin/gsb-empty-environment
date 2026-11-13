package netsec

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
)

const signatureRecordSize = 2 + 8 + ed25519.SignatureSize

// Verifier verifies embedded signify signatures using one configured public
// key. Comments in signature files are never used to select or identify keys.
type Verifier struct {
	publicKey PublicKey
}

// NewVerifier creates a verifier bound to publicKey.
func NewVerifier(publicKey PublicKey) (*Verifier, error) {
	if err := validatePublicKey(publicKey); err != nil {
		return nil, err
	}
	publicKey.Key = append(ed25519.PublicKey(nil), publicKey.Key...)
	return &Verifier{publicKey: publicKey}, nil
}

// NewVerifierFromPublicKeyFile creates a verifier from a signify public key
// file.
func NewVerifierFromPublicKeyFile(path string) (*Verifier, error) {
	publicKey, err := LoadPublicKey(path)
	if err != nil {
		return nil, err
	}
	return NewVerifier(publicKey)
}

// Verify checks the embedded Ed25519 signature over the manifest body and
// only then parses the body. The untrusted comment line is ignored. Key
// substitution is rejected before signature verification.
func (verifier *Verifier) Verify(signedManifest []byte) (*Manifest, error) {
	if verifier == nil {
		return nil, fmt.Errorf("%w: nil verifier", ErrMalformedSignature)
	}
	if err := validatePublicKey(verifier.publicKey); err != nil {
		return nil, err
	}

	signatureLine, message, err := splitSignedManifest(signedManifest)
	if err != nil {
		return nil, err
	}

	record, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signatureLine)))
	if err != nil {
		return nil, fmt.Errorf("%w: decode signature: %v", ErrMalformedSignature, err)
	}
	if len(record) != signatureRecordSize {
		return nil, fmt.Errorf("%w: decoded signature has %d bytes, want %d", ErrMalformedSignature, len(record), signatureRecordSize)
	}
	if string(record[:2]) != algorithmEd {
		return nil, fmt.Errorf("%w: algorithm is %q, want %q", ErrMalformedSignature, record[:2], algorithmEd)
	}
	if !bytes.Equal(record[2:10], verifier.publicKey.KeyNum[:]) {
		return nil, fmt.Errorf("%w: signature key %s, configured key %s", ErrKeyNumMismatch, keyNumString([8]byte(record[2:10])), keyNumString(verifier.publicKey.KeyNum))
	}
	if !ed25519.Verify(verifier.publicKey.Key, message, record[10:]) {
		return nil, ErrInvalidSignature
	}

	return parseManifest(message)
}

// VerifyManifest is an alias for Verify.
func (verifier *Verifier) VerifyManifest(signedManifest []byte) (*Manifest, error) {
	return verifier.Verify(signedManifest)
}

// VerifySignedManifest verifies a signify-signed manifest using publicKey.
func VerifySignedManifest(publicKey PublicKey, signedManifest []byte) (*Manifest, error) {
	verifier, err := NewVerifier(publicKey)
	if err != nil {
		return nil, err
	}
	return verifier.Verify(signedManifest)
}

// VerifyManifest verifies a signify-signed manifest using publicKey.
func VerifyManifest(publicKey PublicKey, signedManifest []byte) (*Manifest, error) {
	return VerifySignedManifest(publicKey, signedManifest)
}

// LoadSignedManifest reads and verifies a signify-signed manifest file.
func LoadSignedManifest(publicKey PublicKey, path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return VerifySignedManifest(publicKey, data)
}

func splitSignedManifest(data []byte) (signature, message []byte, err error) {
	commentEnd := bytes.IndexByte(data, '\n')
	if commentEnd < 0 {
		return nil, nil, fmt.Errorf("%w: missing comment line", ErrMalformedSignature)
	}

	lineStart := commentEnd + 1
	lineEnd := bytes.IndexByte(data[lineStart:], '\n')
	if lineEnd < 0 {
		return nil, nil, fmt.Errorf("%w: missing message body", ErrMalformedSignature)
	}
	lineEnd += lineStart

	signature = data[lineStart:lineEnd]
	message = data[lineEnd+1:]
	if len(message) == 0 {
		return nil, nil, fmt.Errorf("%w: missing message body", ErrMalformedSignature)
	}
	return signature, message, nil
}
