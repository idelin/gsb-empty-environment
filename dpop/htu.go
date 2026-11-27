package dpop

import (
	"net/http"
	"net/url"
	"strings"
)

// DefaultHTU builds the canonical htu from the request URL, dropping any query
// string and fragment. It honors X-Forwarded-Proto / X-Forwarded-Host when
// present, which makes it usable behind a terminating reverse proxy.
func DefaultHTU(r *http.Request) (string, error) {
	if r.URL == nil {
		return "", newError(ErrInvalidDPoP, "request has no URL")
	}

	scheme := "https"
	if forwarded := firstForwardedValue(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = forwarded
	} else if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on") {
		scheme = "http"
	}

	host := r.Host
	if forwarded := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); forwarded != "" {
		host = forwarded
	}
	if host == "" {
		return "", newError(ErrInvalidDPoP, "request has no host")
	}

	canonical := url.URL{
		Scheme: strings.ToLower(scheme),
		Host:   strings.ToLower(host),
		Path:   r.URL.EscapedPath(),
	}
	return canonical.String(), nil
}

// BaseURLHTU returns an HTUFunc pinned to an explicit gateway base URL, for
// example "https://api.example.com". Request paths are appended while query
// and fragment components are always stripped.
func BaseURLHTU(base string) (func(r *http.Request) (string, error), error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, newError(ErrInvalidDPoP, "base URL must include scheme and host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, newError(ErrInvalidDPoP, "base URL must not contain query or fragment")
	}
	pinned := url.URL{
		Scheme: strings.ToLower(parsed.Scheme),
		Host:   strings.ToLower(parsed.Host),
	}
	return func(r *http.Request) (string, error) {
		target := pinned
		target.Path = r.URL.EscapedPath()
		return target.String(), nil
	}, nil
}

// compareHTU compares the proof htu against the canonical request URL. The
// proof htu must itself be free of query/fragment components; scheme and host
// are case-insensitive while the path remains case-sensitive.
func compareHTU(proofHTU, expectedHTU string) error {
	if proofHTU == "" {
		return newError(ErrInvalidDPoP, "DPoP proof is missing the htu claim")
	}
	proofURL, err := url.Parse(proofHTU)
	if err != nil {
		return newError(ErrInvalidDPoP, "htu claim is not a valid URL")
	}
	if proofURL.ForceQuery || proofURL.RawQuery != "" || proofURL.Fragment != "" {
		return newError(ErrInvalidDPoP, "htu claim must not contain query or fragment components")
	}
	if !strings.EqualFold(proofURL.Scheme, "http") && !strings.EqualFold(proofURL.Scheme, "https") {
		return newError(ErrInvalidDPoP, "htu claim must use an http or https URL")
	}
	expectedURL, err := url.Parse(expectedHTU)
	if err != nil {
		return newError(ErrInvalidDPoP, "canonical target URL is invalid")
	}
	if !strings.EqualFold(proofURL.Scheme, expectedURL.Scheme) ||
		!strings.EqualFold(proofURL.Host, expectedURL.Host) {
		return newError(ErrInvalidDPoP, "htu claim does not match the canonical target URL")
	}
	if proofURL.EscapedPath() != expectedURL.EscapedPath() {
		return newError(ErrInvalidDPoP, "htu claim path does not match the canonical target URL")
	}
	return nil
}

func firstForwardedValue(value string) string {
	if value == "" {
		return ""
	}
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	return strings.TrimSpace(value)
}
