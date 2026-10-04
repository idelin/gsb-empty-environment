// Package normalize applies the problem-given normalization rules to raw
// stdout before any comparison happens. Comparison is only ever performed
// on the normalized form.
package normalize

import (
	"fmt"
	"strconv"
	"strings"
)

// Rules are the problem-given normalization rules. They are the ONLY
// criteria the judge is allowed to use.
type Rules struct {
	TrimLineTrailingSpace    bool `json:"trim_line_trailing_space"`
	IgnoreTrailingBlankLines bool `json:"ignore_trailing_blank_lines"`
	CollapseWhitespace       bool `json:"collapse_whitespace"`
	CaseInsensitive          bool `json:"case_insensitive"`
	// NumericTokens requires every whitespace-separated token to parse as a
	// number; a token that does not parse is a PARSE FAILURE, not a
	// semantic difference.
	NumericTokens bool `json:"numeric_tokens"`
	// FloatTolerance > 0 compares numeric tokens with absolute tolerance.
	FloatTolerance float64 `json:"float_tolerance"`
}

// ParseError marks output that violates the problem-given format rules.
type ParseError struct {
	Token string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("token %q is not a number", e.Token)
}

// Result is the normalized view of one executor's stdout.
type Result struct {
	Canonical string    `json:"canonical"`
	Numbers   []float64 `json:"numbers,omitempty"` // populated when NumericTokens
}

// Normalize applies the rules to raw output. A *ParseError is returned when
// the output cannot be parsed under the problem-given format rules.
func Normalize(out string, r Rules) (Result, error) {
	text := out

	if r.TrimLineTrailingSpace {
		lines := strings.Split(text, "\n")
		for i, ln := range lines {
			lines[i] = strings.TrimRight(ln, " \t\r")
		}
		text = strings.Join(lines, "\n")
	}
	if r.IgnoreTrailingBlankLines {
		text = strings.TrimRight(text, " \t\r\n")
	}
	if r.CollapseWhitespace {
		text = strings.Join(strings.Fields(text), " ")
	}
	if r.CaseInsensitive {
		text = strings.ToLower(text)
	}

	res := Result{Canonical: text}
	if r.NumericTokens {
		tokens := strings.Fields(text)
		res.Numbers = make([]float64, 0, len(tokens))
		for _, tok := range tokens {
			v, err := strconv.ParseFloat(tok, 64)
			if err != nil {
				return res, &ParseError{Token: tok}
			}
			res.Numbers = append(res.Numbers, v)
		}
	}
	return res, nil
}

// Equal compares two normalized results under the rules.
func Equal(a, b Result, r Rules) bool {
	if r.NumericTokens {
		if len(a.Numbers) != len(b.Numbers) {
			return false
		}
		for i := range a.Numbers {
			diff := a.Numbers[i] - b.Numbers[i]
			if diff < 0 {
				diff = -diff
			}
			if diff > r.FloatTolerance {
				return false
			}
		}
		return true
	}
	return a.Canonical == b.Canonical
}
