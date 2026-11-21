package redirectplan

import (
	"encoding/base64"
	"net/http"
)

// Purpose identifies the logical use of a request.  Credentials for the same
// origin are not shared between purposes.
type Purpose string

// Identity is the complete credential lookup key.  It intentionally contains
// no redirect position or previous credential, so every hop starts from a
// fresh exact lookup.
type Identity struct {
	Origin  Origin
	Purpose Purpose
}

// String returns a diagnostic representation of the identity.
func (i Identity) String() string {
	return i.Origin.String() + " purpose=" + string(i.Purpose)
}

// Credential is caller-owned authentication material.  Scheme and Token are a
// convenience for the common "Authorization: <scheme> <token>" form.  Headers
// can carry other authentication headers; headers are cloned when applied.
// A non-empty Token with an empty Scheme defaults to "Bearer".
type Credential struct {
	Scheme  string
	Token   string
	Headers http.Header
}

// IsZero reports whether the credential contains no material.
func (c Credential) IsZero() bool {
	return c.Token == "" && len(c.Headers) == 0
}

// Apply copies the credential onto dst.  Configured headers replace any
// same-named header from the caller's base request.
func (c Credential) Apply(dst http.Header) {
	if dst == nil {
		return
	}
	for name, values := range c.Headers {
		dst.Del(name)
		for _, value := range values {
			dst.Add(name, value)
		}
	}
	if c.Token == "" || hasHeader(c.Headers, "Authorization") {
		return
	}
	scheme := c.Scheme
	if scheme == "" {
		scheme = "Bearer"
	}
	dst.Set("Authorization", scheme+" "+c.Token)
}

// BearerToken returns an Authorization: Bearer credential.
func BearerToken(token string) Credential {
	return Credential{Scheme: "Bearer", Token: token}
}

// BasicAuth returns an Authorization: Basic credential.
func BasicAuth(username, password string) Credential {
	value := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return Credential{Scheme: "Basic", Token: value}
}

// HeaderCredential returns a credential made of one authentication header.
func HeaderCredential(name, value string) Credential {
	h := make(http.Header, 1)
	h.Set(name, value)
	return Credential{Headers: h}
}

// CredentialSource supplies caller-authorized credentials.  Implementations
// must key on the complete identity and must not infer credentials from a
// response or an earlier hop.
type CredentialSource interface {
	Lookup(Identity) (Credential, bool)
}

// CredentialFunc adapts a function to CredentialSource.
type CredentialFunc func(Identity) (Credential, bool)

// Lookup implements CredentialSource.
func (f CredentialFunc) Lookup(id Identity) (Credential, bool) {
	if f == nil {
		return Credential{}, false
	}
	return f(id)
}

// CredentialSet is an exact (origin, purpose) to credential mapping.  It is
// deliberately a plain map: there is no wildcard or suffix matching.
type CredentialSet map[Identity]Credential

// Lookup implements CredentialSource.
func (s CredentialSet) Lookup(id Identity) (Credential, bool) {
	c, ok := s[id]
	return c, ok
}

func hasHeader(h http.Header, name string) bool {
	if h == nil {
		return false
	}
	return len(h.Values(name)) != 0
}
