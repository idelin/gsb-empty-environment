package netsec

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"strings"
)

// SignatureAlgorithmEd25519 is the only built-in artifact signature algorithm.
const SignatureAlgorithmEd25519 = "ed25519"

// SignedStatement binds a checksum to its module, version, and pinned source.
type SignedStatement struct {
	SourceID   string `json:"source_id"`
	ModulePath string `json:"module_path"`
	Version    string `json:"version"`
	Checksum   string `json:"checksum"`
}

// Signature is attached to an Artifact by its repository.
type Signature struct {
	KeyID     string `json:"key_id"`
	Algorithm string `json:"algorithm"`
	Value     []byte `json:"value"`
}

// SignatureVerifier verifies signatures against a trusted key set.
type SignatureVerifier interface {
	Verify(ctx context.Context, keyID, algorithm string, payload, signature []byte) error
}

// SignatureMode controls when artifact signatures are checked. The zero value
// is the strict mode.
type SignatureMode int

const (
	// SignaturesRequired rejects unsigned artifacts and is the zero value.
	SignaturesRequired SignatureMode = iota
	// SignaturesIfPresent verifies signatures that are present and accepts
	// unsigned artifacts. Use it only when an external checksum database is
	// already enforcing artifact integrity.
	SignaturesIfPresent
	// SignaturesIgnored skips signature verification entirely. This is unsafe
	// for private sources and exists only for explicit compatibility use.
	SignaturesIgnored
)

// CanonicalStatement returns the exact bytes that are signed. Callers must use
// validated module/source/version/checksum values without newlines.
func CanonicalStatement(statement SignedStatement) []byte {
	var builder strings.Builder
	builder.WriteString("netsec-source-priority-v1\n")
	builder.WriteString("source: ")
	builder.WriteString(statement.SourceID)
	builder.WriteByte('\n')
	builder.WriteString("module: ")
	builder.WriteString(statement.ModulePath)
	builder.WriteByte('\n')
	builder.WriteString("version: ")
	builder.WriteString(statement.Version)
	builder.WriteByte('\n')
	builder.WriteString("checksum: ")
	builder.WriteString(statement.Checksum)
	builder.WriteByte('\n')
	return []byte(builder.String())
}

// SignStatement creates an Ed25519 signature over the canonical statement and
// identifies the signing key with keyID.
func SignStatement(privateKey ed25519.PrivateKey, keyID string, statement SignedStatement) (Signature, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return Signature{}, fmt.Errorf("%w: invalid Ed25519 private key size", ErrSignatureInvalid)
	}
	if err := validateIdentifier("signer key ID", keyID); err != nil {
		return Signature{}, fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	if err := validateSignedStatement(statement); err != nil {
		return Signature{}, err
	}
	return Signature{
		KeyID:     keyID,
		Algorithm: SignatureAlgorithmEd25519,
		Value:     ed25519.Sign(privateKey, CanonicalStatement(statement)),
	}, nil
}

func validateSignedStatement(statement SignedStatement) error {
	if err := validateIdentifier("statement source ID", statement.SourceID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArtifact, err)
	}
	if err := validateModulePath(statement.ModulePath); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArtifact, err)
	}
	if !goVersionPattern.MatchString(statement.Version) {
		return fmt.Errorf("%w: statement version %q", ErrInvalidArtifact, statement.Version)
	}
	if _, err := checksumAlgorithm(statement.Checksum); err != nil {
		return fmt.Errorf("%w: statement checksum: %v", ErrInvalidArtifact, err)
	}
	return nil
}

// Ed25519Verifier verifies Ed25519 signatures using an immutable key allowlist.
type Ed25519Verifier struct {
	keys map[string]ed25519.PublicKey
}

// NewEd25519Verifier copies the supplied public keys into a verifier. A key ID
// that is not in this allowlist is rejected as untrusted.
func NewEd25519Verifier(keys map[string]ed25519.PublicKey) *Ed25519Verifier {
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for id, key := range keys {
		copied[id] = append(ed25519.PublicKey(nil), key...)
	}
	return &Ed25519Verifier{keys: copied}
}

// Verify implements SignatureVerifier.
func (v *Ed25519Verifier) Verify(_ context.Context, keyID, algorithm string, payload, signature []byte) error {
	if v == nil {
		return fmt.Errorf("%w", ErrSignatureVerifierMissing)
	}
	if !strings.EqualFold(algorithm, SignatureAlgorithmEd25519) {
		return fmt.Errorf("%w: unsupported algorithm %q", ErrSignatureInvalid, algorithm)
	}
	key, ok := v.keys[keyID]
	if !ok {
		return fmt.Errorf("%w: key %q", ErrUntrustedSigner, keyID)
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: key %q has invalid size", ErrUntrustedSigner, keyID)
	}
	if !ed25519.Verify(key, payload, signature) {
		return fmt.Errorf("%w: key %q", ErrSignatureInvalid, keyID)
	}
	return nil
}
