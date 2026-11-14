package netsec

import (
	"fmt"
	"net"
	"strings"
)

func parseXForwardedFor(values []string, maxHops int) ([]net.IP, error) {
	var chain []net.IP
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				return nil, fmt.Errorf("empty X-Forwarded-For hop")
			}
			ip, err := parseIP(part)
			if err != nil {
				return nil, fmt.Errorf("invalid X-Forwarded-For hop %q", part)
			}
			chain = append(chain, ip)
			if len(chain) > maxHops {
				return nil, fmt.Errorf("X-Forwarded-For chain exceeds %d hops", maxHops)
			}
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("empty X-Forwarded-For chain")
	}
	return chain, nil
}

func parseForwarded(values []string, maxHops int) ([]net.IP, error) {
	var chain []net.IP
	for _, value := range values {
		elements, err := splitQuoted(value, ',')
		if err != nil {
			return nil, fmt.Errorf("invalid Forwarded header: %w", err)
		}
		for _, element := range elements {
			element = strings.TrimSpace(element)
			if element == "" {
				return nil, fmt.Errorf("empty Forwarded element")
			}
			forValue, err := forwardedFor(element)
			if err != nil {
				return nil, err
			}
			chain = append(chain, forValue)
			if len(chain) > maxHops {
				return nil, fmt.Errorf("Forwarded chain exceeds %d hops", maxHops)
			}
		}
	}
	if len(chain) == 0 {
		return nil, fmt.Errorf("empty Forwarded chain")
	}
	return chain, nil
}

func forwardedFor(element string) (net.IP, error) {
	parameters, err := splitQuoted(element, ';')
	if err != nil {
		return nil, fmt.Errorf("invalid Forwarded element %q: %w", element, err)
	}
	var (
		value string
		found bool
	)
	for _, parameter := range parameters {
		key, raw, ok := strings.Cut(parameter, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "for") {
			continue
		}
		if found {
			return nil, fmt.Errorf("duplicate for parameter in Forwarded element")
		}
		value = strings.TrimSpace(raw)
		if len(value) >= 2 && value[0] == '"' {
			unquoted, err := unquoteHTTP(value)
			if err != nil {
				return nil, fmt.Errorf("invalid quoted Forwarded for value: %w", err)
			}
			value = unquoted
		}
		found = true
	}
	if !found {
		return nil, fmt.Errorf("Forwarded element has no for parameter")
	}
	ip, err := parseIP(value)
	if err != nil {
		return nil, fmt.Errorf("invalid Forwarded for value %q", value)
	}
	return ip, nil
}

func splitQuoted(value string, separator byte) ([]string, error) {
	var (
		parts   []string
		start   int
		quoted  bool
		escaped bool
	)
	for i := 0; i < len(value); i++ {
		switch {
		case escaped:
			escaped = false
		case quoted && value[i] == '\\':
			escaped = true
		case value[i] == '"':
			quoted = !quoted
		case !quoted && value[i] == separator:
			parts = append(parts, value[start:i])
			start = i + 1
		}
	}
	if quoted || escaped {
		return nil, fmt.Errorf("unterminated quoted string")
	}
	parts = append(parts, value[start:])
	return parts, nil
}

func unquoteHTTP(value string) (string, error) {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", fmt.Errorf("not a quoted string")
	}
	var builder strings.Builder
	builder.Grow(len(value) - 2)
	for i := 1; i < len(value)-1; i++ {
		if value[i] != '\\' {
			builder.WriteByte(value[i])
			continue
		}
		i++
		if i >= len(value)-1 {
			return "", fmt.Errorf("trailing escape")
		}
		builder.WriteByte(value[i])
	}
	return builder.String(), nil
}
