package artifactflow

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// normalizeLogicalPath removes explicit current-directory components (".")
// and nothing else. An empty component, an absolute path, or any ".."
// component is rejected: normalization must never escape the tenant root.
func normalizeLogicalPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("artifactflow: empty logical path")
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("artifactflow: path %q is absolute; tenant root is implicit", p)
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "":
			return "", fmt.Errorf("artifactflow: path %q contains an empty component", p)
		case ".":
			continue
		case "..":
			return "", fmt.Errorf("artifactflow: path %q escapes the tenant root", p)
		}
		if !utf8.ValidString(part) {
			return "", fmt.Errorf("artifactflow: path %q is not valid UTF-8", p)
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return "", fmt.Errorf("artifactflow: path %q contains a control character", p)
			}
		}
		if strings.ContainsAny(part, "{}") {
			return "", fmt.Errorf("artifactflow: artifact path %q may not contain a capture marker", p)
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return "", fmt.Errorf("artifactflow: path %q normalizes to empty", p)
	}
	return strings.Join(out, "/"), nil
}

type pathSegment struct {
	literal string
	prefix  string
	suffix  string
	capture bool
}

type compiledRule struct {
	raw        string
	from       []pathSegment
	to         []pathSegment
	hasCapture bool
}

type placeholder struct {
	name string
}

func scanPlaceholders(s string) ([]placeholder, error) {
	var found []placeholder
	for i := 0; i < len(s); {
		switch s[i] {
		case '{':
			j := strings.IndexByte(s[i+1:], '}')
			if j < 0 {
				return nil, fmt.Errorf("artifactflow: unclosed capture marker in %q", s)
			}
			end := i + 1 + j
			name := s[i+1 : end]
			if !validCaptureName(name) {
				return nil, fmt.Errorf("artifactflow: invalid capture name %q", name)
			}
			found = append(found, placeholder{name: name})
			i = end + 1
		case '}':
			return nil, fmt.Errorf("artifactflow: unmatched '}' in %q", s)
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
		}
	}
	return found, nil
}

func validCaptureName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func normalizePattern(p string) ([]string, error) {
	if p == "" {
		return nil, fmt.Errorf("artifactflow: empty rename pattern")
	}
	if strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("artifactflow: rename pattern %q is absolute", p)
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "":
			return nil, fmt.Errorf("artifactflow: rename pattern %q contains an empty component", p)
		case ".":
			continue
		case "..":
			return nil, fmt.Errorf("artifactflow: rename pattern %q escapes the tenant root", p)
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("artifactflow: rename pattern %q normalizes to empty", p)
	}
	return out, nil
}

// compileRule converts a public rename rule into a matcher. A rule has either
// no captures (exact full-path rename) or exactly one capture marker with the
// same name on both sides. The capture always denotes one path component.
func compileRule(r RenameRule) (*compiledRule, error) {
	fromParts, err := normalizePattern(r.From)
	if err != nil {
		return nil, err
	}
	toParts, err := normalizePattern(r.To)
	if err != nil {
		return nil, err
	}
	fromPH, err := scanPlaceholders(r.From)
	if err != nil {
		return nil, err
	}
	toPH, err := scanPlaceholders(r.To)
	if err != nil {
		return nil, err
	}
	if len(fromPH) == 0 && len(toPH) == 0 {
		return &compiledRule{
			raw:  r.From + " -> " + r.To,
			from: literalSegments(fromParts),
			to:   literalSegments(toParts),
		}, nil
	}
	if len(fromPH) != 1 || len(toPH) != 1 {
		return nil, fmt.Errorf("artifactflow: rule %q -> %q must contain exactly one capture on each side, or none on both", r.From, r.To)
	}
	if fromPH[0].name != toPH[0].name {
		return nil, fmt.Errorf("artifactflow: rule %q -> %q uses different capture names", r.From, r.To)
	}
	name := fromPH[0].name
	fromSegs, err := captureSegments(fromParts, name)
	if err != nil {
		return nil, fmt.Errorf("artifactflow: rule %q -> %q: %w", r.From, r.To, err)
	}
	toSegs, err := captureSegments(toParts, name)
	if err != nil {
		return nil, fmt.Errorf("artifactflow: rule %q -> %q: %w", r.From, r.To, err)
	}
	return &compiledRule{
		raw:        r.From + " -> " + r.To,
		from:       fromSegs,
		to:         toSegs,
		hasCapture: true,
	}, nil
}

func literalSegments(parts []string) []pathSegment {
	out := make([]pathSegment, len(parts))
	for i, p := range parts {
		out[i] = pathSegment{literal: p}
	}
	return out
}

func captureSegments(parts []string, name string) ([]pathSegment, error) {
	token := "{" + name + "}"
	out := make([]pathSegment, len(parts))
	seen := 0
	for i, p := range parts {
		idx := strings.Index(p, token)
		if idx < 0 {
			if strings.ContainsAny(p, "{}") {
				return nil, fmt.Errorf("component %q contains a malformed capture marker", p)
			}
			out[i] = pathSegment{literal: p}
			continue
		}
		seen++
		if strings.Count(p, token) != 1 || strings.ContainsAny(strings.ReplaceAll(p, token, ""), "{}") {
			return nil, fmt.Errorf("component %q must contain the capture exactly once", p)
		}
		out[i] = pathSegment{
			prefix:  p[:idx],
			suffix:  p[idx+len(token):],
			capture: true,
		}
	}
	if seen != 1 {
		return nil, fmt.Errorf("capture %s must appear in exactly one component", token)
	}
	return out, nil
}

func (r *compiledRule) matchesFrom(parts []string) (string, bool) {
	if !r.hasCapture {
		return "", len(r.from) == len(parts) && joinSegments(r.from) == strings.Join(parts, "/")
	}
	if len(r.from) != len(parts) {
		return "", false
	}
	capture := ""
	for i, seg := range r.from {
		if seg.capture {
			p := parts[i]
			if !strings.HasPrefix(p, seg.prefix) || !strings.HasSuffix(p, seg.suffix) {
				return "", false
			}
			if len(p) < len(seg.prefix)+len(seg.suffix) {
				return "", false
			}
			capture = p[len(seg.prefix) : len(p)-len(seg.suffix)]
			if capture == "" || strings.Contains(capture, "/") {
				return "", false
			}
			continue
		}
		if parts[i] != seg.literal {
			return "", false
		}
	}
	return capture, true
}

func joinSegments(segs []pathSegment) string {
	parts := make([]string, len(segs))
	for i, seg := range segs {
		parts[i] = seg.literal
	}
	return strings.Join(parts, "/")
}

func (r *compiledRule) mapsTo(capture string) (string, bool) {
	if !r.hasCapture {
		return joinSegments(r.to), true
	}
	parts := make([]string, len(r.to))
	for i, seg := range r.to {
		if seg.capture {
			parts[i] = seg.prefix + capture + seg.suffix
		} else {
			parts[i] = seg.literal
		}
	}
	return strings.Join(parts, "/"), true
}

// permits reports whether the rule allows an edge from src to dst.
func (r *compiledRule) permits(src, dst string) bool {
	if src == dst {
		return true
	}
	if !r.hasCapture {
		return joinSegments(r.from) == src && joinSegments(r.to) == dst
	}
	capture, ok := r.matchesFrom(strings.Split(src, "/"))
	if !ok {
		return false
	}
	mapped, ok := r.mapsTo(capture)
	return ok && mapped == dst
}
