package optmerge

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"strings"
)

// ParsePrincipalFile parses an OpenSSH-style authorized principals file.
//
// Each non-empty, non-comment line is either a bare principal or a
// comma-separated option list followed by a principal:
//
//	alice
//	command="/srv/bin/jump",permit-pty alice
//	source-address="10.0.0.0/8,2001:db8::/32" alice
//
// Only command, source-address and permit-pty are recognized; any other
// option causes ErrUnknownOption so that silently ignored configuration can
// never widen a session.
func ParsePrincipalFile(r io.Reader) ([]PrincipalLine, error) {
	scanner := bufio.NewScanner(r)
	var lines []PrincipalLine
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		line, err := parsePrincipalLine(text)
		if err != nil {
			return nil, fmt.Errorf("principal file line %d: %w", lineNo, err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func parsePrincipalLine(text string) (PrincipalLine, error) {
	if !quotesBalanced(text) {
		return PrincipalLine{}, fmt.Errorf("unterminated quoted string")
	}

	optionText, principal, hasOptions := splitOptionList(text)
	if principal == "" {
		return PrincipalLine{}, fmt.Errorf("missing principal")
	}
	line := PrincipalLine{Principal: principal}
	if !hasOptions {
		if isKnownOptionName(principal) {
			return PrincipalLine{}, fmt.Errorf("missing principal after options")
		}
		return line, nil
	}

	seen := make(map[string]bool)
	options, err := splitOptions(optionText)
	if err != nil {
		return PrincipalLine{}, err
	}
	for _, raw := range options {
		if err := applyOption(&line, seen, raw); err != nil {
			return PrincipalLine{}, err
		}
	}
	return line, nil
}

func quotesBalanced(text string) bool {
	quoted := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch == '\\' && i+1 < len(text) {
			i++
			continue
		}
		if ch == '"' {
			quoted = !quoted
		}
	}
	return !quoted
}

func isKnownOptionName(token string) bool {
	switch token {
	case optionCommand, optionSourceAddress, optionPermitPTY:
		return true
	default:
		return false
	}
}

// splitOptionList separates the comma-separated option list (everything up to
// the first unquoted whitespace) from the bare principal that follows it.
func splitOptionList(text string) (optionText, principal string, hasOptions bool) {
	quoted := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch == '\\' && quoted && i+1 < len(text) {
			i++
			continue
		}
		if ch == '"' {
			quoted = !quoted
			continue
		}
		if !quoted && (ch == ' ' || ch == '\t') {
			return text[:i], strings.TrimSpace(text[i:]), true
		}
	}
	return "", text, false
}

// splitOptions splits an option list on unquoted commas and unquotes each
// option, so values containing spaces or commas inside double quotes survive.
func splitOptions(optionText string) ([]string, error) {
	var options []string
	var current strings.Builder
	quoted := false

	flush := func() {
		if raw := strings.TrimSpace(current.String()); raw != "" {
			options = append(options, unquote(raw))
		}
		current.Reset()
	}

	for i := 0; i < len(optionText); i++ {
		ch := optionText[i]
		if ch == '\\' && quoted && i+1 < len(optionText) {
			current.WriteByte(optionText[i+1])
			i++
			continue
		}
		if ch == '"' {
			quoted = !quoted
			continue
		}
		if ch == ',' && !quoted {
			flush()
			continue
		}
		current.WriteByte(ch)
	}
	flush()
	if quoted {
		return nil, fmt.Errorf("unterminated quoted string")
	}
	return options, nil
}

// unquote strips a single pair of surrounding double quotes. Quoted strings
// have already had their backslash escapes consumed by splitOptions.
func unquote(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return value[1 : len(value)-1]
	}
	return value
}

// applyOption parses one option token, which is either a flag (permit-pty) or
// a key=value pair (command=..., source-address=...).
func applyOption(line *PrincipalLine, seen map[string]bool, raw string) error {
	name, value, hasValue := strings.Cut(raw, "=")
	if name == "" {
		return fmt.Errorf("%w: %q", ErrUnknownOption, raw)
	}
	if seen[name] {
		return fmt.Errorf("duplicate option %q", name)
	}
	seen[name] = true

	switch name {
	case optionCommand:
		if !hasValue || value == "" {
			return fmt.Errorf("option %q requires a value", optionCommand)
		}
		line.Command = value
		line.HasCommand = true
	case optionSourceAddress:
		if !hasValue || value == "" {
			return fmt.Errorf("option %q requires a value", optionSourceAddress)
		}
		prefixes, err := parseSourceAddresses(value)
		if err != nil {
			return err
		}
		line.SourceAddresses = prefixes
	case optionPermitPTY:
		if hasValue {
			return fmt.Errorf("option %q takes no value", optionPermitPTY)
		}
		line.PermitPTY = true
	default:
		return newUnknownOptionError(name)
	}
	return nil
}

func parseSourceAddresses(value string) ([]netip.Prefix, error) {
	parts := strings.Split(value, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid source-address %q: %w", part, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	if len(prefixes) == 0 {
		return nil, fmt.Errorf("option %q requires at least one network", optionSourceAddress)
	}
	return prefixes, nil
}
