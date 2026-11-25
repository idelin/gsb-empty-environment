package artifactflow

import (
	"strings"
)

// NormalizePath normalizes a tenant-scoped logical artifact path.
//
// The only permitted normalization is elimination of explicit current
// directory components ("."). Absolute paths, parent components (".."),
// empty components, backslashes and NUL bytes are rejected, so a normalized
// path can never cross the tenant root. Receipt paths and slot paths are all
// expressed relative to that root.
func NormalizePath(p Path) (Path, error) {
	raw := string(p)
	if raw == "" {
		return "", &ValidationError{Field: "path", Reason: "path is empty"}
	}
	if strings.ContainsRune(raw, 0) || strings.ContainsRune(raw, '\\') {
		return "", &ValidationError{Field: "path", Reason: "path contains an illegal character: " + raw}
	}
	if raw[0] == '/' {
		return "", &ValidationError{Field: "path", Reason: "path must be relative to the tenant root: " + raw}
	}
	parts := strings.Split(raw, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			if part == "" {
				return "", &ValidationError{Field: "path", Reason: "path contains an empty component: " + raw}
			}
			// Explicit current-directory component: the only normalization
			// allowed. It is simply dropped.
			continue
		case "..":
			return "", &ValidationError{Field: "path", Reason: "parent components are forbidden and would cross the tenant root: " + raw}
		default:
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return "", &ValidationError{Field: "path", Reason: "path resolves to the tenant root itself: " + raw}
	}
	return Path(strings.Join(out, "/")), nil
}

// NormalizeTenantRoot normalizes the tenant root. An empty tenant root means
// the flow is scoped to the top-level namespace. The same escape rules as
// NormalizePath apply.
func NormalizeTenantRoot(root Path) (Path, error) {
	if string(root) == "" {
		return "", nil
	}
	return NormalizePath(root)
}

// WithinRoot reports whether normalized path p stays inside normalized root.
// Because NormalizePath forbids "..", every normalized relative path is
// inside the root by construction; this helper documents and re-checks the
// invariant for callers that build paths externally.
func WithinRoot(root, p Path) bool {
	rs := string(root)
	ps := string(p)
	if rs == "" {
		return ps != "" && ps[0] != '/'
	}
	if ps != rs && !strings.HasPrefix(ps, rs+"/") {
		return false
	}
	return true
}

type segment struct {
	capture bool
	name    string
	literal string
	prefix  string
	suffix  string
}

type pattern struct {
	raw      Path
	segments []segment
}

// compilePattern compiles a slot path or rule target. A segment of the form
// "{name}" is a capture component matching a single path component; any
// other segment is a literal.
func compilePattern(p Path) (*pattern, error) {
	raw := string(p)
	if raw == "" {
		return nil, &ValidationError{Field: "pattern", Reason: "pattern is empty"}
	}
	if strings.ContainsRune(raw, 0) || strings.ContainsRune(raw, '\\') {
		return nil, &ValidationError{Field: "pattern", Reason: "pattern contains an illegal character: " + raw}
	}
	if raw[0] == '/' {
		return nil, &ValidationError{Field: "pattern", Reason: "pattern must be relative to the tenant root: " + raw}
	}
	parts := strings.Split(raw, "/")
	pat := &pattern{raw: p, segments: make([]segment, 0, len(parts))}
	for _, part := range parts {
		switch {
		case part == "":
			return nil, &ValidationError{Field: "pattern", Reason: "pattern contains an empty component: " + raw}
		case part == "..":
			return nil, &ValidationError{Field: "pattern", Reason: "parent components are forbidden in patterns: " + raw}
		case part == ".":
			return nil, &ValidationError{Field: "pattern", Reason: "current-directory components are not allowed in patterns: " + raw}
		case strings.ContainsAny(part, "{}"):
			open := strings.IndexByte(part, '{')
			close := strings.IndexByte(part, '}')
			if open < 0 || close < 0 || close < open ||
				strings.Contains(part[open+1:close], "{") || strings.Contains(part[close+1:], "}") ||
				strings.Contains(part[:open], "}") {
				return nil, &ValidationError{Field: "pattern", Reason: "illegal capture component: " + part}
			}
			name := part[open+1 : close]
			if name == "" || strings.ContainsAny(name, "{}/") {
				return nil, &ValidationError{Field: "pattern", Reason: "illegal capture component: " + part}
			}
			pat.segments = append(pat.segments, segment{
				capture: true,
				name:    name,
				prefix:  part[:open],
				suffix:  part[close+1:],
			})
		default:
			pat.segments = append(pat.segments, segment{literal: part})
		}
	}
	return pat, nil
}

// match matches a normalized path against the pattern. Existing bindings are
// honored, so a capture that already binds must equal the new component. The
// returned map is nil on failure; on success it contains the merged bindings.
func (pat *pattern) match(normalized Path, existing map[string]string) (map[string]string, bool) {
	parts := strings.Split(string(normalized), "/")
	if len(parts) != len(pat.segments) {
		return nil, false
	}
	bindings := make(map[string]string, len(existing)+len(pat.segments))
	for k, v := range existing {
		bindings[k] = v
	}
	for i, seg := range pat.segments {
		part := parts[i]
		if seg.capture {
			if !strings.HasPrefix(part, seg.prefix) || !strings.HasSuffix(part, seg.suffix) ||
				len(part) < len(seg.prefix)+len(seg.suffix) {
				return nil, false
			}
			value := part[len(seg.prefix) : len(part)-len(seg.suffix)]
			if value == "" {
				return nil, false
			}
			if prev, ok := bindings[seg.name]; ok && prev != value {
				return nil, false
			}
			bindings[seg.name] = value
		} else if seg.literal != part {
			return nil, false
		}
	}
	return bindings, true
}
