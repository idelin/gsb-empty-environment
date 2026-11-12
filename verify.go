package netsec

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Verify verifies an envelope, its certificate chain, and every in-toto
// binding before invoking the policy's publish callback.
//
// certificates must contain the signing leaf first, followed by any
// intermediates.  A root may also be included, but trust is always determined
// by Policy.Roots/TrustRoots/CertPool rather than by the supplied chain.
func Verify(ctx context.Context, env Envelope, certificates []*x509.Certificate, policy Policy) (*VerifiedStatement, error) {
	return verifyEnvelope(ctx, env, certificates, policy)
}

// VerifyEnvelope verifies an envelope without a context.
func VerifyEnvelope(env Envelope, certificates []*x509.Certificate, policy Policy) (*VerifiedStatement, error) {
	return Verify(context.Background(), env, certificates, policy)
}

// VerifyAttestation is the request-struct entry point used by admission hosts.
func VerifyAttestation(ctx context.Context, request VerifyRequest) (*VerifiedStatement, error) {
	return Verify(ctx, request.Envelope, request.Certificates, request.Policy)
}

// VerifyBytes decodes and verifies a JSON-encoded DSSE envelope.
func VerifyBytes(ctx context.Context, envelopeJSON []byte, certificates []*x509.Certificate, policy Policy) (*VerifiedStatement, error) {
	var env Envelope
	decoder := json.NewDecoder(bytes.NewReader(envelopeJSON))
	if err := decoder.Decode(&env); err != nil {
		return nil, fmt.Errorf("netsec: decode DSSE envelope: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	return Verify(ctx, env, certificates, policy)
}

// VerifyEnvelopeBytes is an alias for VerifyBytes without a context.
func VerifyEnvelopeBytes(envelopeJSON []byte, certificates []*x509.Certificate, policy Policy) (*VerifiedStatement, error) {
	return VerifyBytes(context.Background(), envelopeJSON, certificates, policy)
}

func verifyEnvelope(ctx context.Context, env Envelope, certificates []*x509.Certificate, policy Policy) (*VerifiedStatement, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	payload, signingInput, err := validateEnvelope(env)
	if err != nil {
		return nil, err
	}

	envelopeCertificates, err := certificatesFromEnvelope(env)
	if err != nil {
		return nil, err
	}
	identityOID, err := policy.identityOID()
	if err != nil {
		return nil, err
	}
	expectedIdentity := policy.expectedCertificateIdentity()
	if expectedIdentity == "" {
		return nil, errors.New("netsec: policy has no expected certificate identity")
	}

	seenSignerKeys := make(map[string]struct{}, len(env.Signatures))
	var firstLeaf *x509.Certificate
	var firstChain []*x509.Certificate
	var firstIdentity string

	for i, signature := range env.Signatures {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chain, err := certificateChainForSignature(signature, certificates, envelopeCertificates)
		if err != nil {
			return nil, fmt.Errorf("signature %d: %w", i, err)
		}
		if len(chain) == 0 {
			return nil, fmt.Errorf("signature %d: no signing certificate supplied", i)
		}
		leaf := chain[0]

		if err := verifyCertificateChain(leaf, chain, policy); err != nil {
			return nil, fmt.Errorf("signature %d: %w", i, err)
		}
		identity, err := certificateIdentity(leaf, identityOID)
		if err != nil {
			return nil, fmt.Errorf("signature %d: %w", i, err)
		}
		if identity != expectedIdentity {
			return nil, fmt.Errorf("signature %d: certificate identity %q does not match policy %q", i, identity, expectedIdentity)
		}

		fingerprint, err := publicKeyFingerprint(leaf)
		if err != nil {
			return nil, fmt.Errorf("signature %d: certificate public key: %w", i, err)
		}
		if _, ok := seenSignerKeys[fingerprint]; ok {
			return nil, fmt.Errorf("%w: certificate key appears more than once", ErrDuplicateSigner)
		}
		seenSignerKeys[fingerprint] = struct{}{}

		if err := verifySignatureBytes(signature.Sig, leaf, signingInput); err != nil {
			return nil, fmt.Errorf("signature %d: %w", i, err)
		}

		if firstLeaf == nil {
			firstLeaf = leaf
			firstChain = chain
			firstIdentity = identity
		}
	}

	statement, err := decodeStatement(payload)
	if err != nil {
		return nil, err
	}
	if err := statement.validateTarget(policy); err != nil {
		return nil, err
	}
	if err := statement.validatePolicyBinding(policy); err != nil {
		return nil, err
	}

	result := &VerifiedStatement{
		Statement:        *statement,
		Payload:          append([]byte(nil), payload...),
		PayloadType:      env.PayloadType,
		Signer:           firstLeaf,
		Certificate:      firstLeaf,
		CertificateChain: firstChain,
		SignerIdentity:   firstIdentity,
		Identity:         firstIdentity,
	}
	if err := policy.notify(ctx, result); err != nil {
		return nil, fmt.Errorf("netsec: publish verified statement: %w", err)
	}
	return result, nil
}

func certificatesFromEnvelope(env Envelope) ([]*x509.Certificate, error) {
	var certificateData []string
	for _, value := range env.Certificates {
		if strings.TrimSpace(value) != "" {
			certificateData = append(certificateData, value)
		}
	}
	for _, value := range env.CertificateChain {
		if strings.TrimSpace(value) != "" {
			certificateData = append(certificateData, value)
		}
	}

	var certificates []*x509.Certificate
	for _, value := range certificateData {
		decoded, err := decodeCertificates(value)
		if err != nil {
			return nil, fmt.Errorf("netsec: decode envelope certificate: %w", err)
		}
		certificates = append(certificates, decoded...)
	}
	return certificates, nil
}

func certificateChainForSignature(signature Signature, supplied []*x509.Certificate, envelope []*x509.Certificate) ([]*x509.Certificate, error) {
	var chain []*x509.Certificate
	if value := signature.certificateData(); value != "" {
		decoded, err := decodeCertificates(value)
		if err != nil {
			return nil, fmt.Errorf("decode signing certificate: %w", err)
		}
		chain = append(chain, decoded...)
	}
	for _, value := range signature.certificateChainData() {
		decoded, err := decodeCertificates(value)
		if err != nil {
			return nil, fmt.Errorf("decode certificate chain: %w", err)
		}
		chain = append(chain, decoded...)
	}

	if len(chain) == 0 {
		if len(supplied) != 0 {
			chain = supplied
		} else {
			chain = envelope
		}
	} else if len(supplied) != 0 {
		for _, certificate := range supplied {
			if certificate != nil && !containsCertificate(chain, certificate) {
				chain = append(chain, certificate)
			}
		}
	}
	return chain, nil
}

func containsCertificate(chain []*x509.Certificate, candidate *x509.Certificate) bool {
	for _, certificate := range chain {
		if certificate != nil && bytes.Equal(certificate.Raw, candidate.Raw) {
			return true
		}
	}
	return false
}

func verifySignatureBytes(encoded string, certificate *x509.Certificate, signingInput []byte) error {
	signature, err := decodeBase64(encoded)
	if err != nil {
		return fmt.Errorf("signature bytes: %w", err)
	}
	if len(signature) == 0 {
		return errors.New("signature is empty")
	}

	digest := sha256.Sum256(signingInput)
	switch publicKey := certificate.PublicKey.(type) {
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
			return errors.New("ECDSA signature verification failed")
		}
	case ed25519.PublicKey:
		if !ed25519.Verify(publicKey, signingInput, signature) {
			return errors.New("Ed25519 signature verification failed")
		}
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err == nil {
			return nil
		}
		if err := rsa.VerifyPSS(publicKey, crypto.SHA256, digest[:], signature, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto, Hash: crypto.SHA256}); err == nil {
			return nil
		}
		return errors.New("RSA signature verification failed")
	default:
		return fmt.Errorf("unsupported signing public key type %T", certificate.PublicKey)
	}
	return nil
}
