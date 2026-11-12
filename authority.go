package netsec

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// authority is a normalized scheme + host + port triple used both for the
// caller-registered allow list and for matching outgoing requests.
type authority struct {
	scheme string
	host   string // lower-cased host name, or bare (bracket-less) IP literal
	port   int
}

// key is the canonical form used as the allow-list map key.
type key struct {
	scheme string
	host   string
	port   int
}

func defaultPort(scheme string) (int, bool) {
	switch scheme {
	case "http":
		return 80, true
	case "https":
		return 443, true
	default:
		return 0, false
	}
}

// splitHostPort splits an authority into host and port. A port is optional:
// when absent the scheme default is filled in. IPv6 literals must be wrapped
// in square brackets (RFC 3986), e.g. "[2001:db8::1]:8443".
func splitHostPort(hostport string, scheme string) (string, int, error) {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return "", 0, fmt.Errorf("netsec: empty authority")
	}

	def, ok := defaultPort(scheme)
	if !ok {
		return "", 0, fmt.Errorf("netsec: unsupported scheme %q", scheme)
	}

	if strings.HasPrefix(hostport, "[") {
		closeBracket := strings.IndexByte(hostport, ']')
		if closeBracket < 0 {
			return "", 0, fmt.Errorf("netsec: authority %q has unterminated IPv6 bracket", hostport)
		}
		literal := hostport[1:closeBracket]
		rest := hostport[closeBracket+1:]
		if rest == "" {
			return literal, def, nil
		}
		if rest[0] != ':' {
			return "", 0, fmt.Errorf("netsec: authority %q has invalid text after IPv6 literal", hostport)
		}
		port, err := parsePort(rest[1:])
		if err != nil {
			return "", 0, err
		}
		return literal, port, nil
	}

	if strings.Count(hostport, ":") > 1 {
		return "", 0, fmt.Errorf("netsec: IPv6 literal in authority %q must be wrapped in brackets", hostport)
	}

	host, portText, found := strings.Cut(hostport, ":")
	if !found {
		return host, def, nil
	}
	port, err := parsePort(portText)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func parsePort(text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, fmt.Errorf("netsec: empty port")
	}
	port, err := strconv.Atoi(text)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("netsec: invalid port %q", text)
	}
	return port, nil
}

// normalizeAuthority parses and canonicalizes a scheme and authority. Host
// names are lower-cased; missing ports default to 80 for http and 443 for
// https; bracketed IPv6 literals are stored without brackets internally.
func normalizeAuthority(scheme, raw string) (authority, error) {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	host, port, err := splitHostPort(raw, scheme)
	if err != nil {
		return authority{}, err
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return authority{}, fmt.Errorf("netsec: empty host in authority %q", raw)
	}

	if strings.HasPrefix(host, "[") || strings.HasSuffix(host, "]") {
		return authority{}, fmt.Errorf("netsec: malformed IPv6 literal in authority %q", raw)
	}

	// IP literals (v4 or v6 without brackets at this point) are parsed once
	// and stored in their canonical textual form.
	if addr, err := netip.ParseAddr(host); err == nil {
		host = addr.String()
	} else {
		host = strings.ToLower(host)
		if !validHostname(host) {
			return authority{}, fmt.Errorf("netsec: invalid host %q", host)
		}
	}

	return authority{scheme: scheme, host: host, port: port}, nil
}

func validHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if strings.ContainsAny(host, " \t\r\n/") {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				continue
			}
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return true
}

func (a authority) mapKey() key {
	return key{scheme: a.scheme, host: a.host, port: a.port}
}

// String renders the canonical authority as it must appear on the wire
// (Host header), including brackets around IPv6 literals.
func (a authority) String() string {
	if addr, err := netip.ParseAddr(a.host); err == nil && addr.Is6() {
		return "[" + addr.String() + "]:" + strconv.Itoa(a.port)
	}
	return net.JoinHostPort(a.host, strconv.Itoa(a.port))
}

// sameAuthority reports whether two authority strings normalize to the same
// scheme/host/port, e.g. when comparing a request Host header against the
// request authority.
func sameAuthority(scheme, expected, candidate string) bool {
	want, err := normalizeAuthority(scheme, expected)
	if err != nil {
		return false
	}
	got, err := normalizeAuthority(scheme, candidate)
	if err != nil {
		return false
	}
	return want.mapKey() == got.mapKey()
}
