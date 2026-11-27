package webauthn

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
)

// oidFIDOAAGUID is id-fido-gen-ce-aaguid (FIDO security reference / WebAuthn).
var oidFIDOAAGUID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 45724, 1, 1, 4}

const authenticatorAttestationOU = "Authenticator Attestation"

// Policy configures the enterprise registration verifier. All fields are
// evaluated without network access.
type Policy struct {
	// RPID is the relying-party identifier whose SHA-256 hash must match
	// authenticatorData.rpIdHash.
	RPID string

	// Origin is the exact expected clientDataJSON.origin.
	Origin string

	// RequireUserVerification, when true, mandates the UV flag.
	RequireUserVerification bool

	// AllowNoneAttestation permits the "none" attestation format. Enterprise
	// hardware-attested flows should leave this false so "none" is hard-rejected.
	AllowNoneAttestation bool

	// TrustAnchors are the acceptable FIDO attestation root/intermediate
	// certificates. A packed Full attestation must chain to one of them.
	TrustAnchors *x509.CertPool

	// AllowedAAGUIDs lists enterprise-approved authenticator models. A packed
	// Full attestation is rejected unless its AAGUID appears in this list.
	AllowedAAGUIDs []AAGUID
}

// Credential is the verified public credential created by the registration.
type Credential struct {
	ID        []byte
	AAGUID    AAGUID
	PublicKey *ecdsa.PublicKey
	Algorithm int
	SignCount uint32
}

// RegistrationResult is returned on successful verification.
type RegistrationResult struct {
	Format          string
	AttestationType string // "packed-full", "packed-self", "none"
	Credential      *Credential
	Authenticator   *AuthenticatorData
}

// VerifyRegistration validates a WebAuthn registration response.
//
// attestationObject is the raw CBOR attestation object returned by the
// authenticator, clientDataJSON the raw CollectedClientData JSON, and
// challenge the exact one-time challenge bytes previously issued to the
// client. The function never performs network or filesystem access.
func VerifyRegistration(attestationObject, clientDataJSON, challenge []byte, policy *Policy) (*RegistrationResult, error) {
	if policy == nil {
		return nil, fmt.Errorf("%w: verification policy is required", ErrPolicy)
	}

	root, err := decodeCBOR(attestationObject)
	if err != nil {
		return nil, err
	}
	if root.major != cborMap {
		return nil, errCBORType("attestation object", "map", root)
	}

	formatNode := root.mapIndexText("fmt")
	attStmtNode := root.mapIndexText("attStmt")
	authDataNode := root.mapIndexText("authData")
	if formatNode == nil || attStmtNode == nil || authDataNode == nil {
		return nil, fmt.Errorf("%w: attestation object must contain fmt, attStmt and authData", ErrMalformed)
	}
	// Exactly the three defined top-level members are accepted.
	if len(root.entries) != 3 {
		return nil, fmt.Errorf("%w: unexpected members in attestation object", ErrMalformed)
	}

	format, ok := formatNode.asText()
	if !ok {
		return nil, errCBORType("fmt", "text string", formatNode)
	}
	if attStmtNode.major != cborMap {
		return nil, errCBORType("attStmt", "map", attStmtNode)
	}
	rawAuthData, ok := authDataNode.asBytes()
	if !ok {
		return nil, errCBORType("authData", "byte string", authDataNode)
	}

	authData, err := parseAuthenticatorData(rawAuthData)
	if err != nil {
		return nil, err
	}
	if err := enforceAuthDataPolicy(authData, policy); err != nil {
		return nil, err
	}

	if _, err := parseClientData(clientDataJSON, challenge, policy.Origin); err != nil {
		return nil, err
	}

	publicKey, algorithm, err := parseCOSEPublicKey(authData.CredentialPublicKey)
	if err != nil {
		return nil, err
	}

	clientDataHash := sha256.Sum256(clientDataJSON)
	signedData := make([]byte, 0, len(rawAuthData)+sha256.Size)
	signedData = append(signedData, rawAuthData...)
	signedData = append(signedData, clientDataHash[:]...)

	result := &RegistrationResult{
		Format:        format,
		Authenticator: authData,
		Credential: &Credential{
			ID:        append([]byte(nil), authData.CredentialID...),
			AAGUID:    authData.AAGUID,
			PublicKey: publicKey,
			Algorithm: algorithm,
			SignCount: authData.SignCount,
		},
	}

	switch format {
	case "none":
		if err := verifyNone(attStmtNode, policy); err != nil {
			return nil, err
		}
		result.AttestationType = "none"
	case "packed":
		if err := verifyPacked(attStmtNode, signedData, publicKey, algorithm, authData, policy, result); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: unsupported attestation format %q", ErrPolicy, format)
	}
	return result, nil
}

// enforceAuthDataPolicy validates the binary flags and rpIdHash contract.
func enforceAuthDataPolicy(authData *AuthenticatorData, policy *Policy) error {
	if !authData.UP {
		return fmt.Errorf("%w: UP flag must be set", ErrFlags)
	}
	if !authData.AT {
		return fmt.Errorf("%w: AT flag must be set for registration", ErrFlags)
	}
	if policy.RequireUserVerification && !authData.UV {
		return fmt.Errorf("%w: UV flag is required by policy but is not set", ErrFlags)
	}

	expectedHash := sha256.Sum256([]byte(policy.RPID))
	if !bytes.Equal(authData.RPIDHash, expectedHash[:]) {
		return fmt.Errorf("%w: rpIdHash does not equal SHA-256(%q)", ErrRPIDHash, policy.RPID)
	}
	return nil
}

// verifyNone enforces an empty attestation statement and the format policy.
func verifyNone(attStmt *cborNode, policy *Policy) error {
	if !policy.AllowNoneAttestation {
		return fmt.Errorf("%w: \"none\" attestation is disabled by enterprise policy", ErrPolicy)
	}
	if len(attStmt.entries) != 0 {
		return fmt.Errorf("%w: \"none\" attestation statement must be empty", ErrMalformed)
	}
	return nil
}
