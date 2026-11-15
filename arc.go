package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// HeaderType is one of the three ARC header field types.
type HeaderType string

const (
	ARCSeal                  HeaderType = "ARC-Seal"
	ARCMessageSignature      HeaderType = "ARC-Message-Signature"
	ARCAuthenticationResults HeaderType = "ARC-Authentication-Results"
)

// Header is an unfolded ARC header field. Value is the field value without
// the field name or colon, for example:
//
//	"i=1; a=rsa-sha256; cv=none; b=..."
type Header struct {
	Type  HeaderType
	Value string
}

// String renders the header in wire form.
func (h Header) String() string {
	return string(h.Type) + ": " + h.Value
}

// Instance returns the ARC instance number from the header's i tag.
func (h Header) Instance() (int, error) {
	if !h.Type.valid() {
		return 0, fmt.Errorf("%w: %q", ErrUnknownHeaderType, h.Type)
	}
	fields, err := parseHeaderFields(h, false)
	if err != nil {
		return 0, err
	}
	return fields.instance, nil
}

// VerifyFunc verifies one ARC signature. The caller supplies the message bytes
// and public keys or verification context by closing over them.
//
// VerifyFunc is called for every ARC-Seal and for the most recent
// ARC-Message-Signature. For an ARC-Seal, covered contains every ARC header
// whose instance is greater than or equal to the seal's instance, in the order
// the headers were received. The seal itself is included; the verifier should
// exclude its b tag from canonicalization as required by RFC 8617. For the
// latest ARC-Message-Signature, covered is nil because that signature covers
// the message rather than the ARC headers.
type VerifyFunc func(header Header, covered []Header) error

var (
	ErrEmptyChain          = errors.New("arc: empty chain")
	ErrUnknownHeaderType   = errors.New("arc: unknown header type")
	ErrMalformedHeader     = errors.New("arc: malformed header value")
	ErrInvalidInstance     = errors.New("arc: invalid instance")
	ErrMissingInstance     = errors.New("arc: missing instance")
	ErrDuplicateInstance   = errors.New("arc: duplicate instance")
	ErrDuplicateHeader     = errors.New("arc: duplicate header type for instance")
	ErrMissingHeader       = errors.New("arc: missing header type for instance")
	ErrInvalidCV           = errors.New("arc: invalid chain validation value")
	ErrChainRejected       = errors.New("arc: chain rejected by cv value")
	ErrNilVerifier         = errors.New("arc: nil signature verifier")
	ErrSealVerification    = errors.New("arc: ARC-Seal signature verification failed")
	ErrMessageVerification = errors.New("arc: latest ARC-Message-Signature verification failed")
)

// ParseHeaderLine parses one unfolded ARC header line, including its field
// name and colon. Field names are matched case-insensitively.
func ParseHeaderLine(line string) (Header, error) {
	line = strings.TrimSuffix(line, "\r")
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		return Header{}, fmt.Errorf("%w: missing field name separator", ErrMalformedHeader)
	}

	name := strings.TrimSpace(line[:colon])
	var kind HeaderType
	switch strings.ToLower(name) {
	case "arc-seal":
		kind = ARCSeal
	case "arc-message-signature":
		kind = ARCMessageSignature
	case "arc-authentication-results":
		kind = ARCAuthenticationResults
	default:
		return Header{}, fmt.Errorf("%w: %q", ErrUnknownHeaderType, name)
	}

	return Header{
		Type:  kind,
		Value: strings.TrimSpace(line[colon+1:]),
	}, nil
}

// ParseHeaderLines parses a sequence of unfolded ARC header lines.
func ParseHeaderLines(lines []string) ([]Header, error) {
	headers := make([]Header, 0, len(lines))
	for index, line := range lines {
		header, err := ParseHeaderLine(line)
		if err != nil {
			return nil, fmt.Errorf("arc: header line %d: %w", index+1, err)
		}
		headers = append(headers, header)
	}
	return headers, nil
}

// ValidateStructure checks the ARC chain's instances, one-header-per-type
// rule, and cv values. It returns the number of ARC sets in the chain.
func ValidateStructure(headers []Header) (int, error) {
	chain, err := parseChain(headers)
	if err != nil {
		return 0, err
	}
	return chain.n, nil
}

// ValidateChain validates the structure and cv rules of an ARC chain, verifies
// every ARC-Seal over its complete suffix of ARC headers, and verifies the most
// recent ARC-Message-Signature.
//
// The headers must represent instances 1 through N with exactly one ARC-Seal,
// one ARC-Message-Signature, and one ARC-Authentication-Results in each set.
// ARC-Seal cv must be "none" at instance 1 and "pass" or "fail" at every later
// instance. A cv of "fail" rejects the chain.
func ValidateChain(headers []Header, verify VerifyFunc) error {
	if verify == nil {
		return ErrNilVerifier
	}

	chain, err := parseChain(headers)
	if err != nil {
		return err
	}

	for instance := 1; instance <= chain.n; instance++ {
		seal := chain.sets[instance][ARCSeal]
		covered := make([]Header, 0, chain.n-instance+1)
		for _, candidate := range chain.headers {
			if candidate.instance >= instance {
				covered = append(covered, candidate.header)
			}
		}
		if err := verify(seal.header, covered); err != nil {
			return fmt.Errorf("%w: instance %d: %w", ErrSealVerification, instance, err)
		}
	}

	latestAMS := chain.sets[chain.n][ARCMessageSignature]
	if err := verify(latestAMS.header, nil); err != nil {
		return fmt.Errorf("%w: instance %d: %w", ErrMessageVerification, chain.n, err)
	}

	return nil
}

type parsedHeader struct {
	header   Header
	instance int
	cv       string
}

type parsedChain struct {
	headers []parsedHeader
	sets    map[int]map[HeaderType]parsedHeader
	n       int
}

type headerFields struct {
	instance int
	cv       string
}

func parseChain(headers []Header) (*parsedChain, error) {
	if len(headers) == 0 {
		return nil, ErrEmptyChain
	}

	chain := &parsedChain{
		headers: make([]parsedHeader, 0, len(headers)),
		sets:    make(map[int]map[HeaderType]parsedHeader),
	}

	for _, header := range headers {
		if !header.Type.valid() {
			return nil, fmt.Errorf("%w: %q", ErrUnknownHeaderType, header.Type)
		}

		fields, err := parseHeaderFields(header, true)
		if err != nil {
			return nil, err
		}

		parsed := parsedHeader{
			header:   header,
			instance: fields.instance,
			cv:       fields.cv,
		}
		chain.headers = append(chain.headers, parsed)

		set, ok := chain.sets[fields.instance]
		if !ok {
			set = make(map[HeaderType]parsedHeader, 3)
			chain.sets[fields.instance] = set
		}
		if _, exists := set[header.Type]; exists {
			return nil, fmt.Errorf(
				"%w: instance %d: %w: %s",
				ErrDuplicateInstance, fields.instance, ErrDuplicateHeader, header.Type,
			)
		}
		set[header.Type] = parsed
	}

	chain.n = len(chain.sets)
	for instance := 1; instance <= chain.n; instance++ {
		set, ok := chain.sets[instance]
		if !ok {
			return nil, fmt.Errorf("%w: instance %d", ErrMissingInstance, instance)
		}
		for _, kind := range []HeaderType{ARCSeal, ARCMessageSignature, ARCAuthenticationResults} {
			if _, ok := set[kind]; !ok {
				return nil, fmt.Errorf("%w: instance %d: %s", ErrMissingHeader, instance, kind)
			}
		}
	}

	for instance := 1; instance <= chain.n; instance++ {
		seal := chain.sets[instance][ARCSeal]
		if seal.cv == "fail" {
			return nil, fmt.Errorf("%w: ARC-Seal instance %d has cv=fail", ErrChainRejected, instance)
		}
	}

	return chain, nil
}

func parseHeaderFields(header Header, requireCV bool) (headerFields, error) {
	parts, err := splitHeaderValue(header.Value)
	if err != nil {
		return headerFields{}, err
	}

	var fields headerFields
	var haveInstance bool
	var haveCV bool

	for _, part := range parts {
		name, rawValue, ok := splitAttribute(part)
		if !ok {
			// ARC-Authentication-Results permits a bare authserv-id token.
			continue
		}

		switch strings.ToLower(name) {
		case "i":
			if haveInstance {
				return headerFields{}, fmt.Errorf("%w: duplicate i tag", ErrInvalidInstance)
			}
			instance, err := parseInstance(rawValue)
			if err != nil {
				return headerFields{}, err
			}
			fields.instance = instance
			haveInstance = true
		case "cv":
			if header.Type != ARCSeal {
				continue
			}
			if haveCV {
				return headerFields{}, fmt.Errorf("%w: duplicate cv tag", ErrInvalidCV)
			}
			value, err := unquoteHeaderValue(rawValue)
			if err != nil {
				return headerFields{}, err
			}
			fields.cv = strings.ToLower(value)
			haveCV = true
		}
	}

	if !haveInstance {
		return headerFields{}, fmt.Errorf("%w: missing i tag", ErrInvalidInstance)
	}

	if requireCV && header.Type == ARCSeal {
		if !haveCV {
			return headerFields{}, fmt.Errorf("%w: instance %d: missing cv tag", ErrInvalidCV, fields.instance)
		}
		switch {
		case fields.instance == 1 && fields.cv != "none":
			return headerFields{}, fmt.Errorf(
				"%w: instance 1 must have cv=none, got %q",
				ErrInvalidCV, fields.cv,
			)
		case fields.instance > 1 && fields.cv != "pass" && fields.cv != "fail":
			return headerFields{}, fmt.Errorf(
				"%w: instance %d must have cv=pass or cv=fail, got %q",
				ErrInvalidCV, fields.instance, fields.cv,
			)
		}
	}

	return fields, nil
}

func (t HeaderType) valid() bool {
	switch t {
	case ARCSeal, ARCMessageSignature, ARCAuthenticationResults:
		return true
	default:
		return false
	}
}

func parseInstance(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("%w: empty i tag", ErrInvalidInstance)
	}
	for index := 0; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, fmt.Errorf("%w: i tag %q is not a decimal integer", ErrInvalidInstance, raw)
		}
	}
	if len(raw) > 1 && raw[0] == '0' {
		return 0, fmt.Errorf("%w: i tag %q has a leading zero", ErrInvalidInstance, raw)
	}

	instance, err := strconv.Atoi(raw)
	if err != nil || instance < 1 {
		return 0, fmt.Errorf("%w: i tag %q", ErrInvalidInstance, raw)
	}
	return instance, nil
}

func splitAttribute(part string) (name, value string, ok bool) {
	part = strings.TrimSpace(part)
	equals := strings.IndexByte(part, '=')
	if equals < 0 {
		return "", "", false
	}
	name = strings.TrimSpace(part[:equals])
	if name == "" {
		return "", "", false
	}
	return name, strings.TrimSpace(part[equals+1:]), true
}

func unquoteHeaderValue(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw[0] != '"' {
		return raw, nil
	}
	if len(raw) < 2 || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("%w: unterminated quoted value", ErrMalformedHeader)
	}

	var value strings.Builder
	value.Grow(len(raw) - 2)
	for index := 1; index < len(raw)-1; index++ {
		if raw[index] == '\\' {
			index++
			if index >= len(raw)-1 {
				return "", fmt.Errorf("%w: trailing escape in quoted value", ErrMalformedHeader)
			}
		}
		value.WriteByte(raw[index])
	}
	return value.String(), nil
}

func splitHeaderValue(value string) ([]string, error) {
	var parts []string
	start := 0
	inQuote := false
	escaped := false
	commentDepth := 0

	for index := 0; index < len(value); index++ {
		char := value[index]
		if escaped {
			escaped = false
			continue
		}
		if inQuote {
			switch char {
			case '\\':
				escaped = true
			case '"':
				inQuote = false
			}
			continue
		}

		switch char {
		case '"':
			inQuote = true
		case '(':
			commentDepth++
		case ')':
			if commentDepth > 0 {
				commentDepth--
			}
		case ';':
			if commentDepth == 0 {
				parts = append(parts, value[start:index])
				start = index + 1
			}
		}
	}

	if inQuote || commentDepth != 0 {
		return nil, fmt.Errorf("%w: unterminated quoted string or comment", ErrMalformedHeader)
	}
	parts = append(parts, value[start:])
	return parts, nil
}
