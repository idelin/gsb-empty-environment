package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// Signature is the coverage-relevant portion of a DKIM-Signature field.
type Signature struct {
	Raw                    string
	Version                string
	Algorithm              string
	Domain                 string
	Selector               string
	Canonicalization       string
	HeaderCanonicalization CanonMode
	BodyCanonicalization   CanonMode
	BodyLength             *int64
	HeaderFields           []string
	BodyHash               string
	SignatureData          string
}

// HasBodyLength reports whether the signature contains an l= tag.
func (s Signature) HasBodyLength() bool {
	return s.BodyLength != nil
}

// BodyLengthValue returns l= and whether it was present.
func (s Signature) BodyLengthValue() (int64, bool) {
	if s.BodyLength == nil {
		return 0, false
	}
	return *s.BodyLength, true
}

// Identity returns a stable description of the signing identity and claim.
func (s Signature) Identity() string {
	length := "full"
	if s.BodyLength != nil {
		length = "l=" + strconv.FormatInt(*s.BodyLength, 10)
	}
	return fmt.Sprintf("d=%s;s=%s;a=%s;c=%s;%s",
		s.Domain, s.Selector, s.Algorithm, s.Canonicalization, length)
}

// SignatureVerification pairs a parsed signature with the result of the
// gateway's cryptographic verification. Header may be supplied instead of a
// pre-parsed Signature.
type SignatureVerification struct {
	Header    string
	Signature Signature
	Verified  bool
	Error     string
}

type Verification = SignatureVerification
type SignatureResult = SignatureVerification

// ParseVerification parses a DKIM-Signature header and records the crypto
// verification result.
func ParseVerification(header string, verified bool) (SignatureVerification, error) {
	sig, err := ParseDKIMSignature(header)
	if err != nil {
		return SignatureVerification{Header: header, Error: err.Error()}, err
	}
	return SignatureVerification{Header: header, Signature: sig, Verified: verified}, nil
}

// ParseSignature is an alias for ParseDKIMSignature.
func ParseSignature(header string) (Signature, error) {
	return ParseDKIMSignature(header)
}

// ParseDKIMSignature parses one DKIM-Signature field value. A leading
// "DKIM-Signature:" field name and RFC 5322 folding are accepted.
func ParseDKIMSignature(header string) (Signature, error) {
	value, err := unfoldDKIMHeader(header)
	if err != nil {
		return Signature{}, err
	}

	sig := Signature{
		Raw:                    header,
		Canonicalization:       "simple/simple",
		HeaderCanonicalization: CanonSimple,
		BodyCanonicalization:   CanonSimple,
	}
	seen := make(map[string]bool)
	for _, field := range splitTagList(value) {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		eq := strings.IndexByte(field, '=')
		if eq < 0 {
			return Signature{}, fmt.Errorf("malformed DKIM tag %q", field)
		}
		name := strings.ToLower(strings.TrimSpace(field[:eq]))
		tagValue := strings.TrimSpace(field[eq+1:])
		if name == "" {
			return Signature{}, fmt.Errorf("empty DKIM tag name")
		}
		if seen[name] {
			return Signature{}, fmt.Errorf("duplicate DKIM tag %q", name)
		}
		seen[name] = true

		switch name {
		case "v":
			sig.Version = unquote(tagValue)
		case "a":
			sig.Algorithm = unquote(tagValue)
		case "d":
			sig.Domain = unquote(tagValue)
		case "s":
			sig.Selector = unquote(tagValue)
		case "c":
			headerMode, bodyMode, canonical, err := parseCanonicalization(unquote(tagValue))
			if err != nil {
				return Signature{}, err
			}
			sig.HeaderCanonicalization = headerMode
			sig.BodyCanonicalization = bodyMode
			sig.Canonicalization = canonical
		case "l":
			if strings.HasPrefix(tagValue, "\"") {
				return Signature{}, fmt.Errorf("DKIM l= must not be quoted")
			}
			length, err := parseBodyLength(tagValue)
			if err != nil {
				return Signature{}, err
			}
			sig.BodyLength = &length
		case "h":
			for _, fieldName := range strings.Split(unquote(tagValue), ":") {
				fieldName = strings.TrimSpace(fieldName)
				if fieldName != "" {
					sig.HeaderFields = append(sig.HeaderFields, fieldName)
				}
			}
		case "bh":
			sig.BodyHash = collapseWhitespace(unquote(tagValue))
		case "b":
			sig.SignatureData = collapseWhitespace(unquote(tagValue))
		}
	}

	if sig.Version != "" && sig.Version != "1" {
		return Signature{}, fmt.Errorf("unsupported DKIM version %q", sig.Version)
	}
	return sig, nil
}

func unfoldDKIMHeader(header string) (string, error) {
	text := strings.ReplaceAll(header, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := range lines {
		if i == 0 {
			if colon := strings.IndexByte(lines[i], ':'); colon >= 0 {
				name := strings.TrimSpace(lines[i][:colon])
				if strings.EqualFold(name, "DKIM-Signature") {
					lines[i] = lines[i][colon+1:]
				}
			}
			continue
		}
		if strings.TrimSpace(lines[i]) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = " " + strings.TrimLeft(lines[i], " \t")
	}
	value := strings.TrimSpace(strings.Join(lines, ""))
	if value == "" {
		return "", fmt.Errorf("empty DKIM-Signature value")
	}
	return value, nil
}

func splitTagList(value string) []string {
	var fields []string
	start := 0
	quoted := false
	escaped := false
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case escaped:
			escaped = false
		case c == '\\' && quoted:
			escaped = true
		case c == '"':
			quoted = !quoted
		case c == ';' && !quoted:
			fields = append(fields, value[start:i])
			start = i + 1
		}
	}
	fields = append(fields, value[start:])
	return fields
}

func parseCanonicalization(value string) (headerMode, bodyMode CanonMode, normalized string, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(value)), "/")
	if len(parts) > 2 {
		return "", "", "", fmt.Errorf("invalid DKIM canonicalization %q", value)
	}
	headerMode, err = parseCanonMode(parts[0])
	if err != nil {
		return "", "", "", err
	}
	bodyMode = headerMode
	if len(parts) == 2 {
		bodyMode, err = parseCanonMode(parts[1])
		if err != nil {
			return "", "", "", err
		}
	}
	return headerMode, bodyMode, string(headerMode) + "/" + string(bodyMode), nil
}

func parseCanonMode(value string) (CanonMode, error) {
	switch CanonMode(strings.TrimSpace(value)) {
	case CanonSimple:
		return CanonSimple, nil
	case CanonRelaxed:
		return CanonRelaxed, nil
	default:
		return "", fmt.Errorf("unknown DKIM canonicalization %q", value)
	}
}

func parseBodyLength(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty DKIM l= value")
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("invalid DKIM l= value %q", value)
		}
	}
	length, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid DKIM l= value %q: %w", value, err)
	}
	return length, nil
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
		value = strings.ReplaceAll(value, `\"`, `"`)
		value = strings.ReplaceAll(value, `\\`, `\`)
	}
	return value
}

func collapseWhitespace(value string) string {
	return strings.Join(strings.Fields(value), "")
}
