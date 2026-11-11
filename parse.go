package netsec

import (
	"errors"
	"strings"
)

// FormatError describes a malformed template. It is returned together with a
// safe error marker written to the output. FormatError never wraps or embeds
// argument values.
type FormatError struct {
	reason string
	pos    int
}

func (e *FormatError) Error() string {
	return "netsec: malformed format string at byte " + itoa(e.pos) + ": " + e.reason
}

// Is reports all FormatError values as equivalent so callers can match a
// single sentinel without inspecting internals, and also matches ErrFormat.
func (e *FormatError) Is(target error) bool {
	if target == ErrFormat {
		return true
	}
	_, ok := target.(*FormatError)
	return ok
}

// ErrFormat is the sentinel matched by errors.Is for any template problem.
var ErrFormat = errors.New("netsec: malformed format string")

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

type segmentKind int

const (
	segLiteral segmentKind = iota
	segPercent             // an escaped %% literal
	segVerb
)

// verb holds a parsed conversion. Explicit indexes and star width/precision
// are resolved during rendering; the parser only records their shape.
type verb struct {
	// explicitIndex is the 1-based [n] index, or 0 when implicit.
	explicitIndex int

	// Starred width/precision consume their own arguments. A starred field with
	// starIndex 0 takes the next implicit argument; otherwise it takes the
	// explicit 1-based starIndex position.
	widthStar bool
	widthIdx  int
	precStar  bool
	precIdx   int

	hasWidth bool
	width    int
	hasPrec  bool
	prec     int

	flags string
	verb  byte
	pos   int
}

type segment struct {
	kind segmentKind
	text string
	verb *verb
}

// parse splits format into literal and conversion segments. Any malformed
// conversion aborts the whole parse with a FormatError; a failed parse never
// yields partial output derived from arguments.
func parse(format string) ([]segment, error) {
	var segs []segment
	var lit strings.Builder

	flush := func() {
		if lit.Len() > 0 {
			segs = append(segs, segment{kind: segLiteral, text: lit.String()})
			lit.Reset()
		}
	}

	for i := 0; i < len(format); {
		if format[i] != '%' {
			lit.WriteByte(format[i])
			i++
			continue
		}

		start := i
		i++
		if i >= len(format) {
			flush()
			return nil, &FormatError{reason: "trailing bare %", pos: start}
		}

		if format[i] == '%' {
			flush()
			segs = append(segs, segment{kind: segPercent, text: "%"})
			i++
			continue
		}

		v := &verb{pos: start}

		// Flags: each character may appear at most once.
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			if strings.Contains(v.flags, string(format[i])) {
				return nil, &FormatError{reason: "duplicate flag", pos: i}
			}
			v.flags += string(format[i])
			i++
		}

		// Leading explicit argument index: [n].
		if i < len(format) && format[i] == '[' {
			n, next, err := parseBracketIndex(format, i)
			if err != nil {
				return nil, err
			}
			// Tentatively the value index; a following star claims it instead.
			v.explicitIndex = n
			i = next
		}

		// Width.
		if i < len(format) && format[i] == '*' {
			i++
			v.hasWidth = true
			v.widthStar = true
			// An implicit star cannot follow an explicit value index.
			if v.explicitIndex > 0 && i < len(format) && format[i] != '[' {
				return nil, &FormatError{reason: "implicit star mixed with explicit index", pos: i}
			}
			if v.explicitIndex > 0 {
				v.widthIdx = v.explicitIndex
				v.explicitIndex = 0
			}
			if i < len(format) && format[i] == '[' {
				n, next, err := parseBracketIndex(format, i)
				if err != nil {
					return nil, err
				}
				v.explicitIndex = n
				i = next
			}
		} else if n, ok, next := parseDecimal(format, i); ok {
			v.hasWidth = true
			v.width = n
			i = next
		}

		// Precision.
		if i < len(format) && format[i] == '.' {
			i++
			v.hasPrec = true
			if i < len(format) && format[i] == '*' {
				i++
				v.precStar = true
				// An implicit star cannot follow an explicit value index.
				if v.explicitIndex > 0 && i < len(format) && format[i] != '[' {
					return nil, &FormatError{reason: "implicit star mixed with explicit index", pos: i}
				}
				if v.explicitIndex > 0 {
					v.precIdx = v.explicitIndex
					v.explicitIndex = 0
				}
				if i < len(format) && format[i] == '[' {
					n, next, err := parseBracketIndex(format, i)
					if err != nil {
						return nil, err
					}
					v.explicitIndex = n
					i = next
				}
			} else if n, ok, next := parseDecimal(format, i); ok {
				v.prec = n
				i = next
			}
		}

		if i >= len(format) {
			return nil, &FormatError{reason: "missing verb", pos: start}
		}

		// Explicit argument index preceding the verb, including the post-star
		// value index in the indexed-star form %[1]*[2]s.
		if format[i] == '[' {
			n, next, err := parseBracketIndex(format, i)
			if err != nil {
				return nil, err
			}
			if v.explicitIndex != 0 {
				return nil, &FormatError{reason: "repeated argument index", pos: i}
			}
			v.explicitIndex = n
			i = next
		}

		if i >= len(format) {
			return nil, &FormatError{reason: "missing verb", pos: start}
		}

		ch := format[i]
		if !isVerb(ch) {
			return nil, &FormatError{reason: "unsupported verb %" + string(ch), pos: i}
		}
		v.verb = ch
		i++

		flush()
		segs = append(segs, segment{kind: segVerb, verb: v})
	}

	flush()
	return segs, nil
}

func isVerb(ch byte) bool {
	return strings.IndexByte("vTstqxXdobOeEfFgGc%", ch) >= 0
}

// parseBracketIndex parses [n] at position of '[' and returns the 1-based
// index plus the position just past ']'.
func parseBracketIndex(format string, at int) (int, int, error) {
	j := at + 1
	n, ok, next := parseDecimal(format, j)
	if !ok || n < 1 {
		return 0, 0, &FormatError{reason: "invalid argument index", pos: at}
	}
	j = next
	if j >= len(format) || format[j] != ']' {
		return 0, 0, &FormatError{reason: "missing ] after argument index", pos: at}
	}
	return n, j + 1, nil
}

func parseDecimal(format string, at int) (value int, ok bool, next int) {
	j := at
	n := 0
	for j < len(format) && format[j] >= '0' && format[j] <= '9' {
		n = n*10 + int(format[j]-'0')
		j++
	}
	if j == at {
		return 0, false, at
	}
	return n, true, j
}
