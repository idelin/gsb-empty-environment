// Package mailfrag constructs HTML fragments for embedding untrusted rich
// text into outbound email templates without breaking the outer structure.
//
// Three rendering contexts are kept strictly separate:
//
//   - Text: plain character data, escaped for the HTML text context.
//   - Attr: attribute values, escaped for the quoted-attribute context.
//   - URL:  restricted URLs (http, https, mailto only) for href/src.
//
// There is intentionally no way to splice a raw string into markup: the
// only node constructors are Text (always escaped) and El (allow-listed
// tags with validated attributes). Cross-context concatenation is rejected
// by the type system — a string is never a Node and an Attr is never a
// child — and again by render-time validation.
//
// Rendering is atomic: Render builds the fragment in a private buffer and
// returns an error instead of partial output, and EnqueueRendered never
// touches the recipient queue until the whole fragment renders cleanly.
package mailfrag

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Node is a renderable HTML fragment node.
type Node interface {
	appendTo(buf *bytes.Buffer) error
}

var textEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
)

func checkCharData(s, what string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("mailfrag: %s contains invalid UTF-8", what)
	}
	for _, r := range s {
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7f {
			return fmt.Errorf("mailfrag: %s contains control character U+%04X", what, r)
		}
	}
	return nil
}

type textNode string

// Text returns a node that renders s escaped for the HTML text context.
// Markup-looking input stays inert: it is never re-parsed as tags or
// entities, so already-escaped text is escaped exactly once more.
func Text(s string) Node { return textNode(s) }

func (t textNode) appendTo(buf *bytes.Buffer) error {
	if err := checkCharData(string(t), "text"); err != nil {
		return err
	}
	buf.WriteString(textEscaper.Replace(string(t)))
	return nil
}

// Fragment is an ordered sequence of nodes rendered without a wrapper.
type Fragment []Node

// Frag groups nodes into a single node.
func Frag(nodes ...Node) Fragment { return Fragment(nodes) }

// Concat joins fragments into one. Only Node values can be concatenated;
// raw strings and attribute values are rejected at compile time.
func Concat(frags ...Fragment) Fragment {
	var out Fragment
	for _, f := range frags {
		out = append(out, f...)
	}
	return out
}

func (f Fragment) appendTo(buf *bytes.Buffer) error {
	for _, n := range f {
		if n == nil {
			return errors.New("mailfrag: nil node in fragment")
		}
		if err := n.appendTo(buf); err != nil {
			return err
		}
	}
	return nil
}

var allowedTags = map[string]bool{
	"a": true, "abbr": true, "b": true, "blockquote": true, "br": true,
	"cite": true, "code": true, "dd": true, "div": true, "dl": true,
	"dt": true, "em": true, "figcaption": true, "figure": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"hr": true, "i": true, "img": true, "li": true, "ol": true, "p": true,
	"pre": true, "q": true, "s": true, "small": true, "span": true,
	"strong": true, "sub": true, "sup": true, "table": true, "tbody": true,
	"td": true, "tfoot": true, "th": true, "thead": true, "tr": true,
	"u": true, "ul": true,
}

var voidTags = map[string]bool{
	"br": true, "hr": true, "img": true,
}

type element struct {
	tag      string
	attrs    []Attr
	children []Node
}

// El builds an element node. The tag must be on the email-safe allowlist
// and attrs must come from A, Href or Src; violations are reported by
// Render, never emitted as markup.
func El(tag string, attrs []Attr, children ...Node) Node {
	return element{tag: tag, attrs: attrs, children: children}
}

func (e element) appendTo(buf *bytes.Buffer) error {
	tag := strings.ToLower(strings.TrimSpace(e.tag))
	if !allowedTags[tag] {
		return fmt.Errorf("mailfrag: tag %q is not allowed", e.tag)
	}
	if voidTags[tag] && len(e.children) > 0 {
		return fmt.Errorf("mailfrag: void element <%s> cannot have children", tag)
	}
	buf.WriteByte('<')
	buf.WriteString(tag)
	seen := make(map[string]bool, len(e.attrs))
	hasSrc := false
	for _, a := range e.attrs {
		name, value, err := a.renderPair()
		if err != nil {
			return err
		}
		if seen[name] {
			return fmt.Errorf("mailfrag: duplicate attribute %q on <%s>", name, tag)
		}
		seen[name] = true
		if name == "src" {
			hasSrc = true
		}
		buf.WriteByte(' ')
		buf.WriteString(name)
		buf.WriteString(`="`)
		buf.WriteString(attrEscaper.Replace(value))
		buf.WriteByte('"')
	}
	if tag == "img" && !hasSrc {
		return errors.New("mailfrag: <img> requires a src attribute built with Src(url)")
	}
	buf.WriteByte('>')
	if voidTags[tag] {
		return nil
	}
	for _, c := range e.children {
		if c == nil {
			return fmt.Errorf("mailfrag: nil child in <%s>", tag)
		}
		if err := c.appendTo(buf); err != nil {
			return err
		}
	}
	buf.WriteString("</")
	buf.WriteString(tag)
	buf.WriteByte('>')
	return nil
}

// Render serializes n to HTML. The fragment is built in a private buffer;
// on error no partial output is returned.
func Render(n Node) (string, error) {
	if n == nil {
		return "", errors.New("mailfrag: cannot render a nil node")
	}
	var buf bytes.Buffer
	if err := n.appendTo(&buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
