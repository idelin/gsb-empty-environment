package netsec

import (
	"errors"
	"net/url"
	"sort"
	"strings"
)

// PURL holds the decoded components of a Package URL
// (https://github.com/package-url/purl-spec).
type PURL struct {
	Type       string
	Namespace  string
	Name       string
	Version    string
	Qualifiers map[string]string
	Subpath    string
}

// Parse decodes a purl string into its components. All components are
// percent-decoded; qualifier keys are lowercased; "." and ".." subpath
// segments are dropped.
func Parse(s string) (*PURL, error) {
	const scheme = "pkg:"
	if !strings.HasPrefix(s, scheme) {
		return nil, errors.New("purl: missing \"pkg:\" scheme")
	}
	body := s[len(scheme):]
	if body == "" {
		return nil, errors.New("purl: empty purl body")
	}

	var rawSubpath string
	if i := strings.IndexByte(body, '#'); i >= 0 {
		rawSubpath = body[i+1:]
		body = body[:i]
	}

	var rawQualifiers string
	if i := strings.IndexByte(body, '?'); i >= 0 {
		rawQualifiers = body[i+1:]
		body = body[:i]
	}

	var rawVersion string
	if i := strings.IndexByte(body, '@'); i >= 0 {
		rawVersion = body[i+1:]
		body = body[:i]
	}

	segments := strings.Split(body, "/")
	if len(segments) < 2 || segments[0] == "" || segments[len(segments)-1] == "" {
		return nil, errors.New("purl: expected pkg:type/[namespace/]name")
	}

	p := &PURL{}
	var err error
	if p.Type, err = decodeComponent(segments[0]); err != nil {
		return nil, err
	}
	if p.Name, err = decodeComponent(segments[len(segments)-1]); err != nil {
		return nil, err
	}
	if len(segments) > 2 {
		namespace := make([]string, 0, len(segments)-2)
		for _, segment := range segments[1 : len(segments)-1] {
			decoded, err := decodeComponent(segment)
			if err != nil {
				return nil, err
			}
			namespace = append(namespace, decoded)
		}
		p.Namespace = strings.Join(namespace, "/")
	}
	if p.Version, err = decodeComponent(rawVersion); err != nil {
		return nil, err
	}

	if rawQualifiers != "" {
		p.Qualifiers = make(map[string]string)
		for _, pair := range strings.Split(rawQualifiers, "&") {
			equal := strings.IndexByte(pair, '=')
			if equal <= 0 {
				return nil, errors.New("purl: qualifier must be key=value")
			}
			key, err := decodeComponent(pair[:equal])
			if err != nil {
				return nil, err
			}
			value, err := decodeComponent(pair[equal+1:])
			if err != nil {
				return nil, err
			}
			p.Qualifiers[strings.ToLower(key)] = value
		}
	}

	if rawSubpath != "" {
		subpath := make([]string, 0)
		for _, segment := range strings.Split(rawSubpath, "/") {
			decoded, err := decodeComponent(segment)
			if err != nil {
				return nil, err
			}
			if decoded == "" || decoded == "." || decoded == ".." {
				continue
			}
			subpath = append(subpath, decoded)
		}
		p.Subpath = strings.Join(subpath, "/")
	}

	return p, nil
}

// lowerCaseNameTypes lists purl types whose namespace and name are
// case-insensitive and must be canonicalized to lowercase.
var lowerCaseNameTypes = map[string]bool{
	"github":    true,
	"bitbucket": true,
}

// Normalize applies canonical case rules in place: the type is always
// lowercased; github/bitbucket namespace and name are lowercased; every
// other type (notably maven) keeps its original casing.
func (p *PURL) Normalize() *PURL {
	p.Type = strings.ToLower(p.Type)
	if lowerCaseNameTypes[p.Type] {
		p.Namespace = strings.ToLower(p.Namespace)
		p.Name = strings.ToLower(p.Name)
	}
	return p
}

// String renders the canonical purl. Qualifiers are emitted with
// lowercased keys sorted lexicographically; decoded reserved characters
// are percent-encoded again using uppercase hex.
func (p *PURL) String() string {
	var builder strings.Builder
	builder.WriteString("pkg:")
	builder.WriteString(escapeComponent(p.Type, escapeType))
	if p.Namespace != "" {
		for _, segment := range strings.Split(p.Namespace, "/") {
			builder.WriteByte('/')
			builder.WriteString(escapeComponent(segment, escapePath))
		}
	}
	builder.WriteByte('/')
	builder.WriteString(escapeComponent(p.Name, escapePath))
	if p.Version != "" {
		builder.WriteByte('@')
		builder.WriteString(escapeComponent(p.Version, escapePath))
	}
	if len(p.Qualifiers) > 0 {
		keys := make([]string, 0, len(p.Qualifiers))
		for key := range p.Qualifiers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder.WriteByte('?')
		for i, key := range keys {
			if i > 0 {
				builder.WriteByte('&')
			}
			builder.WriteString(escapeComponent(key, escapeQualifierKey))
			builder.WriteByte('=')
			builder.WriteString(escapeComponent(p.Qualifiers[key], escapeQualifierValue))
		}
	}
	if p.Subpath != "" {
		builder.WriteByte('#')
		for i, segment := range strings.Split(p.Subpath, "/") {
			if i > 0 {
				builder.WriteByte('/')
			}
			builder.WriteString(escapeComponent(segment, escapePath))
		}
	}
	return builder.String()
}

// NormalizeString parses a purl, applies canonicalization and returns the
// canonical form. Equivalent purls (different case/encoding/qualifier
// order) produce identical output.
func NormalizeString(purl string) (string, error) {
	parsed, err := Parse(purl)
	if err != nil {
		return "", err
	}
	parsed.Normalize()
	return parsed.String(), nil
}

func decodeComponent(value string) (string, error) {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", errors.New("purl: invalid percent-encoding: " + err.Error())
	}
	return decoded, nil
}

type escapeMode int

const (
	escapeType escapeMode = iota
	escapePath
	escapeQualifierKey
	escapeQualifierValue
)

const hexUpper = "0123456789ABCDEF"

func escapeComponent(value string, mode escapeMode) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isAllowed(c, mode) {
			builder.WriteByte(c)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hexUpper[c>>4])
		builder.WriteByte(hexUpper[c&0x0f])
	}
	return builder.String()
}

func isAllowed(c byte, mode escapeMode) bool {
	switch mode {
	case escapeType:
		return isAlphaNumeric(c) || c == '.' || c == '+' || c == '-'
	case escapeQualifierKey:
		return isUnreserved(c)
	default: // escapePath, escapeQualifierValue
		return isUnreserved(c) || isSubDelimiter(c) || c == ':' || c == '@'
	}
}

func isAlphaNumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isUnreserved(c byte) bool {
	return isAlphaNumeric(c) || c == '-' || c == '.' || c == '_' || c == '~'
}

func isSubDelimiter(c byte) bool {
	switch c {
	case '!', '$', '&', '\'', '(', ')', '*', '+', ',', ';', '=':
		return true
	}
	return false
}
