package robotstxt

import (
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"
)

// rule is a single Allow or Disallow line belonging to a group.
type rule struct {
	allow    bool
	path     string // rule path as written, without a trailing "$"
	decoded  string // percent-decoded rule path
	anchored bool   // the value ended with "$"
}

// robots is the parsed representation of a robots.txt body.
type robots struct {
	// groups maps a lowercased user-agent token (or "*") to its merged rules.
	groups map[string][]rule
}

const utf8BOM = "\ufeff"

// parse parses a robots.txt body per RFC 9309 section 2.2.
//
// The caller is expected to have applied the 500 KiB size cap; parse operates
// on whatever bytes it receives.
func parse(body []byte) *robots {
	r := &robots{groups: make(map[string][]rule)}

	text := strings.TrimPrefix(string(body), utf8BOM)

	var agents []string
	groupOpen := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			continue
		}
		if h := strings.IndexByte(line, '#'); h >= 0 {
			line = line[:h]
		}

		field, value, ok := splitField(line)
		if !ok {
			// Malformed lines are ignored without ending a pending group.
			continue
		}

		switch field = strings.ToLower(strings.TrimSpace(field)); field {
		case "user-agent":
			if !groupOpen {
				agents = nil
				groupOpen = true
			}
			token := strings.ToLower(value)
			if token == "" {
				continue
			}
			agents = append(agents, token)
		case "allow", "disallow":
			if value == "" {
				// An empty Disallow value carries no rules; per RFC 9309 it
				// neither adds a rule nor ends the group.
				continue
			}
			if len(agents) == 0 {
				continue
			}
			groupOpen = false
			rl, valid := newRule(field == "allow", value)
			if !valid {
				continue
			}
			// Multiple consecutive User-agent lines share the same rules, and
			// groups declared under the same name are merged (section 2.4.1).
			for _, token := range agents {
				r.groups[token] = append(r.groups[token], rl)
			}
		default:
			// Unknown fields (e.g. Sitemap, Crawl-delay) are ignored without
			// ending any pending user-agent group.
			continue
		}
	}

	return r
}

// splitField splits a line into its field name and value around the first ":",
// stripping comments and leading whitespace from the value.
func splitField(line string) (field, value string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", "", false
	}
	field = strings.TrimSpace(line[:i])
	value = strings.TrimSpace(line[i+1:])
	if field == "" {
		return "", "", false
	}
	return field, value, true
}

// newRule builds a rule from an Allow/Disallow value. ok is false when the
// value cannot be percent-decoded, in which case the rule is ignored.
func newRule(allow bool, value string) (rule, bool) {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return rule{}, false
	}

	anchored := strings.HasSuffix(value, "$")
	path := value
	if anchored {
		path = value[:len(value)-1]
	}
	decodedPath := decoded
	if anchored {
		decodedPath = decoded[:len(decoded)-1]
	}

	return rule{
		allow:    allow,
		path:     path,
		decoded:  decodedPath,
		anchored: anchored,
	}, true
}

// selectGroup returns the merged rule set applicable to userAgent per
// RFC 9309 section 2.3.1.1: the most specific (longest) user-agent token that
// is a case-insensitive substring of the crawler identifier, falling back to
// the "*" group.
func (r *robots) selectGroup(userAgent string) ([]rule, bool) {
	ua := strings.ToLower(userAgent)

	names := make([]string, 0, len(r.groups))
	for name := range r.groups {
		if name != "*" {
			names = append(names, name)
		}
	}
	sort.Strings(names) // deterministic tie handling

	best := ""
	for _, name := range names {
		if strings.Contains(ua, name) && utf8.RuneCountInString(name) > utf8.RuneCountInString(best) {
			best = name
		}
	}

	if best != "" {
		return r.groups[best], true
	}
	if rules, ok := r.groups["*"]; ok {
		return rules, true
	}
	return nil, false
}

// allowed applies the Allow/Disallow rules to the two forms of the request
// path per RFC 9309 section 2.3.2: the longest matching rule wins, with Allow
// winning ties. An empty or absent group allows everything.
func (r *robots) allowed(userAgent, rawPath, decodedPath string) bool {
	rules, ok := r.selectGroup(userAgent)
	if !ok || len(rules) == 0 {
		return true
	}

	matchLength := -1
	decision := true

	for _, rl := range rules {
		// Section 2.3.2.3: percent-encoded rules are decoded and compared with
		// the decoded path; unencoded rules are compared with the raw path.
		// The two forms are never cross-matched.
		var matches bool
		if containsPercentTriplet(rl.path) {
			matches = globMatch(rl.decoded, decodedPath, rl.anchored)
		} else {
			matches = globMatch(rl.path, rawPath, rl.anchored)
		}
		if !matches {
			continue
		}

		length := utf8.RuneCountInString(rl.path)
		if length > matchLength {
			matchLength = length
			decision = rl.allow
		} else if length == matchLength && rl.allow {
			// Equal length: Allow wins over Disallow.
			decision = true
		}
	}

	return decision
}

// globMatch reports whether pattern matches path. "*" matches any run of
// characters (including none). When anchored is false a prefix match
// suffices, matching default robots.txt prefix semantics; when true the whole
// path must be consumed (the "$" case). Matching is rune-based so multibyte
// characters compare correctly.
func globMatch(pattern, path string, anchored bool) bool {
	p := []rune(pattern)
	s := []rune(path)

	if !containsRune(p, '*') {
		if anchored {
			return string(p) == string(s)
		}
		return len(s) >= len(p) && string(s[:len(p)]) == string(p)
	}

	pi, si, starPi, starSi := 0, 0, -1, 0
	pLen, sLen := len(p), len(s)

	for si < sLen {
		switch {
		case pi < pLen && p[pi] == '*':
			starPi = pi
			starSi = si
			pi++
		case pi < pLen && p[pi] == s[si]:
			pi++
			si++
		case starPi >= 0:
			pi = starPi + 1
			starSi++
			si = starSi
		default:
			return false
		}
	}

	for pi < pLen && p[pi] == '*' {
		pi++
	}

	return pi == pLen
}

func containsRune(runes []rune, want rune) bool {
	for _, r := range runes {
		if r == want {
			return true
		}
	}
	return false
}

// containsPercentTriplet reports whether s contains a percent-encoded triplet
// ("%" followed by two hex digits), per RFC 3986's pct-encoded production.
func containsPercentTriplet(s string) bool {
	for i := 0; i+3 <= len(s); i++ {
		if s[i] == '%' && isHex(s[i+1]) && isHex(s[i+2]) {
			return true
		}
	}
	return false
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}
