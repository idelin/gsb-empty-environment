package netsec

import (
	"errors"
	"sort"
	"strings"
)

// Purl is a parsed Package URL (https://github.com/package-url/purl-spec).
type Purl struct {
	Type       string
	Namespace  string
	Name       string
	Version    string
	Qualifiers map[string]string
	Subpath    string
}

var (
	errEmptyPurl        = errors.New("netsec: purl must not be empty")
	errMissingType      = errors.New("netsec: purl type is required")
	errMissingName      = errors.New("netsec: purl name is required")
	errMissingVersion   = errors.New("netsec: purl version is required")
	errBadPercentEscape = errors.New("netsec: invalid percent-encoding in purl")
)

// NormalizePurl returns the canonical string form of a purl.
//
// Normalization rules:
//   - type and namespace are lower-cased (name case is preserved, e.g. Go
//     import paths are case-sensitive);
//   - every component is percent-decoded and re-encoded so escaping is
//     uniform (unreserved characters stay literal, everything else uses
//     upper-case hex escapes);
//   - qualifier keys are lower-cased, duplicate keys collapse to their last
//     value, and qualifiers are emitted sorted by key;
//   - the version is mandatory and cannot be omitted.
func NormalizePurl(raw string) (string, error) {
	p, err := parsePurl(raw, true)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}

func parsePurl(raw string, requireVersion bool) (*Purl, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errEmptyPurl
	}

	s := raw
	if strings.HasPrefix(s, "pkg:") {
		s = s[len("pkg:"):]
	}

	var subpath string
	if i := strings.IndexByte(s, '#'); i >= 0 {
		subpath = s[i+1:]
		s = s[:i]
	}

	var qualifierString string
	if i := strings.IndexByte(s, '?'); i >= 0 {
		qualifierString = s[i+1:]
		s = s[:i]
	}

	slash := strings.IndexByte(s, '/')
	if slash < 0 {
		return nil, errMissingType
	}
	purlType, remainder := s[:slash], s[slash+1:]
	if purlType == "" {
		return nil, errMissingType
	}

	var version string
	if i := strings.IndexByte(remainder, '@'); i >= 0 {
		version = remainder[i+1:]
		remainder = remainder[:i]
	}

	var namespace, name string
	if i := strings.LastIndexByte(remainder, '/'); i >= 0 {
		namespace, name = remainder[:i], remainder[i+1:]
	} else {
		name = remainder
	}
	if name == "" {
		return nil, errMissingName
	}
	if requireVersion && version == "" {
		return nil, errMissingVersion
	}

	p := &Purl{Qualifiers: map[string]string{}}

	p.Type = strings.ToLower(normalizeToken(purlType))
	if namespace != "" {
		segments := strings.Split(namespace, "/")
		for i, seg := range segments {
			decoded, err := normalizeComponent(seg)
			if err != nil {
				return nil, err
			}
			segments[i] = strings.ToLower(decoded)
		}
		p.Namespace = strings.Join(segments, "/")
	}

	normalizedName, err := normalizeComponent(name)
	if err != nil {
		return nil, err
	}
	p.Name = normalizedName

	if version != "" {
		if p.Version, err = normalizeComponent(version); err != nil {
			return nil, err
		}
	}

	if qualifierString != "" {
		for _, pair := range strings.Split(qualifierString, "&") {
			if pair == "" {
				continue
			}
			key, value, found := strings.Cut(pair, "=")
			key = strings.ToLower(normalizeToken(key))
			if key == "" {
				return nil, errors.New("netsec: purl qualifier key is required")
			}
			if found {
				if value, err = normalizeComponent(value); err != nil {
					return nil, err
				}
			} else {
				value = ""
			}
			p.Qualifiers[key] = value
		}
	}

	if subpath != "" {
		segments := strings.Split(subpath, "/")
		kept := segments[:0]
		for _, seg := range segments {
			if seg == "." || seg == ".." || seg == "" {
				continue
			}
			normalized, err := normalizeComponent(seg)
			if err != nil {
				return nil, err
			}
			kept = append(kept, normalized)
		}
		p.Subpath = strings.Join(kept, "/")
	}

	return p, nil
}

// String renders the purl in canonical, comparable form.
func (p *Purl) String() string {
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(p.Type)
	if p.Namespace != "" {
		b.WriteByte('/')
		b.WriteString(p.Namespace)
	}
	b.WriteByte('/')
	b.WriteString(p.Name)
	if p.Version != "" {
		b.WriteByte('@')
		b.WriteString(p.Version)
	}
	if len(p.Qualifiers) > 0 {
		keys := make([]string, 0, len(p.Qualifiers))
		for key := range p.Qualifiers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteByte('?')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte('&')
			}
			b.WriteString(key)
			b.WriteByte('=')
			b.WriteString(p.Qualifiers[key])
		}
	}
	if p.Subpath != "" {
		b.WriteByte('#')
		b.WriteString(p.Subpath)
	}
	return b.String()
}

// coordinates returns the version-independent identity (type/namespace/name),
// used for package-level blacklist entries.
func (p *Purl) coordinates() string {
	var b strings.Builder
	b.WriteString("pkg:")
	b.WriteString(p.Type)
	if p.Namespace != "" {
		b.WriteByte('/')
		b.WriteString(p.Namespace)
	}
	b.WriteByte('/')
	b.WriteString(p.Name)
	return b.String()
}

func normalizeToken(s string) string {
	decoded, err := percentDecode(s)
	if err != nil {
		return s
	}
	return decoded
}

// normalizeComponent decodes existing escapes and re-encodes canonically,
// so "%2f" and "%2F" collapse to the same upper-case escape while a literal
// unreserved character stays unescaped.
func normalizeComponent(s string) (string, error) {
	decoded, err := percentDecode(s)
	if err != nil {
		return "", err
	}
	return percentEncode(decoded), nil
}

func percentDecode(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return "", errBadPercentEscape
		}
		b.WriteByte(fromHex(s[i+1])<<4 | fromHex(s[i+2]))
		i += 2
	}
	return b.String(), nil
}

func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(toHex(c >> 4))
			b.WriteByte(toHex(c & 0x0f))
		}
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '-' || c == '.' || c == '_' || c == '~'
	}
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func fromHex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func toHex(nibble byte) byte {
	if nibble < 10 {
		return '0' + nibble
	}
	return 'A' + nibble - 10
}
