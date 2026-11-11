package ctxescape

import "strings"

// urlAttributes is the set of attribute names whose value is parsed as a URL.
var urlAttributes = map[string]bool{
	"action":     true,
	"archive":    true,
	"background": true,
	"cite":       true,
	"classid":    true,
	"codebase":   true,
	"data":       true,
	"formaction": true,
	"href":       true,
	"icon":       true,
	"longdesc":   true,
	"manifest":   true,
	"poster":     true,
	"profile":    true,
	"src":        true,
	"srcset":     true,
	"usemap":     true,
	"xmlns":      true,
}

// commitAttrName classifies the attribute accumulated in nameBuf, using the
// same conservative heuristics as html/template: "on*" names are scripts,
// "style" is CSS, URL-named attributes are URLs, everything else is plain
// text.
func (w *Writer) commitAttrName() {
	name := strings.ToLower(string(w.nameBuf))
	w.attr = classifyAttr(name)
}

func classifyAttr(name string) attributeKind {
	if name == "style" {
		return attrStyle
	}
	if strings.HasPrefix(name, "on") {
		return attrJS
	}
	if urlAttributes[name] {
		return attrURL
	}
	// Custom data attributes and namespaced attributes frequently carry URLs;
	// apply the stdlib's name-based heuristic.
	trimmed := name
	if strings.HasPrefix(trimmed, "data-") {
		trimmed = trimmed[5:]
	}
	if strings.Contains(trimmed, "src") ||
		strings.Contains(trimmed, "uri") ||
		strings.Contains(trimmed, "url") {
		return attrURL
	}
	return attrPlain
}

func isASCIIAlpha(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isHTMLSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}

func isTagNameByte(c byte) bool {
	return isASCIIAlpha(c) || c >= 0x80 ||
		(c >= '0' && c <= '9') || c == '-' || c == ':'
}

func hasCaseInsensitivePrefix(s []byte, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		a, b := s[i], prefix[i]
		if a >= 'A' && a <= 'Z' {
			a += 'a' - 'A'
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if a != b {
			return false
		}
	}
	return true
}
