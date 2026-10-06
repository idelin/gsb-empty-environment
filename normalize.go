package migrate

import "strings"

// CaseRule selects how name casing is normalized.
type CaseRule int

const (
	CasePreserve CaseRule = iota
	CaseLower
	CaseUpper
)

// Normalizer rewrites a single path component (a base name, no slashes).
// The character map is applied first, then the case rule.
type Normalizer struct {
	CharMap map[rune]string
	Case    CaseRule
}

// Normalize returns the normalized form of a single base name.
func (n Normalizer) Normalize(name string) string {
	var b strings.Builder
	for _, r := range name {
		if rep, ok := n.CharMap[r]; ok {
			b.WriteString(rep)
		} else {
			b.WriteRune(r)
		}
	}
	s := b.String()
	switch n.Case {
	case CaseLower:
		s = strings.ToLower(s)
	case CaseUpper:
		s = strings.ToUpper(s)
	}
	return s
}
