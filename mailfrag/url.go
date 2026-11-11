package mailfrag

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// URL is a restricted URL safe for href/src in email HTML. Construct it
// with ParseURL; the zero value is invalid and fails rendering.
type URL struct {
	raw string
	ok  bool
}

// String returns the validated URL text.
func (u URL) String() string { return u.raw }

// ParseURL validates raw and returns a restricted URL. Only http, https
// and mailto are accepted. Obfuscation vectors are rejected outright:
// embedded whitespace or control characters, backslashes, missing scheme
// or host, and userinfo (which lets "http://trusted@evil" masquerade).
func ParseURL(raw string) (URL, error) {
	if raw == "" {
		return URL{}, errors.New("mailfrag: empty URL")
	}
	if !utf8.ValidString(raw) {
		return URL{}, errors.New("mailfrag: URL is not valid UTF-8")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return URL{}, fmt.Errorf("mailfrag: URL contains control character U+%04X", r)
		}
		if r == ' ' {
			return URL{}, errors.New("mailfrag: URL contains whitespace")
		}
	}
	if strings.Contains(raw, "\\") {
		return URL{}, errors.New("mailfrag: URL contains backslash")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return URL{}, fmt.Errorf("mailfrag: cannot parse URL: %w", err)
	}
	if u.User != nil {
		return URL{}, errors.New("mailfrag: URLs with userinfo are not allowed")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return URL{}, fmt.Errorf("mailfrag: %s URL requires a host", u.Scheme)
		}
	case "mailto":
		if u.Opaque == "" && u.Path == "" {
			return URL{}, errors.New("mailfrag: mailto URL requires an address")
		}
	default:
		return URL{}, fmt.Errorf("mailfrag: URL scheme %q is not allowed (want http, https or mailto)", u.Scheme)
	}
	return URL{raw: raw, ok: true}, nil
}
