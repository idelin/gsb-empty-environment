package netsec

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"hash"
	"io"
	"sort"
	"strings"
)

const xmlNamespace = "http://www.w3.org/XML/1998/namespace"

// Canonicalize serializes data as Canonical XML 1.0 without comments.
//
// XML digital signatures commonly cover a canonicalized form of the signed
// element. Ordinary Canonical XML omits comments, so adding, removing or
// changing a comment does not change the signature digest. Consumers must
// therefore not treat a valid signature as evidence that comment markup in a
// signed value is safe.
//
// Canonicalize performs the namespace, attribute-order, entity, line-ending,
// and escaping normalization needed by the common SAML signature profile. It
// intentionally omits comments and the XML declaration.
func Canonicalize(data []byte) ([]byte, error) {
	data = normalizeLineEndings(data)
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true

	var (
		out        strings.Builder
		stack      []canonFrame
		currentNS  = map[string]string{"xml": xmlNamespace}
		renderedNS = map[string]string{"xml": xmlNamespace}
		seenRoot   bool
	)

	for {
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("netsec: canonicalizing XML: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				seenRoot = true
			}

			ns := cloneNamespace(currentNS)
			var attrs []canonAttr
			for _, attr := range t.Attr {
				if prefix, ok := namespaceDeclaration(attr.Name); ok {
					ns[prefix] = attr.Value
					continue
				}
				uri, err := resolveAttributeNamespace(attr.Name, ns)
				if err != nil {
					return nil, err
				}
				attrs = append(attrs, canonAttr{
					qname: qname(attr.Name),
					local: attr.Name.Local,
					uri:   uri,
					value: attr.Value,
				})
			}

			rendered := cloneNamespace(renderedNS)
			var declarations []namespaceDecl
			// Canonical XML sorts namespace declarations by prefix, with the
			// default namespace first.
			prefixes := make([]string, 0, len(ns))
			for prefix := range ns {
				if prefix != "xml" {
					prefixes = append(prefixes, prefix)
				}
			}
			sort.Strings(prefixes)
			for _, prefix := range prefixes {
				uri := ns[prefix]
				if prefix == "" && uri == "" {
					if _, inherited := rendered[""]; !inherited {
						continue
					}
				}
				if old, ok := rendered[prefix]; ok && old == uri {
					continue
				}
				declarations = append(declarations, namespaceDecl{prefix: prefix, uri: uri})
				rendered[prefix] = uri
			}

			sort.Slice(attrs, func(i, j int) bool {
				if attrs[i].uri != attrs[j].uri {
					return attrs[i].uri < attrs[j].uri
				}
				return attrs[i].local < attrs[j].local
			})

			out.WriteByte('<')
			out.WriteString(qname(t.Name))
			for _, decl := range declarations {
				out.WriteByte(' ')
				if decl.prefix == "" {
					out.WriteString("xmlns")
				} else {
					out.WriteString("xmlns:")
					out.WriteString(decl.prefix)
				}
				out.WriteString(`="`)
				out.WriteString(escapeAttribute(decl.uri))
				out.WriteByte('"')
			}
			for _, attr := range attrs {
				out.WriteByte(' ')
				out.WriteString(attr.qname)
				out.WriteString(`="`)
				out.WriteString(escapeAttribute(attr.value))
				out.WriteByte('"')
			}
			out.WriteByte('>')

			stack = append(stack, canonFrame{
				name:     t.Name,
				ns:       ns,
				rendered: rendered,
			})
			currentNS = ns
			renderedNS = rendered

		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("netsec: canonicalizing XML: unexpected </%s>", t.Name.Local)
			}
			top := stack[len(stack)-1]
			if top.name.Space != t.Name.Space || top.name.Local != t.Name.Local {
				return nil, fmt.Errorf("netsec: canonicalizing XML: expected </%s>, got </%s>", qname(top.name), qname(t.Name))
			}
			out.WriteString("</")
			out.WriteString(qname(t.Name))
			out.WriteByte('>')
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				currentNS = map[string]string{"xml": xmlNamespace}
				renderedNS = map[string]string{"xml": xmlNamespace}
			} else {
				currentNS = stack[len(stack)-1].ns
				renderedNS = stack[len(stack)-1].rendered
			}

		case xml.CharData:
			// Whitespace outside the document element is not part of the
			// canonical document.
			if len(stack) == 0 {
				if strings.TrimSpace(string(t)) != "" {
					return nil, fmt.Errorf("netsec: canonicalizing XML: text outside document element")
				}
				continue
			}
			out.WriteString(escapeText(string(t)))

		case xml.Comment:
			// Deliberately omitted. Comment changes must not affect the
			// canonical bytes used for a signature digest.

		case xml.ProcInst:
			if strings.EqualFold(t.Target, "xml") {
				// The XML declaration is not part of canonical XML.
				continue
			}
			out.WriteString("<?")
			out.WriteString(t.Target)
			if len(t.Inst) != 0 {
				out.WriteByte(' ')
				out.WriteString(string(t.Inst))
			}
			out.WriteString("?>")

		case xml.Directive:
			// A document type declaration is not part of canonical XML.
		}
	}

	if !seenRoot {
		return nil, fmt.Errorf("netsec: canonicalizing XML: no document element")
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("netsec: canonicalizing XML: unexpected EOF")
	}
	return []byte(out.String()), nil
}

// CanonicalizeWithoutComments is an explicit alias for Canonicalize.
func CanonicalizeWithoutComments(data []byte) ([]byte, error) {
	return Canonicalize(data)
}

// CanonicalDigest hashes the comment-excluding canonical form of data using h.
func CanonicalDigest(data []byte, h hash.Hash) ([]byte, error) {
	canonical, err := Canonicalize(data)
	if err != nil {
		return nil, err
	}
	h.Reset()
	if _, err := h.Write(canonical); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// Digest returns the SHA-256 digest of the comment-excluding canonical form.
func Digest(data []byte) ([]byte, error) {
	return CanonicalDigest(data, sha256.New())
}

// SHA1Digest returns the SHA-1 digest of the comment-excluding canonical form.
// SHA-1 is provided for compatibility with older XML signatures; prefer
// SHA256Digest or Digest for new applications.
func SHA1Digest(data []byte) ([]byte, error) {
	return CanonicalDigest(data, sha1.New())
}

// SHA256Digest returns the SHA-256 digest of the comment-excluding canonical
// form.
func SHA256Digest(data []byte) ([]byte, error) {
	return Digest(data)
}

type canonFrame struct {
	name     xml.Name
	ns       map[string]string
	rendered map[string]string
}

type canonAttr struct {
	qname string
	local string
	uri   string
	value string
}

type namespaceDecl struct {
	prefix string
	uri    string
}

func namespaceDeclaration(name xml.Name) (string, bool) {
	if name.Space == "xmlns" {
		return name.Local, true
	}
	if name.Space == "" && name.Local == "xmlns" {
		return "", true
	}
	return "", false
}

func resolveAttributeNamespace(name xml.Name, ns map[string]string) (string, error) {
	if name.Space == "" {
		return "", nil
	}
	if name.Space == "xml" {
		return xmlNamespace, nil
	}
	uri, ok := ns[name.Space]
	if !ok {
		return "", fmt.Errorf("netsec: canonicalizing XML: undeclared namespace prefix %q", name.Space)
	}
	return uri, nil
}

func qname(name xml.Name) string {
	if name.Space == "" {
		return name.Local
	}
	return name.Space + ":" + name.Local
}

func cloneNamespace(ns map[string]string) map[string]string {
	clone := make(map[string]string, len(ns))
	for prefix, uri := range ns {
		clone[prefix] = uri
	}
	return clone
}

func escapeText(s string) string {
	var out strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '\r':
			out.WriteString("&#xD;")
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func escapeAttribute(s string) string {
	var out strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '"':
			out.WriteString("&quot;")
		case '\t':
			out.WriteString("&#x9;")
		case '\n':
			out.WriteString("&#xA;")
		case '\r':
			out.WriteString("&#xD;")
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
