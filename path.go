package netsec

import "strings"

type pathSegment struct {
	name      string
	predicate string
}

// rulePathCovers reports whether rulePath selects targetPath or one of its
// ancestors. Path segments are compared as nodes, not as string prefixes, so
// "/system" covers "/system/user" but not "/systematic".
func rulePathCovers(rulePath, targetPath string) bool {
	rule, ok := parsePath(rulePath)
	if !ok {
		return false
	}
	target, ok := parsePath(targetPath)
	if !ok {
		return false
	}

	if len(rule) == 0 {
		return true
	}
	if len(rule) > len(target) {
		return false
	}

	for i, segment := range rule {
		if !segmentCovers(segment, target[i]) {
			return false
		}
	}
	return true
}

func parsePath(raw string) ([]pathSegment, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "/" {
		return nil, true
	}

	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}

	segments := make([]pathSegment, 0, strings.Count(raw, "/"))
	var b strings.Builder
	depth := 0
	var quote byte
	escaped := false

	flush := func() bool {
		part := strings.TrimSpace(b.String())
		b.Reset()
		if part == "" {
			return true
		}
		if part == "." || part == ".." {
			return false
		}
		segment, ok := parseSegment(part)
		if !ok {
			return false
		}
		segments = append(segments, segment)
		return true
	}

	for i := 0; i < len(raw); i++ {
		c := raw[i]

		if quote != 0 {
			b.WriteByte(c)
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}

		switch c {
		case '\'', '"':
			quote = c
			b.WriteByte(c)
		case '[':
			depth++
			b.WriteByte(c)
		case ']':
			if depth == 0 {
				return nil, false
			}
			depth--
			b.WriteByte(c)
		case '/':
			if depth != 0 {
				b.WriteByte(c)
				continue
			}
			if !flush() {
				return nil, false
			}
		default:
			b.WriteByte(c)
		}
	}

	if quote != 0 || depth != 0 {
		return nil, false
	}
	if !flush() {
		return nil, false
	}
	return segments, true
}

func parseSegment(raw string) (pathSegment, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "." || raw == ".." {
		return pathSegment{}, false
	}

	predicate := ""
	if open := strings.IndexByte(raw, '['); open >= 0 {
		predicate = strings.TrimSpace(raw[open:])
		raw = strings.TrimSpace(raw[:open])
	}
	if raw == "" {
		return pathSegment{}, false
	}

	return pathSegment{name: raw, predicate: predicate}, true
}

func segmentCovers(rule, target pathSegment) bool {
	if !nameCovers(rule.name, target.name) {
		return false
	}
	if rule.predicate == "" {
		return true
	}
	return predicateCovers(rule.predicate, target.predicate)
}

func nameCovers(rule, target string) bool {
	rulePrefix, ruleLocal := splitName(rule)
	targetPrefix, targetLocal := splitName(target)

	if ruleLocal != "*" && ruleLocal != targetLocal {
		return false
	}
	if rulePrefix == "" || rulePrefix == "*" {
		return true
	}
	return rulePrefix == targetPrefix
}

func splitName(name string) (prefix, local string) {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

func predicateCovers(rule, target string) bool {
	if rule == target {
		return true
	}
	// A more specific target with additional predicates is still in the
	// node set selected by the rule.
	return len(target) > len(rule) &&
		strings.HasPrefix(target, rule) &&
		target[len(rule)] == '['
}
