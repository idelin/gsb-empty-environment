package webauthn

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
)

// verifyPacked implements both Full (x5c present) and Self (no x5c) modes of
// the packed attestation statement.
func verifyPacked(attStmt *cborNode, signedData []byte,
	credentialKey *ecdsa.PublicKey, credentialAlg int,
	authData *AuthenticatorData, policy *Policy, result *RegistrationResult) error {

	sigNode := attStmt.mapIndexText("sig")
	if sigNode == nil {
		return fmt.Errorf("%w: packed attestation statement is missing sig", ErrMalformed)
	}
	signature, ok := sigNode.asBytes()
	if !ok {
		return errCBORType("attStmt.sig", "byte string", sigNode)
	}

	algNode := attStmt.mapIndexText("alg")
	if algNode == nil {
		return fmt.Errorf("%w: packed attestation statement is missing alg", ErrMalformed)
	}
	stmtAlg, ok := algNode.asInt64()
	if !ok {
		return errCBORType("attStmt.alg", "integer", algNode)
	}
	if int(stmtAlg) != credentialAlg {
		return fmt.Errorf("%w: attStmt.alg %d differs from credential alg %d", ErrMalformed, stmtAlg, credentialAlg)
	}

	x5cNode := attStmt.mapIndexText("x5c")
	if x5cNode != nil {
		// Full attestation: reject any unknown statement members.
		if len(attStmt.entries) != 3 {
			return fmt.Errorf("%w: packed full attestation statement has unexpected members", ErrMalformed)
		}
		return verifyPackedFull(x5cNode, signature, signedData, int(stmtAlg), authData, policy, result)
	}

	// Self attestation: only sig and alg are permitted.
	if len(attStmt.entries) != 2 {
		return fmt.Errorf("%w: packed self attestation statement has unexpected members", ErrMalformed)
	}
	if err := verifyECDSASignature(credentialKey, signedData, signature, credentialAlg); err != nil {
		return err
	}
	result.AttestationType = "packed-self"
	return nil
}

// verifyPackedFull validates the attestation certificate chain, metadata
// constraints and the AAGUID extension before verifying the signature.
func verifyPackedFull(x5cNode *cborNode, signature, signedData []byte, stmtAlg int,
	authData *AuthenticatorData, policy *Policy, result *RegistrationResult) error {

	if x5cNode.major != cborArray || len(x5cNode.array) == 0 {
		return errCBORType("attStmt.x5c", "non-empty array", x5cNode)
	}

	certificates := make([]*x509.Certificate, len(x5cNode.array))
	for i, entry := range x5cNode.array {
		der, ok := entry.asBytes()
		if !ok {
			return errCBORType("attStmt.x5c entry", "byte string", entry)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("%w: cannot parse attestation certificate: %v", ErrCertificate, err)
		}
		certificates[i] = cert
	}

	leaf := certificates[0]
	if err := enforceAttestationCertificate(leaf, authData); err != nil {
		return err
	}
	if err := verifyCertificateChain(certificates, policy); err != nil {
		return err
	}
	if !aaguidAllowed(authData.AAGUID, policy.AllowedAAGUIDs) {
		return fmt.Errorf("%w: AAGUID %s is not in the enterprise allow-list", ErrAAGUID, formatAAGUID(authData.AAGUID))
	}

	leafKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: attestation certificate public key must be EC", ErrCertificate)
	}
	// The attestation signature algorithm is declared by attStmt.alg (which
	// already matches the credential algorithm); cert.SignatureAlgorithm only
	// describes how the CA signed this certificate and is not authoritative.
	if err := verifyECDSASignature(leafKey, signedData, signature, stmtAlg); err != nil {
		return err
	}
	result.AttestationType = "packed-full"
	return nil
}

// enforceAttestationCertificate applies the mandatory FIDO metadata checks.
func enforceAttestationCertificate(cert *x509.Certificate, authData *AuthenticatorData) error {
	if cert.Version != 3 {
		return fmt.Errorf("%w: attestation certificate must be X.509 v3 (got v%d)", ErrCertificate, cert.Version)
	}
	if cert.IsCA {
		return fmt.Errorf("%w: attestation certificate must have CA=false", ErrCertificate)
	}
	if !hasExactOU(cert.Subject.OrganizationalUnit, authenticatorAttestationOU) {
		return fmt.Errorf("%w: subject OU must contain %q", ErrCertificate, authenticatorAttestationOU)
	}

	extensionValue := findAAGUIDExtension(cert)
	if extensionValue == nil {
		return fmt.Errorf("%w: certificate is missing id-fido-gen-ce-aaguid extension", ErrAAGUID)
	}

	// The extension extnValue is an OCTET STRING wrapping the 16-byte AAGUID.
	var inner asn1.RawValue
	rest, err := asn1.Unmarshal(extensionValue, &inner)
	if err != nil || len(rest) != 0 || inner.Tag != asn1.TagOctetString {
		return fmt.Errorf("%w: AAGUID extension is not an ASN.1 OCTET STRING", ErrAAGUID)
	}
	if len(inner.Bytes) != aaguidLength {
		return fmt.Errorf("%w: AAGUID extension must wrap exactly %d bytes", ErrAAGUID, aaguidLength)
	}
	var extensionAAGUID AAGUID
	copy(extensionAAGUID[:], inner.Bytes)
	if extensionAAGUID != authData.AAGUID {
		return fmt.Errorf("%w: certificate AAGUID %s does not match authenticatorData AAGUID %s",
			ErrAAGUID, formatAAGUID(extensionAAGUID), formatAAGUID(authData.AAGUID))
	}
	return nil
}

func findAAGUIDExtension(cert *x509.Certificate) []byte {
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidFIDOAAGUID) {
			return ext.Value
		}
	}
	return nil
}

// verifyCertificateChain builds and verifies the chain against injected trust
// anchors only. EKU is not enforced because FIDO attestation certificates use
// the id-fido-gen-ce-aaguid mechanism rather than a dedicated EKU.
func verifyCertificateChain(certificates []*x509.Certificate, policy *Policy) error {
	if policy.TrustAnchors == nil {
		return fmt.Errorf("%w: no trust anchors configured for full attestation", ErrCertificate)
	}

	intermediates := x509.NewCertPool()
	for _, intermediate := range certificates[1:] {
		intermediates.AddCert(intermediate)
	}

	opts := x509.VerifyOptions{
		Roots:         policy.TrustAnchors,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	if _, err := certificates[0].Verify(opts); err != nil {
		return fmt.Errorf("%w: attestation chain does not verify against a trust anchor: %v", ErrCertificate, err)
	}
	return nil
}

func aaguidAllowed(candidate AAGUID, allowed []AAGUID) bool {
	for _, entry := range allowed {
		if entry == candidate {
			return true
		}
	}
	return false
}

func hasExactOU(units []string, wanted string) bool {
	for _, unit := range units {
		if unit == wanted {
			return true
		}
	}
	return false
}

func formatAAGUID(guid AAGUID) string {
	return hex.EncodeToString(guid[:])
}
