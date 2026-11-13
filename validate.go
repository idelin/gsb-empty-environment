package netsec

import "fmt"

// ClientRegistration is the client policy needed to decide whether a token
// must carry a certificate binding. It is populated by the authorization
// server's client registry and shared with resource servers as needed.
type ClientRegistration struct {
	// RequireCertificateBinding is true when this client's access tokens must
	// carry cnf.x5t#S256.
	RequireCertificateBinding bool
}

// ValidateAccessToken checks a decoded token (JWT claims or an introspection
// response) against the current connection and client registration.
//
// The rules are:
//   - A cnf.x5t#S256 value must equal SHA-256 of the connection's client
//     certificate DER, encoded as canonical unpadded base64url.
//   - Any cnf requires a client certificate on the connection.
//   - A client registered as requiring certificate binding must receive a
//     token whose cnf contains x5t#S256.
//   - If cnf contains both jkt and x5t#S256, both must match.
//
// JWT signature validation is deliberately outside this function.
func ValidateAccessToken(claims map[string]any, conn Connection, reg ClientRegistration) error {
	cnf, present, err := ExtractCnf(claims)
	if err != nil {
		return err
	}
	if !present {
		if reg.RequireCertificateBinding {
			return ErrCertificateBindingRequired
		}
		return nil
	}
	return validateCnfBinding(cnf, conn, reg)
}

// ValidateAccessTokenJWT parses a compact JWT and validates its binding
// claims. The caller must verify the JWT signature separately.
func ValidateAccessTokenJWT(rawJWT string, conn Connection, reg ClientRegistration) error {
	claims, err := ParseJWTClaims(rawJWT)
	if err != nil {
		return err
	}
	return ValidateAccessToken(claims, conn, reg)
}

// ValidateAccessTokenIntrospection parses an RFC 7662 introspection response
// and validates its binding claims. The caller must check the "active" member
// and any other token status separately.
func ValidateAccessTokenIntrospection(rawResponse []byte, conn Connection, reg ClientRegistration) error {
	claims, err := ParseIntrospectionResponse(rawResponse)
	if err != nil {
		return err
	}
	return ValidateAccessToken(claims, conn, reg)
}

func validateCnfBinding(cnf Cnf, conn Connection, reg ClientRegistration) error {
	// A cnf-bearing token can never be accepted on a connection that did not
	// authenticate a client certificate. This check intentionally happens
	// before inspecting individual members.
	if !conn.HasClientCertificate() {
		return ErrCnfWithoutClientCertificate
	}

	if reg.RequireCertificateBinding && !cnf.HasCertificateBinding() {
		return ErrCertificateBindingRequired
	}

	if cnf.X5TS256 != "" {
		if err := ValidateX5TS256(cnf.X5TS256); err != nil {
			return err
		}
		certFingerprint, err := conn.CertificateFingerprint()
		if err != nil {
			return err
		}
		match, err := fingerprintsEqual(cnf.X5TS256, certFingerprint)
		if err != nil {
			return err
		}
		if !match {
			return ErrFingerprintMismatch
		}
	}

	if cnf.JKT != "" {
		if err := ValidateX5TS256(cnf.JKT); err != nil {
			return err
		}
		proofThumbprint, err := conn.keyThumbprint()
		if err != nil {
			return err
		}
		match, err := fingerprintsEqual(cnf.JKT, proofThumbprint)
		if err != nil {
			return err
		}
		if !match {
			return ErrJKTMismatch
		}
	}

	return nil
}

// RequireCertificateBinding is an optional helper for resource servers that
// know a client must be certificate-bound but receive only a token.
func RequireCertificateBinding(claims map[string]any) error {
	cnf, present, err := ExtractCnf(claims)
	if err != nil {
		return err
	}
	if !present || !cnf.HasCertificateBinding() {
		return ErrCertificateBindingRequired
	}
	return nil
}

// BoundFingerprint returns the certificate fingerprint carried by a token, if
// any. It validates the claim format but does not compare it to a connection.
func BoundFingerprint(claims map[string]any) (string, error) {
	cnf, present, err := ExtractCnf(claims)
	if err != nil {
		return "", err
	}
	if !present || cnf.X5TS256 == "" {
		return "", fmt.Errorf("%w: token has no cnf.x5t#S256", ErrCertificateBindingRequired)
	}
	if err := ValidateX5TS256(cnf.X5TS256); err != nil {
		return "", err
	}
	return cnf.X5TS256, nil
}
