package saml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

// Well-known namespace URIs. Comparison always happens on the resolved URI,
// never on the prefix, so xmlns rebinding cannot move an element between
// vocabularies.
const (
	NSAssertion = "urn:oasis:names:tc:SAML:2.0:assertion"
	NSProtocol  = "urn:oasis:names:tc:SAML:2.0:protocol"
	NSSignature = "http://www.w3.org/2000/09/xmldsig#"
	NSXML       = "http://www.w3.org/XML/1998/namespace"
)

// Element is a fully materialized XML element. The tree is produced by the
// real encoding/xml decoder, which resolves every name and attribute to its
// namespace URI before we ever inspect it.
type Element struct {
	Name     xml.Name
	Attr     []xml.Attr
	Text     string
	Children []*Element
	Parent   *Element
}

// Document is the parsed SAML message together with the unique ID index
// covering every element in the document, not just the signed subtree.
type Document struct {
	Root *Element

	// ids maps every ID-bearing element to the single element that owns it.
	// An insertion collision means an attacker wrapped the document with a
	// second element reusing an existing ID and parsing is rejected outright.
	ids map[string]*Element
}

// vocabulary records, per reserved local name, the only namespace URI that
// is allowed to own it. An element carrying one of these local names from a
// different URI is a namespace-substitution attempt.
var vocabulary = func() map[string]string {
	v := make(map[string]string)
	for _, term := range []string{
		"Assertion", "Subject", "NameID", "Issuer", "Conditions",
		"AudienceRestriction", "Audience", "AuthnStatement",
		"AttributeStatement", "Attribute", "AttributeValue", "SubjectConfirmation",
	} {
		v[term] = NSAssertion
	}
	for _, term := range []string{
		"Response", "Status", "StatusCode", "StatusMessage",
	} {
		v[term] = NSProtocol
	}
	for _, term := range []string{
		"Signature", "SignedInfo", "CanonicalizationMethod", "SignatureMethod",
		"Reference", "Transforms", "Transform", "DigestMethod", "DigestValue",
		"SignatureValue", "KeyInfo", "X509Data", "X509Certificate",
	} {
		v[term] = NSSignature
	}
	return v
}()

// Parse decodes the whole document with encoding/xml, builds the parent-linked
// element tree and the unique document-wide ID table.
func Parse(r io.Reader) (*Document, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = true
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	doc := &Document{ids: make(map[string]*Element)}
	var stack []*Element

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
		}

		switch t := tok.(type) {
		case xml.Directive:
			// A DOCTYPE opens the door to entity expansion and external
			// entities; SAML messages never need one.
			if isDOCTYPE(t) {
				return nil, fmt.Errorf("%w: DOCTYPE is not allowed", ErrMalformed)
			}
		case xml.StartElement:
			if err := checkVocabulary(t.Name); err != nil {
				return nil, err
			}
			el := &Element{
				Name: t.Name,
				Attr: append([]xml.Attr(nil), t.Copy().Attr...),
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				el.Parent = parent
				parent.Children = append(parent.Children, el)
			} else if doc.Root != nil {
				return nil, fmt.Errorf("%w: multiple root elements", ErrMalformed)
			} else {
				doc.Root = el
			}
			stack = append(stack, el)

			if id, ok := elementID(el); ok {
				if _, exists := doc.ids[id]; exists {
					return nil, fmt.Errorf("%w: %q", ErrDuplicateID, id)
				}
				doc.ids[id] = el
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].Text += string(t)
			}
		case xml.EndElement:
			if len(stack) == 0 {
				return nil, fmt.Errorf("%w: unexpected closing tag", ErrMalformed)
			}
			stack = stack[:len(stack)-1]
		}
	}

	if doc.Root == nil {
		return nil, fmt.Errorf("%w: empty document", ErrMalformed)
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("%w: unclosed element", ErrMalformed)
	}
	return doc, nil
}

func isDOCTYPE(d xml.Directive) bool {
	// Directives retain the raw text, e.g. `DOCTYPE foo [...]`.
	return len(d) >= 7 && equalFoldASCII(string(d[:7]), "doctype")
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'a' && x <= 'z' {
			x -= 'a' - 'A'
		}
		if y >= 'a' && y <= 'z' {
			y -= 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func checkVocabulary(name xml.Name) error {
	want, reserved := vocabulary[name.Local]
	if !reserved {
		return nil
	}
	if name.Space != want {
		return fmt.Errorf("%w: <%s> bound to %q, expected %q",
			ErrNamespaceSubstitution, name.Local, name.Space, want)
	}
	return nil
}

// elementID returns the XML ID for an element. Both the SAML convention of an
// unqualified "ID" attribute and the reserved xml:id attribute are honored.
func elementID(el *Element) (string, bool) {
	for _, a := range el.Attr {
		if (a.Name.Space == "" && a.Name.Local == "ID") ||
			(a.Name.Space == NSXML && a.Name.Local == "id") {
			if a.Value != "" {
				return a.Value, true
			}
		}
	}
	return "", false
}

// LookupID resolves an ID through the document index. It never walks the tree,
// so a duplicate- or wrapper-bearing document would already have been rejected
// at parse time.
func (d *Document) LookupID(id string) (*Element, bool) {
	el, ok := d.ids[id]
	return el, ok
}

// Signatures returns every XML-DSig Signature element in the document.
func (d *Document) Signatures() []*Element {
	var out []*Element
	walk(d.Root, func(el *Element) bool {
		if el.Name.Local == "Signature" && el.Name.Space == NSSignature {
			out = append(out, el)
		}
		return true
	})
	return out
}

// walk performs a depth-first traversal. Returning false from visit prunes the
// subtree of the visited element.
func walk(el *Element, visit func(*Element) bool) {
	if el == nil {
		return
	}
	if !visit(el) {
		return
	}
	for _, c := range el.Children {
		walk(c, visit)
	}
}
