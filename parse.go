package netsec

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

func parseAlternative(origin Origin, value string, now time.Time) (Entry, bool) {
	equal := strings.IndexByte(value, '=')
	if equal <= 0 {
		return Entry{}, false
	}
	protocol, ok := normalizeProtocol(value[:equal])
	if !ok {
		return Entry{}, false
	}

	parts := splitOutsideQuotes(value[equal+1:], ';')
	if len(parts) == 0 {
		return Entry{}, false
	}
	authority, err := unquoteValue(parts[0])
	if err != nil {
		return Entry{}, false
	}
	host, port, ok := parseAuthority(authority, origin.Host)
	if !ok {
		return Entry{}, false
	}

	maxAge := DefaultMaxAge
	persist := false
	for _, param := range parts[1:] {
		param = strings.TrimSpace(param)
		if param == "" {
			continue
		}
		name, value, hasValue := strings.Cut(param, "=")
		name = strings.TrimSpace(name)
		switch {
		case strings.EqualFold(name, "ma"):
			if !hasValue {
				return Entry{}, false
			}
			maxAge, ok = parseMaxAge(value)
			if !ok {
				return Entry{}, false
			}
		case strings.EqualFold(name, "persist"):
			if !hasValue {
				persist = true
				continue
			}
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "1", "true":
				persist = true
			case "0", "false":
				persist = false
			default:
				persist = false
			}
		}
	}

	return Entry{
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Expires:  now.Add(maxAge),
		Persist:  persist,
	}, true
}

func parseAuthority(authority, defaultHost string) (string, int, bool) {
	authority = strings.TrimSpace(authority)
	if authority == "" {
		return "", 0, false
	}

	var host, portText string
	if strings.HasPrefix(authority, ":") {
		host = defaultHost
		portText = authority[1:]
	} else {
		var err error
		host, portText, err = net.SplitHostPort(authority)
		if err != nil {
			return "", 0, false
		}
	}
	if portText == "" {
		return "", 0, false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	if host == "" {
		host = defaultHost
	}
	host, err = normalizeHost(host)
	if err != nil {
		return "", 0, false
	}
	return host, port, true
}

func parseMaxAge(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}

	seconds, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		// An all-digit value that overflows uint64 is still subject to the
		// seven-day cap rather than being treated as a malformed lifetime.
		return MaxMaxAge, true
	}
	if seconds > uint64(MaxMaxAge/time.Second) {
		return MaxMaxAge, true
	}
	return time.Duration(seconds) * time.Second, true
}

func unquoteValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("altsvc: empty field value")
	}
	if value[0] != '"' {
		if strings.ContainsRune(value, '"') {
			return "", fmt.Errorf("altsvc: malformed quoted value")
		}
		return value, nil
	}
	if len(value) < 2 || value[len(value)-1] != '"' {
		return "", fmt.Errorf("altsvc: malformed quoted value")
	}
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return "", fmt.Errorf("altsvc: malformed quoted value: %w", err)
	}
	return unquoted, nil
}

func splitOutsideQuotes(value string, separator byte) []string {
	var parts []string
	start := 0
	inQuote := false
	escaped := false
	for i := 0; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case inQuote && value[i] == '\\':
			escaped = true
		case value[i] == '"':
			inQuote = !inQuote
		case value[i] == separator && !inQuote:
			parts = append(parts, value[start:i])
			start = i + 1
		}
	}
	parts = append(parts, value[start:])
	return parts
}
