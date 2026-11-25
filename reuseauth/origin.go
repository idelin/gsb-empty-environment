package reuseauth

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// NavID identifies a browser navigation. Epoch fences all stale waiters.
type NavID string

// RequestID is a navigation-scoped correlation identifier. It is safe to
// expose to the navigation and must never be the private DownloadID.
type RequestID string

// DownloadID is a trusted host/prefetch correlation identifier. It is not
// placed in navigation-visible traces.
type DownloadID string

// SignerID identifies an origin signing key in TrustStore.
type SignerID string

// Epoch is changed by revocation or a top-level site switch.
type Epoch uint64

// Origin is a canonical scheme/host/port origin.
type Origin struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Port   string `json:"port,omitempty"`
	wild   bool
}

// AnyOrigin matches every origin in a policy rule. It must not be used as an
// actual recipient or signer origin.
var AnyOrigin = Origin{Scheme: "*", Host: "*", wild: true}

// ParseOrigin parses and canonicalizes a web origin.
func ParseOrigin(raw string) (Origin, error) {
	if raw == "*" {
		return AnyOrigin, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Origin{}, fmt.Errorf("%w: origin %q: %v", ErrInvalidArgument, raw, err)
	}
	if u.Scheme == "" || u.Hostname() == "" || u.User != nil || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return Origin{}, fmt.Errorf("%w: invalid origin %q", ErrInvalidArgument, raw)
	}
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	return Origin{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Hostname()), Port: port}, nil
}

// OriginFromURL extracts and canonicalizes the origin of an absolute URL.
func OriginFromURL(raw string) (Origin, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Origin{}, fmt.Errorf("%w: url %q: %v", ErrInvalidArgument, raw, err)
	}
	if !u.IsAbs() || u.Host == "" {
		return Origin{}, fmt.Errorf("%w: absolute URL required: %q", ErrInvalidArgument, raw)
	}
	return ParseOrigin(u.Scheme + "://" + u.Host)
}

// IsZero reports whether o is unset.
func (o Origin) IsZero() bool { return o.Scheme == "" && o.Host == "" }

// IsAny reports whether o is the wildcard origin.
func (o Origin) IsAny() bool { return o.wild || o == AnyOrigin }

// Equal reports exact origin equality. Wildcards are not considered equal to a
// concrete origin here; use OriginMatches for policy matching.
func (o Origin) Equal(other Origin) bool {
	return o.Scheme == other.Scheme && o.Host == other.Host && o.Port == other.Port && o.wild == other.wild
}

// OriginMatches reports whether pattern permits exact. AnyOrigin is wildcard.
func OriginMatches(pattern, exact Origin) bool {
	if pattern.IsAny() {
		return !exact.IsZero()
	}
	return pattern.Equal(exact)
}

// String returns the canonical origin string.
func (o Origin) String() string {
	if o.IsAny() {
		return "*"
	}
	if o.Port == "" {
		return o.Scheme + "://" + o.Host
	}
	return o.Scheme + "://" + o.Host + ":" + o.Port
}

// MarshalText implements encoding.TextMarshaler.
func (o Origin) MarshalText() ([]byte, error) { return []byte(o.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (o *Origin) UnmarshalText(text []byte) error {
	parsed, err := ParseOrigin(string(text))
	if err != nil {
		return err
	}
	*o = parsed
	return nil
}

// CredentialMode mirrors the browser request credential mode.
type CredentialMode string

const (
	CredentialsOmit       CredentialMode = "omit"
	CredentialsSameOrigin CredentialMode = "same-origin"
	CredentialsInclude    CredentialMode = "include"
)

// Valid reports whether mode is one of the supported modes.
func (m CredentialMode) Valid() bool {
	switch m {
	case CredentialsOmit, CredentialsSameOrigin, CredentialsInclude:
		return true
	default:
		return false
	}
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
