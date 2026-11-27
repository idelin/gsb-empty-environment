package streamguard

import (
	"unicode"
	"unicode/utf8"
)

// Common XMPP namespaces.
const (
	NamespaceXML    = "http://www.w3.org/XML/1998/namespace"
	NamespaceStream = "http://etherx.jabber.org/streams"
	NamespaceClient = "jabber:client"
	NamespaceTLS    = "urn:ietf:params:xml:ns:xmpp-tls"
	NamespaceSASL   = "urn:ietf:params:xml:ns:xmpp-sasl"
	NamespaceBind   = "urn:ietf:params:xml:ns:xmpp-bind"
)

// Name is a parsed XML qualified name. Space contains the resolved namespace
// URI when namespace declarations are available.
type Name struct {
	Raw    string
	Prefix string
	Local  string
	Space  string
}

// URI returns the resolved namespace URI.
func (n Name) URI() string { return n.Space }

func isNameStart(r rune) bool {
	return r == ':' || r == '_' || unicode.IsLetter(r)
}

func isNameChar(r rune) bool {
	return isNameStart(r) ||
		r == '-' || r == '.' || r == 0x00B7 ||
		unicode.IsDigit(r) || unicode.IsMark(r)
}

func isASCIIWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func skipASCIIWhitespace(b []byte) int {
	n := 0
	for n < len(b) && isASCIIWhitespace(b[n]) {
		n++
	}
	return n
}

// readName reads one XML Name. It returns errIncompleteName when a multibyte
// rune at the end has not fully arrived.
func readName(b []byte) (Name, int, error) {
	if len(b) == 0 {
		return Name{}, 0, ErrNeedMore
	}
	r, size := utf8.DecodeRune(b)
	if r == utf8.RuneError && size == 1 && !utf8.FullRune(b) {
		return Name{}, 0, ErrNeedMore
	}
	if r == utf8.RuneError && size == 1 {
		return Name{}, 0, ErrMalformedXML
	}
	if !isNameStart(r) {
		return Name{}, 0, ErrMalformedXML
	}

	n := size
	for n < len(b) {
		r, size = utf8.DecodeRune(b[n:])
		if r == utf8.RuneError && size == 1 && !utf8.FullRune(b[n:]) {
			break
		}
		if r == utf8.RuneError && size == 1 {
			return Name{}, 0, ErrMalformedXML
		}
		if !isNameChar(r) {
			break
		}
		n += size
	}

	raw := string(b[:n])
	name := Name{Raw: raw}
	for i := 0; i < len(raw); i++ {
		if raw[i] == ':' {
			if name.Prefix != "" {
				return Name{}, 0, ErrMalformedXML
			}
			name.Prefix = raw[:i]
			name.Local = raw[i+1:]
		}
	}
	if name.Local == "" {
		name.Local = raw
	}
	if name.Prefix != "" && name.Local == "" {
		return Name{}, 0, ErrMalformedXML
	}
	return name, n, nil
}

func validXMLRune(r rune) bool {
	switch r {
	case '\t', '\n', '\r':
		return true
	}
	return r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r <= 0xFFFD ||
		r >= 0x10000 && r <= 0x10FFFF
}

func validateXMLChars(b []byte) error {
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			return ErrInvalidUTF8
		}
		if !validXMLRune(r) {
			return ErrInvalidXMLChar
		}
		b = b[size:]
	}
	return nil
}
