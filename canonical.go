package netsec

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const xmlNamespace = "http://www.w3.org/XML/1998/namespace"

var ErrMalformedXML = errors.New("netsec: malformed XML")

type namespaceFrame struct {
	name  xml.Name
	scope map[string]string
}

type canonicalNamespace struct {
	prefix string
	uri    string
}

type canonicalAttribute struct {
	prefix string
	uri    string
	local  string
	value  string
}

// CanonicalizeExcludingComments returns the Canonical XML 1.0 form of data
// with all comments omitted. It accepts one complete XML document.
func CanonicalizeExcludingComments(data []byte) ([]byte, error) {
	return canonicalizeXML(data)
}

// Canonicalize is an alias for CanonicalizeExcludingComments.
func Canonicalize(data []byte) ([]byte, error) {
	return CanonicalizeExcludingComments(data)
}

// CanonicalizeXML is an alias for CanonicalizeExcludingComments.
func CanonicalizeXML(data []byte) ([]byte, error) {
	return CanonicalizeExcludingComments(data)
}

// DigestExcludingComments canonicalizes data without comments and returns the
// raw digest produced by the supplied hash. With no hash argument, SHA-256 is
// used. A hash may be supplied as hash.Hash, func() hash.Hash, or crypto.Hash.
func DigestExcludingComments(data []byte, hashers ...any) ([]byte, error) {
	h, err := resolveHash(hashers)
	if err != nil {
		return nil, err
	}
	canonical, err := CanonicalizeExcludingComments(data)
	if err != nil {
		return nil, err
	}
	h.Reset()
	if _, err := h.Write(canonical); err != nil {
		return nil, fmt.Errorf("netsec: hash canonical XML: %w", err)
	}
	return h.Sum(nil), nil
}

// Digest is an alias for DigestExcludingComments.
func Digest(data []byte, hashers ...any) ([]byte, error) {
	return DigestExcludingComments(data, hashers...)
}

// CanonicalDigest is an alias for DigestExcludingComments.
func CanonicalDigest(data []byte, hashers ...any) ([]byte, error) {
	return DigestExcludingComments(data, hashers...)
}

func resolveHash(hashers []any) (hash.Hash, error) {
	if len(hashers) > 1 {
		return nil, errors.New("netsec: at most one hash may be supplied")
	}
	if len(hashers) == 0 || hashers[0] == nil {
		return sha256.New(), nil
	}

	switch h := hashers[0].(type) {
	case hash.Hash:
		if h == nil {
			return nil, errors.New("netsec: nil hash")
		}
		return h, nil
	case func() hash.Hash:
		if h == nil {
			return nil, errors.New("netsec: nil hash constructor")
		}
		resolved := h()
		if resolved == nil {
			return nil, errors.New("netsec: nil hash")
		}
		return resolved, nil
	case crypto.Hash:
		if !h.Available() {
			return nil, fmt.Errorf("netsec: hash %v is unavailable", h)
		}
		return h.New(), nil
	default:
		return nil, fmt.Errorf("netsec: unsupported hash type %T", hashers[0])
	}
}

func canonicalizeXML(data []byte) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var output bytes.Buffer
	var stack []namespaceFrame
	seenRoot := false
	var lastOffset int64

	for {
		token, err := decoder.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformedXML, err)
		}
		offset := decoder.InputOffset()
		raw := data[lastOffset:offset]
		lastOffset = offset

		switch token := token.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				if seenRoot {
					return nil, fmt.Errorf("%w: multiple root elements", ErrMalformedXML)
				}
				seenRoot = true
			}
			parent := map[string]string{"xml": xmlNamespace}
			if len(stack) > 0 {
				parent = stack[len(stack)-1].scope
			}
			rawValues, err := rawAttributeValues(raw)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrMalformedXML, err)
			}
			frame, err := writeCanonicalStart(&output, token, rawValues, parent)
			if err != nil {
				return nil, err
			}
			stack = append(stack, frame)
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("%w: unmatched end element", ErrMalformedXML)
			}
			frame := stack[len(stack)-1]
			if frame.name != token.Name {
				return nil, fmt.Errorf("%w: element mismatch", ErrMalformedXML)
			}
			output.WriteString("</")
			output.WriteString(qualifiedName(token.Name))
			output.WriteByte('>')
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) == 0 {
				if !isXMLWhitespace(token) {
					return nil, fmt.Errorf("%w: text outside the root element", ErrMalformedXML)
				}
				continue
			}
			writeEscapedText(&output, token)
		case xml.Comment:
			continue
		case xml.ProcInst:
			if strings.EqualFold(token.Target, "xml") {
				if len(stack) != 0 {
					return nil, fmt.Errorf("%w: XML declaration inside an element", ErrMalformedXML)
				}
				continue
			}
			output.WriteString("<?")
			output.WriteString(token.Target)
			if len(token.Inst) > 0 {
				output.WriteByte(' ')
				output.Write(token.Inst)
			}
			output.WriteString("?>")
		case xml.Directive:
			return nil, fmt.Errorf("%w: DTD declarations are not accepted", ErrMalformedXML)
		}
	}

	if len(stack) != 0 {
		return nil, fmt.Errorf("%w: unclosed element", ErrMalformedXML)
	}
	if !seenRoot {
		return nil, fmt.Errorf("%w: no root element", ErrMalformedXML)
	}
	return output.Bytes(), nil
}

func writeCanonicalStart(output *bytes.Buffer, start xml.StartElement, rawValues []string, parent map[string]string) (namespaceFrame, error) {
	if len(rawValues) != len(start.Attr) {
		return namespaceFrame{}, fmt.Errorf("%w: cannot map raw attributes", ErrMalformedXML)
	}
	values := make([]string, len(rawValues))
	for index, raw := range rawValues {
		value, err := normalizeAttributeValue(raw)
		if err != nil {
			return namespaceFrame{}, fmt.Errorf("%w: %v", ErrMalformedXML, err)
		}
		values[index] = value
	}

	scope := make(map[string]string, len(parent)+len(start.Attr))
	for prefix, uri := range parent {
		scope[prefix] = uri
	}
	scope["xml"] = xmlNamespace

	declared := make(map[string]bool)
	var namespaces []canonicalNamespace
	var attributes []canonicalAttribute

	for index, attribute := range start.Attr {
		prefix, ok := namespaceDeclarationPrefix(attribute.Name)
		if !ok {
			continue
		}
		if declared[prefix] {
			return namespaceFrame{}, fmt.Errorf("%w: duplicate namespace declaration %q", ErrMalformedXML, prefix)
		}
		declared[prefix] = true
		if prefix == "xmlns" {
			return namespaceFrame{}, fmt.Errorf("%w: invalid namespace prefix %q", ErrMalformedXML, prefix)
		}
		if prefix == "xml" && values[index] != xmlNamespace {
			return namespaceFrame{}, fmt.Errorf("%w: invalid xml namespace binding", ErrMalformedXML)
		}
		scope[prefix] = values[index]
		namespaces = append(namespaces, canonicalNamespace{prefix: prefix, uri: values[index]})
	}

	for index, attribute := range start.Attr {
		if _, ok := namespaceDeclarationPrefix(attribute.Name); ok {
			continue
		}
		prefix := attribute.Name.Space
		uri := ""
		switch prefix {
		case "":
		case "xml":
			uri = xmlNamespace
		default:
			var found bool
			uri, found = scope[prefix]
			if !found || uri == "" {
				return namespaceFrame{}, fmt.Errorf("%w: unbound attribute prefix %q", ErrMalformedXML, prefix)
			}
		}
		attributes = append(attributes, canonicalAttribute{
			prefix: prefix,
			uri:    uri,
			local:  attribute.Name.Local,
			value:  values[index],
		})
	}

	prefix := start.Name.Space
	if prefix == "xmlns" {
		return namespaceFrame{}, fmt.Errorf("%w: invalid element prefix %q", ErrMalformedXML, prefix)
	}
	if prefix != "" && prefix != "xml" && scope[prefix] == "" {
		return namespaceFrame{}, fmt.Errorf("%w: unbound element prefix %q", ErrMalformedXML, prefix)
	}

	seenAttributes := make(map[string]bool, len(attributes))
	for _, attribute := range attributes {
		key := attribute.uri + "\x00" + attribute.local
		if seenAttributes[key] {
			return namespaceFrame{}, fmt.Errorf("%w: duplicate attribute %q", ErrMalformedXML, attribute.local)
		}
		seenAttributes[key] = true
	}

	sort.Slice(namespaces, func(i, j int) bool {
		return namespaces[i].prefix < namespaces[j].prefix
	})
	sort.Slice(attributes, func(i, j int) bool {
		if attributes[i].uri != attributes[j].uri {
			return attributes[i].uri < attributes[j].uri
		}
		return attributes[i].local < attributes[j].local
	})

	output.WriteByte('<')
	output.WriteString(qualifiedName(start.Name))

	for _, namespace := range namespaces {
		if namespace.uri == "" && parent[namespace.prefix] == "" {
			continue
		}
		if parent[namespace.prefix] == namespace.uri && namespace.uri != "" {
			continue
		}
		output.WriteString(" xmlns")
		if namespace.prefix != "" {
			output.WriteByte(':')
			output.WriteString(namespace.prefix)
		}
		output.WriteString(`="`)
		writeEscapedAttribute(output, []byte(namespace.uri))
		output.WriteByte('"')
	}

	for _, attribute := range attributes {
		output.WriteByte(' ')
		if attribute.prefix != "" {
			output.WriteString(attribute.prefix)
			output.WriteByte(':')
		}
		output.WriteString(attribute.local)
		output.WriteString(`="`)
		writeEscapedAttribute(output, []byte(attribute.value))
		output.WriteByte('"')
	}
	output.WriteByte('>')

	return namespaceFrame{name: start.Name, scope: scope}, nil
}

func rawAttributeValues(startTag []byte) ([]string, error) {
	index := 0
	if len(startTag) < 2 || startTag[index] != '<' {
		return nil, errors.New("invalid start tag")
	}
	index++
	for index < len(startTag) && !isXMLSpace(startTag[index]) && startTag[index] != '/' && startTag[index] != '>' {
		index++
	}

	var values []string
	for {
		for index < len(startTag) && isXMLSpace(startTag[index]) {
			index++
		}
		if index >= len(startTag) {
			return nil, errors.New("unterminated start tag")
		}
		if startTag[index] == '>' {
			return values, nil
		}
		if startTag[index] == '/' {
			index++
			if index >= len(startTag) || startTag[index] != '>' {
				return nil, errors.New("invalid empty-element tag")
			}
			return values, nil
		}

		start := index
		for index < len(startTag) && !isXMLSpace(startTag[index]) && startTag[index] != '=' && startTag[index] != '/' && startTag[index] != '>' {
			index++
		}
		if index == start {
			return nil, errors.New("invalid attribute name")
		}
		for index < len(startTag) && isXMLSpace(startTag[index]) {
			index++
		}
		if index >= len(startTag) || startTag[index] != '=' {
			return nil, errors.New("missing attribute value")
		}
		index++
		for index < len(startTag) && isXMLSpace(startTag[index]) {
			index++
		}
		if index >= len(startTag) || (startTag[index] != '\'' && startTag[index] != '"') {
			return nil, errors.New("unquoted attribute value")
		}
		quote := startTag[index]
		index++
		valueStart := index
		for index < len(startTag) && startTag[index] != quote {
			index++
		}
		if index >= len(startTag) {
			return nil, errors.New("unterminated attribute value")
		}
		values = append(values, string(startTag[valueStart:index]))
		index++
	}
}

func normalizeAttributeValue(raw string) (string, error) {
	var value strings.Builder
	for index := 0; index < len(raw); {
		character := raw[index]
		switch character {
		case '\r':
			value.WriteByte(' ')
			index++
			if index < len(raw) && raw[index] == '\n' {
				index++
			}
		case '\n', '\t':
			value.WriteByte(' ')
			index++
		case '&':
			end := strings.IndexByte(raw[index:], ';')
			if end < 0 {
				return "", errors.New("unterminated entity reference")
			}
			end += index
			reference := raw[index+1 : end]
			decoded, err := decodeEntityReference(reference)
			if err != nil {
				return "", err
			}
			value.WriteString(decoded)
			index = end + 1
		default:
			value.WriteByte(character)
			index++
		}
	}
	return value.String(), nil
}

func decodeEntityReference(reference string) (string, error) {
	switch reference {
	case "amp":
		return "&", nil
	case "lt":
		return "<", nil
	case "gt":
		return ">", nil
	case "apos":
		return "'", nil
	case "quot":
		return `"`, nil
	}
	if !strings.HasPrefix(reference, "#") {
		return "", fmt.Errorf("unknown entity reference %q", reference)
	}

	base := 10
	digits := reference[1:]
	if strings.HasPrefix(digits, "x") || strings.HasPrefix(digits, "X") {
		base = 16
		digits = digits[1:]
	}
	if digits == "" {
		return "", fmt.Errorf("invalid character reference %q", reference)
	}
	codePoint, err := strconv.ParseUint(digits, base, 32)
	if err != nil {
		return "", fmt.Errorf("invalid character reference %q", reference)
	}
	character := rune(codePoint)
	if !utf8.ValidRune(character) || character == 0 {
		return "", fmt.Errorf("invalid character reference %q", reference)
	}
	return string(character), nil
}

func isXMLSpace(character byte) bool {
	switch character {
	case ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func namespaceDeclarationPrefix(name xml.Name) (string, bool) {
	if name.Space == "" && name.Local == "xmlns" {
		return "", true
	}
	if name.Space == "xmlns" {
		return name.Local, true
	}
	return "", false
}

func qualifiedName(name xml.Name) string {
	if name.Space == "" {
		return name.Local
	}
	return name.Space + ":" + name.Local
}

func writeEscapedText(output *bytes.Buffer, text []byte) {
	for _, character := range text {
		switch character {
		case '&':
			output.WriteString("&amp;")
		case '<':
			output.WriteString("&lt;")
		case '>':
			output.WriteString("&gt;")
		case '\r':
			output.WriteString("&#xD;")
		default:
			output.WriteByte(character)
		}
	}
}

func writeEscapedAttribute(output *bytes.Buffer, value []byte) {
	for _, character := range value {
		switch character {
		case '&':
			output.WriteString("&amp;")
		case '<':
			output.WriteString("&lt;")
		case '"':
			output.WriteString("&quot;")
		case '\t':
			output.WriteString("&#x9;")
		case '\n':
			output.WriteString("&#xA;")
		case '\r':
			output.WriteString("&#xD;")
		default:
			output.WriteByte(character)
		}
	}
}

func isXMLWhitespace(data []byte) bool {
	for _, character := range data {
		switch character {
		case ' ', '\t', '\r', '\n':
		default:
			return false
		}
	}
	return true
}
