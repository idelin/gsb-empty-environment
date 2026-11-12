// Package webauthn implements origin and RP ID verification for WebAuthn
// assertion (authenticatorGetAssertion) responses.
//
// A relying party that is reachable through several brand domains must not
// accept an assertion merely because the caller presents a syntactically
// valid rpIdHash: attacker-controlled look-alike domains or hijacked
// subdomains can also produce such responses. The verifier here therefore
// requires every frame that participates in an assertion to be listed in the
// relying party's /.well-known/webauthn origin allow list, and independently
// checks that the rpIdHash carried in the authenticator data matches the
// configured RP ID.
package webauthn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MaxManifestOrigins is the largest number of origins a manifest may contain.
// Each entry is expected to be a registrable brand-domain origin, so the cap
// keeps the allow list small and auditable.
const MaxManifestOrigins = 5

// maxManifestBytes bounds the size of a fetched manifest document.
const maxManifestBytes = 64 * 1024

// Sentinel errors returned by the verifier. Callers should use errors.Is to
// distinguish rejection reasons in security logs.
var (
	// ErrManifest is returned when a manifest document is malformed, too
	// large, contains invalid origins, or violates the entry cap.
	ErrManifest = errors.New("webauthn: invalid origin manifest")
	// ErrOriginNotAllowed is returned when an assertion origin is not
	// present in the manifest.
	ErrOriginNotAllowed = errors.New("webauthn: origin is not in the manifest allow list")
	// ErrTopOriginRequired is returned for a cross-origin assertion that
	// omits topOrigin, or one whose topOrigin is not allow-listed.
	ErrTopOriginRequired = errors.New("webauthn: allow-listed top origin required for cross-origin or framed assertion")
	// ErrRPIDHash is returned when the authenticator data does not carry a
	// 32-byte hash matching the configured RP ID.
	ErrRPIDHash = errors.New("webauthn: authenticator data rpIdHash mismatch")
	// ErrRPID is returned when the configured RP ID itself is invalid.
	ErrRPID = errors.New("webauthn: invalid RP ID")
)

// Manifest is the parsed /.well-known/webauthn resource as defined by the
// W3C "Web Authentication Advanced APIs" extension. Origins holds fully
// canonicalized serialized origins (scheme://host[:port]); matching is exact,
// never suffix-based, which is what blocks look-alike and hijacked subdomains.
type Manifest struct {
	Origins []string `json:"origins"`
}

// ParseManifest decodes a manifest document and canonicalizes every origin.
// It rejects an empty or duplicated allow list, any invalid origin, and lists
// longer than MaxManifestOrigins.
func ParseManifest(data []byte) (*Manifest, error) {
	var raw Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	return buildManifest(raw.Origins)
}

func buildManifest(origins []string) (*Manifest, error) {
	if len(origins) == 0 {
		return nil, fmt.Errorf("%w: empty origins list", ErrManifest)
	}
	if len(origins) > MaxManifestOrigins {
		return nil, fmt.Errorf("%w: %d origins exceed maximum of %d", ErrManifest, len(origins), MaxManifestOrigins)
	}

	seen := make(map[string]struct{}, len(origins))
	canonical := make([]string, 0, len(origins))
	for _, origin := range origins {
		normalized, err := CanonicalOrigin(origin)
		if err != nil {
			return nil, fmt.Errorf("%w: %q: %v", ErrManifest, origin, err)
		}
		if _, dup := seen[normalized]; dup {
			return nil, fmt.Errorf("%w: duplicate origin %q", ErrManifest, normalized)
		}
		seen[normalized] = struct{}{}
		canonical = append(canonical, normalized)
	}
	return &Manifest{Origins: canonical}, nil
}

// Allows reports whether the manifest contains origin exactly. Both values
// must be serialized origins; origin is canonicalized first.
func (m *Manifest) Allows(origin string) bool {
	normalized, err := CanonicalOrigin(origin)
	if err != nil {
		return false
	}
	for _, allowed := range m.Origins {
		if allowed == normalized {
			return true
		}
	}
	return false
}

// CanonicalOrigin parses and normalizes a serialized origin into
// scheme://host[:port] using lowercase scheme/host and the scheme's default
// port when no explicit port is given. HTTP origins are only accepted on
// loopback hosts; production deployments use HTTPS.
func CanonicalOrigin(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("empty origin")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "https", "http":
	default:
		return "", fmt.Errorf("unsupported scheme %q (only https and http are supported)", parsed.Scheme)
	}
	if parsed.User != nil {
		return "", errors.New("origin must not contain userinfo")
	}
	if parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must not contain path, query, or fragment")
	}

	if parsed.Host == "" {
		return "", errors.New("missing host")
	}
	// Hostname() strips brackets from an IPv6 literal; use the bare address
	// for policy checks and the bracketed literal when serializing.
	hostname := parsed.Hostname()
	if strings.ToLower(hostname) != hostname {
		return "", errors.New("host must be lowercase")
	}
	if strings.HasSuffix(hostname, ".") {
		return "", errors.New("host must not be fully qualified with a trailing dot")
	}
	canonicalHost := hostname
	if strings.Contains(hostname, ":") {
		canonicalHost = "[" + hostname + "]"
	}

	port := parsed.Port()
	if port == "" {
		if scheme == "http" && !isLoopbackHost(hostname) {
			return "", errors.New("http origins are only permitted on loopback hosts")
		}
		return scheme + "://" + canonicalHost, nil
	}

	// url.Parse already rejects non-numeric ports via a parse error, but guard
	// against embedded whitespace or other oddities explicitly.
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("invalid port %q", port)
		}
	}
	switch {
	case scheme == "https" && port == "443":
		return "https://" + canonicalHost, nil
	case scheme == "http" && port == "80":
		if !isLoopbackHost(hostname) {
			return "", errors.New("http origins are only permitted on loopback hosts")
		}
		return "http://" + canonicalHost, nil
	case scheme == "http" && !isLoopbackHost(hostname):
		return "", errors.New("http origins are only permitted on loopback hosts")
	}
	return scheme + "://" + canonicalHost + ":" + port, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "127.0.0.1" || host == "[::1]" {
		return true
	}
	// url.Parse strips the brackets when returning Hostname().
	return host == "::1"
}

// WellKnownURL builds the manifest URL for an RP ID as specified by the
// WebAuthn well-known origin policy: https://<rpId>/.well-known/webauthn.
func WellKnownURL(rpID string) (string, error) {
	if err := ValidateRPID(rpID); err != nil {
		return "", err
	}
	return "https://" + rpID + "/.well-known/webauthn", nil
}

// ValidateRPID checks that rpID is a registrable domain: no scheme, port,
// path, or wildcard; only DNS labels of letters, digits, and hyphens, or the
// special single label "localhost".
func ValidateRPID(rpID string) error {
	if rpID == "" || strings.HasPrefix(rpID, ".") || strings.HasSuffix(rpID, ".") {
		return fmt.Errorf("%w: %q", ErrRPID, rpID)
	}
	if strings.ContainsAny(rpID, "/:?#@* ") {
		return fmt.Errorf("%w: %q contains scheme, port, path, or wildcard characters", ErrRPID, rpID)
	}
	labels := strings.Split(rpID, ".")
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("%w: %q has an empty or hyphen-padded label", ErrRPID, rpID)
		}
		for _, r := range label {
			switch {
			case r >= 'a' && r <= 'z',
				r >= '0' && r <= '9',
				r == '-':
			default:
				return fmt.Errorf("%w: %q contains invalid character %q", ErrRPID, rpID, r)
			}
		}
	}
	return nil
}

// FetchManifest retrieves and parses the /.well-known/webauthn manifest for
// rpID using client (http.DefaultClient when nil). The response must be
// served over HTTPS with a JSON content type and remain within
// maxManifestBytes.
func FetchManifest(ctx context.Context, client *http.Client, rpID string) (*Manifest, error) {
	manifestURL, err := WellKnownURL(rpID)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManifest, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch failed: %v", ErrManifest, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: unexpected status %d", ErrManifest, resp.StatusCode)
	}
	if mediaType := resp.Header.Get("Content-Type"); !isJSONContentType(mediaType) {
		return nil, fmt.Errorf("%w: unexpected content type %q", ErrManifest, mediaType)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read failed: %v", ErrManifest, err)
	}
	if len(body) > maxManifestBytes {
		return nil, fmt.Errorf("%w: document larger than %d bytes", ErrManifest, maxManifestBytes)
	}
	return ParseManifest(body)
}

func isJSONContentType(contentType string) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	return mediaType == "application/json"
}
