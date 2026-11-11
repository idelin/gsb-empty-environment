package mailfrag

import (
	"fmt"
	"regexp"
	"strings"
)

var attrEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&#39;",
)

// Attr is a name/value pair for an element attribute. Build plain
// attributes with A and URL-bearing attributes with Href or Src.
type Attr struct {
	name  string
	value string
	u     URL
	isURL bool
}

var allowedAttrs = map[string]bool{
	"align": true, "alt": true, "bgcolor": true, "class": true,
	"colspan": true, "dir": true, "height": true, "id": true,
	"lang": true, "rel": true, "rowspan": true, "style": true,
	"target": true, "title": true, "valign": true, "width": true,
}

var attrNameRE = regexp.MustCompile(`^[a-z][a-z0-9_.:-]*$`)

// A builds a plain attribute from an allow-listed name and a value that is
// escaped for the quoted-attribute context at render time. URL-bearing
// attributes (href, src) are refused here; use Href or Src with a URL.
func A(name, value string) Attr {
	return Attr{name: name, value: value}
}

// Href builds an href attribute from a restricted URL.
func Href(u URL) Attr { return Attr{name: "href", u: u, isURL: true} }

// Src builds a src attribute from a restricted URL.
func Src(u URL) Attr { return Attr{name: "src", u: u, isURL: true} }

func (a Attr) renderPair() (name, value string, err error) {
	if a.isURL {
		if !a.u.ok {
			return "", "", fmt.Errorf("mailfrag: attribute %q has no valid URL (zero-value URL?)", a.name)
		}
		return a.name, a.u.raw, nil
	}
	name = strings.ToLower(strings.TrimSpace(a.name))
	if name == "href" || name == "src" {
		return "", "", fmt.Errorf("mailfrag: attribute %q must be built from a URL via Href/Src", name)
	}
	if !attrNameRE.MatchString(name) {
		return "", "", fmt.Errorf("mailfrag: invalid attribute name %q", a.name)
	}
	if strings.HasPrefix(name, "on") {
		return "", "", fmt.Errorf("mailfrag: event handler attribute %q is not allowed", name)
	}
	if !allowedAttrs[name] {
		return "", "", fmt.Errorf("mailfrag: attribute %q is not in the allowlist", name)
	}
	if err := checkCharData(a.value, "attribute "+name); err != nil {
		return "", "", err
	}
	return name, a.value, nil
}
