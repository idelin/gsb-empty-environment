package netsec

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	// ErrInvalidRedirectURI reports a redirect URI that is not a usable
	// absolute URI.
	ErrInvalidRedirectURI = errors.New("netsec: invalid redirect URI")

	// ErrRedirectURIUserInfo reports a redirect URI containing a userinfo
	// component, such as https://trusted.example@attacker.example/callback.
	ErrRedirectURIUserInfo = errors.New("netsec: redirect URI must not contain userinfo")

	// ErrRedirectURIFragment reports a redirect URI containing a fragment.
	ErrRedirectURIFragment = errors.New("netsec: redirect URI must not contain a fragment")

	// ErrRedirectURIMismatch reports that the redirect URI presented during
	// token redemption differs from the one used in the authorization request.
	ErrRedirectURIMismatch = errors.New("netsec: redirect URI mismatch")
)

// ValidateRedirectURI verifies that the redirect_uri presented during
// authorization-code redemption is byte-for-byte identical to the one supplied
// in the authorization request.
//
// The comparison is deliberately opaque: the URI is never normalized, decoded,
// or re-encoded. In particular, query parameters must be in the same order and
// use the same escaping as in the authorization request. Both values are also
// rejected if they contain userinfo or a fragment.
//
// Callers must persist the authorization request's redirect_uri exactly as it
// was received and pass that value as authorizationRedirectURI.
func ValidateRedirectURI(authorizationRedirectURI, tokenRedirectURI string) error {
	if err := ValidateRedirectURIFormat(authorizationRedirectURI); err != nil {
		return fmt.Errorf("authorization redirect_uri: %w", err)
	}
	if err := ValidateRedirectURIFormat(tokenRedirectURI); err != nil {
		return fmt.Errorf("token request redirect_uri: %w", err)
	}
	if authorizationRedirectURI != tokenRedirectURI {
		return ErrRedirectURIMismatch
	}
	return nil
}

// ValidateTokenRedirectURI is an alias for ValidateRedirectURI.
func ValidateTokenRedirectURI(authorizationRedirectURI, tokenRedirectURI string) error {
	return ValidateRedirectURI(authorizationRedirectURI, tokenRedirectURI)
}

// ValidateRedirect is an alias for ValidateRedirectURI.
func ValidateRedirect(authorizationRedirectURI, tokenRedirectURI string) error {
	return ValidateRedirectURI(authorizationRedirectURI, tokenRedirectURI)
}

// RedirectURIMatches reports whether the redirect URI presented during token
// redemption is acceptable for the authorization request. It applies the same
// checks as ValidateRedirectURI.
func RedirectURIMatches(authorizationRedirectURI, tokenRedirectURI string) bool {
	return ValidateRedirectURI(authorizationRedirectURI, tokenRedirectURI) == nil
}

// ValidateRedirectURIFormat validates a single redirect URI. It requires an
// absolute URI and rejects userinfo and fragments. It does not compare the URI
// with an authorization request; use ValidateRedirectURI for that.
func ValidateRedirectURIFormat(redirectURI string) error {
	if redirectURI == "" {
		return ErrInvalidRedirectURI
	}

	// A literal '#' always starts a fragment. Check the raw value because
	// url.Parse represents a bare trailing '#' as an empty fragment.
	if strings.IndexByte(redirectURI, '#') >= 0 {
		return ErrRedirectURIFragment
	}

	// Spaces and ASCII control characters are not valid in a URI. Reject them
	// rather than allowing a caller to compare malformed values successfully.
	for i := 0; i < len(redirectURI); i++ {
		if b := redirectURI[i]; b <= 0x20 || b == 0x7f {
			return ErrInvalidRedirectURI
		}
	}

	u, err := url.Parse(redirectURI)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRedirectURI, err)
	}
	if !u.IsAbs() {
		return ErrInvalidRedirectURI
	}
	if u.User != nil {
		return ErrRedirectURIUserInfo
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return ErrRedirectURIFragment
	}
	if u.Opaque == "" && u.Host == "" && u.Path == "" {
		return ErrInvalidRedirectURI
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme == "http" || scheme == "https") && u.Hostname() == "" {
		return ErrInvalidRedirectURI
	}
	return nil
}
