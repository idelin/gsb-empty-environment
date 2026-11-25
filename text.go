package displaycommit

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TextPolicy controls restricted-text rendering. The zero value uses safe
// defaults: format/invisible runes are escaped and known confusables are
// annotated.
type TextPolicy struct {
	MaxRunes            int      `json:"max_runes,omitempty"`
	RejectInvalidUTF8   bool     `json:"reject_invalid_utf8,omitempty"`
	RejectInvisible     bool     `json:"reject_invisible,omitempty"`
	RejectConfusables   bool     `json:"reject_confusables,omitempty"`
	EscapeInvisible     bool     `json:"escape_invisible,omitempty"`
	AnnotateConfusables bool     `json:"annotate_confusables,omitempty"`
	AllowedScripts      []string `json:"allowed_scripts,omitempty"`
}

func DefaultTextPolicy() TextPolicy {
	return TextPolicy{
		MaxRunes:            256,
		RejectInvalidUTF8:   true,
		EscapeInvisible:     true,
		AnnotateConfusables: true,
	}
}

type TextReport struct {
	Text            string
	Display         string
	Runes           int
	HasInvisible    bool
	HasConfusable   bool
	InvalidUTF8     bool
	InvisibleRunes  []rune
	ConfusableRunes []rune
}

// IsInvisibleRune reports common zero-width, format, bidi and variation
// selector code points.
func IsInvisibleRune(r rune) bool {
	if r == utf8.RuneError {
		return false
	}
	if unicode.Is(unicode.Cf, r) {
		return true
	}
	switch {
	case r >= 0xFE00 && r <= 0xFE0F:
		return true
	case r >= 0xE0100 && r <= 0xE01EF:
		return true
	}
	switch r {
	case 0x00AD, 0x115F, 0x1160, 0x3164, 0xFFA0:
		return true
	default:
		return false
	}
}

// commonConfusables covers the small cross-script pairs commonly used in
// address and label spoofing. It is deliberately used for annotation, not as
// the sole safety boundary: verification also compares exact commitments.
var commonConfusables = map[rune]rune{
	0x0430: 'a', 0x0410: 'A', // Cyrillic а/A
	0x0435: 'e', 0x0415: 'E',
	0x043E: 'o', 0x041E: 'O',
	0x0440: 'p', 0x0420: 'P',
	0x0441: 'c', 0x0421: 'C',
	0x0445: 'x', 0x0425: 'X',
	0x0443: 'y', 0x0423: 'Y',
	0x0456: 'i', 0x0406: 'I',
	0x0458: 'j', 0x0408: 'J',
	0x043A: 'k', 0x041A: 'K',
	0x043C: 'm', 0x041C: 'M',
	0x043D: 'h', 0x041D: 'H',
	0x0442: 't', 0x0422: 'T',
	0x0412: 'B', 0x0432: 'v',
	0x03B1: 'a', // Greek alpha
	0x03B5: 'e',
	0x03BF: 'o',
	0x039F: 'O',
	0x03C1: 'p',
	0x03A1: 'P',
	0x03BA: 'k',
	0x039A: 'K',
	0x03C4: 't',
	0x03A4: 'T',
	0x2170: 'i', // small Roman numerals
	0x2171: 'j',
}

func ConfusableSkeleton(s string) string {
	var b strings.Builder
	for _, r := range s {
		if IsInvisibleRune(r) {
			continue
		}
		if r >= 0xFF21 && r <= 0xFF3A { // full-width A-Z
			r = rune('A' + (r - 0xFF21))
		} else if r >= 0xFF41 && r <= 0xFF5A { // full-width a-z
			r = rune('a' + (r - 0xFF41))
		} else if r >= 0xFF10 && r <= 0xFF19 { // full-width 0-9
			r = rune('0' + (r - 0xFF10))
		} else if r == 0x0131 {
			r = 'i'
		}
		if ascii, ok := commonConfusables[r]; ok {
			b.WriteRune(ascii)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// VisualSkeleton is the pixel-confusable comparison key.
func VisualSkeleton(s string) string { return ConfusableSkeleton(s) }

func IsConfusableRune(r rune) bool {
	if (r >= 0xFF21 && r <= 0xFF3A) || (r >= 0xFF41 && r <= 0xFF5A) || (r >= 0xFF10 && r <= 0xFF19) {
		return true
	}
	_, ok := commonConfusables[r]
	return ok
}

// InspectRestrictedText validates and safely renders restricted text.
func InspectRestrictedText(s string, policy *TextPolicy) (TextReport, error) {
	p := DefaultTextPolicy()
	if policy != nil {
		p = *policy
		if p.MaxRunes == 0 {
			p.MaxRunes = 256
		}
	}
	rep := TextReport{Text: s}
	if !utf8.ValidString(s) {
		rep.InvalidUTF8 = true
		if p.RejectInvalidUTF8 {
			return rep, fmt.Errorf("displaycommit: invalid UTF-8")
		}
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			rep.InvalidUTF8 = true
			if p.RejectInvalidUTF8 {
				return rep, fmt.Errorf("displaycommit: invalid UTF-8 at byte %d", i)
			}
			b.WriteString(`\u{FFFD}`)
			i += size
			continue
		}
		rep.Runes++
		if rep.Runes > p.MaxRunes {
			return rep, fmt.Errorf("displaycommit: text exceeds %d runes", p.MaxRunes)
		}

		if r == '\\' {
			b.WriteString(`\\`)
			i += size
			continue
		}

		if IsInvisibleRune(r) {
			rep.HasInvisible = true
			rep.InvisibleRunes = append(rep.InvisibleRunes, r)
			if p.RejectInvisible {
				return rep, fmt.Errorf("displaycommit: invisible/format rune U+%04X", r)
			}
			if p.EscapeInvisible {
				fmt.Fprintf(&b, `\u{%04X}`, r)
				i += size
				continue
			}
		}

		if IsConfusableRune(r) {
			rep.HasConfusable = true
			rep.ConfusableRunes = append(rep.ConfusableRunes, r)
			if p.RejectConfusables {
				return rep, fmt.Errorf("displaycommit: confusable rune U+%04X", r)
			}
			b.WriteRune(r)
			if p.AnnotateConfusables {
				fmt.Fprintf(&b, `\u{%04X}`, r)
			}
			i += size
			continue
		}

		b.WriteRune(r)
		i += size
	}
	rep.Display = b.String()
	return rep, nil
}

// RenderRestrictedText uses the safe default text policy.
func RenderRestrictedText(s string) (string, error) {
	rep, err := InspectRestrictedText(s, nil)
	return rep.Display, err
}

// MustRenderRestrictedText is intended for tests and known-valid constants.
func MustRenderRestrictedText(s string) string {
	out, err := RenderRestrictedText(s)
	if err != nil {
		panic(err)
	}
	return out
}

// SameSafeScreen reports whether safe rendering makes two texts identical.
// With the default policy, distinct invisible/confusable code points are
// annotated and therefore do not collapse.
func SameSafeScreen(a, b string) (bool, error) {
	sa, err := RenderRestrictedText(a)
	if err != nil {
		return false, err
	}
	sb, err := RenderRestrictedText(b)
	if err != nil {
		return false, err
	}
	return sa == sb, nil
}

// ValidateRestrictedText applies the strict host policy: invalid UTF-8,
// invisible/format runes and known confusables are rejected.
func ValidateRestrictedText(s string) error {
	p := DefaultTextPolicy()
	p.RejectInvisible = true
	p.RejectConfusables = true
	_, err := InspectRestrictedText(s, &p)
	return err
}

func IsValidRestrictedText(s string) bool {
	return ValidateRestrictedText(s) == nil
}
