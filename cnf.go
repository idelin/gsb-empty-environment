package netsec

import "fmt"

// Cnf is the subset of the RFC 8705 / RFC 9449 "cnf" claim understood by this
// package. Either member may be absent, but at least one must be present in a
// token that carries cnf.
type Cnf struct {
	// X5TS256 binds the token to the DER-encoded client certificate.
	X5TS256 string `json:"x5t#S256,omitempty"`
	// JKT binds the token to a proof-of-possession key thumbprint.
	JKT string `json:"jkt,omitempty"`
}

// Present reports whether at least one supported binding member is set.
func (c Cnf) Present() bool {
	return c.X5TS256 != "" || c.JKT != ""
}

// HasCertificateBinding reports whether the cnf carries an x5t#S256 member.
func (c Cnf) HasCertificateBinding() bool {
	return c.X5TS256 != ""
}

// Claims returns the cnf object in a form suitable for embedding in a JWT or
// introspection response.
func (c Cnf) Claims() map[string]any {
	claims := make(map[string]any, 2)
	if c.X5TS256 != "" {
		claims["x5t#S256"] = c.X5TS256
	}
	if c.JKT != "" {
		claims["jkt"] = c.JKT
	}
	return claims
}

// ExtractCnf returns the cnf binding from token claims. The bool reports
// whether a cnf member was present at all; a present but empty or malformed
// cnf is an error rather than an unbound token.
//
// claims may come from a decoded JWT payload or from an RFC 7662 introspection
// response. Signature validation is the caller's responsibility.
func ExtractCnf(claims map[string]any) (Cnf, bool, error) {
	raw, ok := claims["cnf"]
	if !ok {
		return Cnf{}, false, nil
	}

	object, ok := raw.(map[string]any)
	if !ok {
		return Cnf{}, true, fmt.Errorf("%w: cnf must be a JSON object", ErrMalformedCnf)
	}

	var cnf Cnf
	if value, ok := object["x5t#S256"]; ok {
		s, ok := value.(string)
		if !ok {
			return Cnf{}, true, fmt.Errorf("%w: cnf.x5t#S256 must be a string", ErrMalformedCnf)
		}
		if s == "" {
			return Cnf{}, true, fmt.Errorf("%w: cnf.x5t#S256 is empty", ErrInvalidFingerprint)
		}
		cnf.X5TS256 = s
	}
	if value, ok := object["jkt"]; ok {
		s, ok := value.(string)
		if !ok {
			return Cnf{}, true, fmt.Errorf("%w: cnf.jkt must be a string", ErrMalformedCnf)
		}
		if s == "" {
			return Cnf{}, true, fmt.Errorf("%w: cnf.jkt is empty", ErrInvalidFingerprint)
		}
		cnf.JKT = s
	}
	if !cnf.Present() {
		return Cnf{}, true, fmt.Errorf("%w: cnf has no supported binding member", ErrMalformedCnf)
	}
	return cnf, true, nil
}
