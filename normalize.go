// Package migrate plans and applies safe, collision-free file-tree
// renames driven by user-supplied normalization rules.
package migrate

import "strings"

// CaseRule controls how letter case is normalized.
type CaseRule int

const (
	// CasePreserve leaves letter case untouched.
	CasePreserve CaseRule = iota
	// CaseLower folds all letters to lower case.
	CaseLower
	// CaseUpper folds all letters to upper case.
	CaseUpper
)

// Rules describes how a single path segment (file or directory name)
// is normalized: first every character is rewritten through Chars,
// then the case rule is applied to the result.
type Rules struct {
	// Chars maps a source rune to its replacement. Runes absent from
	// the map are kept as-is.
	Chars map[rune]rune
	// Case is applied after the character mapping.
	Case CaseRule
}

// Normalize returns the normalized form of a single name.
func (r Rules) Normalize(name string) string {
	runes := []rune(name)
	for i, ch := range runes {
		if mapped, ok := r.Chars[ch]; ok {
			runes[i] = mapped
		}
	}
	out := string(runes)
	switch r.Case {
	case CaseLower:
		out = strings.ToLower(out)
	case CaseUpper:
		out = strings.ToUpper(out)
	}
	return out
}
