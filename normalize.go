package netsec

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type caseMode uint8

const (
	casePreserve caseMode = iota
	caseLower
	casePEP503
)

type typeRule struct {
	namespace caseMode
	name      caseMode
}

// The canonical purl types that this package knows about.  Unknown types are
// still parsed, but are marked and are not subjected to type-specific case
// folding.
var typeRules = map[string]typeRule{
	"alpm":             {namespace: caseLower, name: caseLower},
	"apk":              {namespace: caseLower, name: caseLower},
	"bitbucket":        {namespace: caseLower, name: caseLower},
	"brew":             {namespace: caseLower, name: caseLower},
	"cargo":            {namespace: caseLower, name: caseLower},
	"chrome-extension": {namespace: caseLower, name: caseLower},
	"clojars":          {namespace: caseLower, name: caseLower},
	"cocoapods":        {namespace: caseLower, name: caseLower},
	"composer":         {namespace: caseLower, name: caseLower},
	"conan":            {namespace: caseLower, name: caseLower},
	"conda":            {namespace: caseLower, name: caseLower},
	"cpan":             {namespace: caseLower, name: caseLower},
	"cran":             {namespace: casePreserve, name: casePreserve},
	"deb":              {namespace: caseLower, name: caseLower},
	"docker":           {namespace: caseLower, name: caseLower},
	"gem":              {namespace: caseLower, name: caseLower},
	"generic":          {namespace: casePreserve, name: casePreserve},
	"github":           {namespace: caseLower, name: caseLower},
	"git":              {namespace: casePreserve, name: casePreserve},
	"golang":           {namespace: caseLower, name: caseLower},
	"hackage":          {namespace: caseLower, name: caseLower},
	"hex":              {namespace: caseLower, name: caseLower},
	"hg":               {namespace: casePreserve, name: casePreserve},
	"huggingface":      {namespace: casePreserve, name: casePreserve},
	"julia":            {namespace: casePreserve, name: casePreserve},
	"luarocks":         {namespace: caseLower, name: caseLower},
	"maven":            {namespace: casePreserve, name: casePreserve},
	"mlflow":           {namespace: caseLower, name: caseLower},
	"npm":              {namespace: caseLower, name: caseLower},
	"nuget":            {namespace: caseLower, name: caseLower},
	"oci":              {namespace: caseLower, name: caseLower},
	"opam":             {namespace: caseLower, name: caseLower},
	"otp":              {namespace: caseLower, name: caseLower},
	"pub":              {namespace: caseLower, name: caseLower},
	"pypi":             {namespace: casePEP503, name: casePEP503},
	"qpkg":             {namespace: caseLower, name: caseLower},
	"rpm":              {namespace: caseLower, name: caseLower},
	"svn":              {namespace: casePreserve, name: casePreserve},
	"swid":             {namespace: casePreserve, name: casePreserve},
	"swift":            {namespace: casePreserve, name: casePreserve},
	"vscode-extension": {namespace: caseLower, name: caseLower},
	"vcpkg":            {namespace: caseLower, name: caseLower},
	"yocto":            {namespace: caseLower, name: caseLower},
}

// IsKnownType reports whether typ is a registered purl type (after applying
// the built-in type aliases).
func IsKnownType(typ string) bool {
	canonical, _ := NormalizeType(typ)
	_, ok := typeRules[canonical]
	return ok
}

// NormalizeType returns the canonical type spelling and whether the type is
// known.  Type aliases such as "go", "pip" and "node" are resolved to
// "golang", "pypi" and "npm".
func NormalizeType(typ string) (string, bool) {
	original := strings.TrimSpace(typ)
	canonical := asciiLower(original)
	aliased := false
	if target, ok := resolveTypeAlias(canonical); ok {
		canonical = target
		aliased = true
	}
	_, known := typeRules[canonical]
	if !known && !aliased {
		return original, false
	}
	return canonical, known
}

func applyCase(s string, mode caseMode) string {
	switch mode {
	case caseLower:
		return asciiLower(s)
	case casePEP503:
		return pep503Normalize(s)
	default:
		return s
	}
}

var pep503Separators = regexp.MustCompile(`[-_.]+`)

func pep503Normalize(s string) string {
	return pep503Separators.ReplaceAllString(asciiLower(s), "-")
}

func asciiLower(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// goModuleUnescape reverses the "!x" case escaping used by the Go module
// proxy.  Vulnerability feeds and SBOMs occasionally contain that spelling.
func goModuleUnescape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '!' && i+1 < len(s) {
			c := s[i+1]
			if c >= 'a' && c <= 'z' {
				b.WriteByte(c - ('a' - 'A'))
				i++
				continue
			}
			if c == '!' {
				b.WriteByte('!')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func decodeComponent(s string) (string, error) {
	decoded, err := url.PathUnescape(s)
	if err != nil {
		return "", fmt.Errorf("invalid percent-encoding %q: %w", s, err)
	}
	for _, r := range decoded {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("control character in %q", s)
		}
	}
	return decoded, nil
}

func encodeComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	return c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~' || c == ':'
}

func validType(s string) bool {
	if s == "" {
		return false
	}
	first := s[0]
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z') {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '+' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func whitespaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f'
}

func validQualifierKey(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}
