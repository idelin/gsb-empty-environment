package difftest

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Rules describes the only transformations the judge is allowed to apply.
// Anything outside these rules is compared literally.
type Rules struct {
	TrimSpace     bool     `json:"trim_space"`     // trim leading/trailing whitespace
	CollapseSpace bool     `json:"collapse_space"` // collapse whitespace runs to one space
	CaseFold      bool     `json:"case_fold"`      // Unicode case folding
	SortLines     bool     `json:"sort_lines"`     // order-insensitive line comparison
	StripPatterns []string `json:"strip_patterns"` // regexes removed before comparison
	FloatMode     bool     `json:"float_mode"`     // compare whitespace-separated tokens, numbers with tolerance
	FloatStrict   bool     `json:"float_strict"`   // non-numeric token in float mode is a parse failure
	FloatTol      float64  `json:"float_tol"`      // absolute+relative tolerance for float tokens

	compiled []*regexp.Regexp
}

// Compile validates and precompiles the strip patterns.
func (r *Rules) Compile() error {
	r.compiled = r.compiled[:0]
	for _, p := range r.StripPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("strip pattern %q: %v", p, err)
		}
		r.compiled = append(r.compiled, re)
	}
	if r.FloatTol < 0 {
		return fmt.Errorf("float tolerance must be >= 0")
	}
	return nil
}

// ParseError reports output that cannot be interpreted under the rules.
type ParseError struct {
	Side  string
	Token string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("executor %s: token %q is not a number", e.Side, e.Token)
}

// token is one comparable unit: either a number or a literal string.
type token struct {
	isNum bool
	num   float64
	str   string
}

// normalize applies the textual rules and returns the canonical form plus,
// in float mode, the token stream. A *ParseError is returned when strict
// float parsing fails.
func (r *Rules) normalize(side, raw string) (string, []token, error) {
	s := raw
	for _, re := range r.compiled {
		s = re.ReplaceAllString(s, "")
	}
	if r.CaseFold {
		s = strings.ToLower(s)
	}
	if r.TrimSpace {
		s = strings.TrimSpace(s)
	}
	if r.CollapseSpace {
		s = strings.Join(strings.Fields(s), " ")
	}
	if r.SortLines {
		lines := strings.Split(s, "\n")
		sort.Strings(lines)
		s = strings.Join(lines, "\n")
	}
	if !r.FloatMode {
		return s, nil, nil
	}
	fields := strings.Fields(s)
	toks := make([]token, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.ParseFloat(f, 64)
		if err != nil {
			if r.FloatStrict {
				return s, nil, &ParseError{Side: side, Token: f}
			}
			toks = append(toks, token{str: f})
			continue
		}
		toks = append(toks, token{isNum: true, num: n})
	}
	return s, toks, nil
}

// tokensEqual compares token streams under the float tolerance.
func (r *Rules) tokensEqual(a, b []token) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ta, tb := a[i], b[i]
		if ta.isNum && tb.isNum {
			diff := ta.num - tb.num
			if diff < 0 {
				diff = -diff
			}
			largest := ta.num
			if tb.num > largest {
				largest = tb.num
			}
			if largest < 0 {
				largest = -largest
			}
			if diff > r.FloatTol && diff > r.FloatTol*largest {
				return false
			}
			continue
		}
		if ta.isNum != tb.isNum || ta.str != tb.str {
			return false
		}
	}
	return true
}
