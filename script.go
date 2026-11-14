package netsec

import (
	"regexp"
	"strings"
)

var scriptFieldFunction = regexp.MustCompile(`\bfield\s*\(\s*['"]([^'"]+)['"]\s*\)`)

// extractScriptFields returns every document/source field that can be
// statically identified in a Painless script. safe is false when the script
// accesses fields through a dynamic expression, captures the whole document
// source, or otherwise cannot be bounded.
func extractScriptFields(source string) (fields []string, safe bool) {
	seen := make(map[string]struct{})
	unsafe := false
	add := func(field string) {
		field = strings.TrimSpace(field)
		if field == "" {
			return
		}
		if _, ok := seen[field]; ok {
			return
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}

	lower := source
	for i := 0; i < len(lower); {
		if !isIdentStart(lower[i]) {
			i++
			continue
		}
		start := i
		i++
		for i < len(lower) && isIdentPart(lower[i]) {
			i++
		}
		token := lower[start:i]

		switch token {
		case "doc":
			next := skipSpace(lower, i)
			if next < len(lower) && lower[next] == '.' {
				afterDot := skipSpace(lower, next+1)
				method, end := readIdentifier(lower, afterDot)
				if method != "containsKey" {
					// A method other than containsKey could hide a dynamic
					// field collection access.
					unsafe = true
					break
				}
				arg := skipSpace(lower, end)
				if arg >= len(lower) || lower[arg] != '(' {
					unsafe = true
					break
				}
				value, pos, ok := readQuoted(lower, skipSpace(lower, arg+1))
				if !ok {
					unsafe = true
					break
				}
				closePos := skipSpace(lower, pos)
				if closePos >= len(lower) || lower[closePos] != ')' {
					unsafe = true
					break
				}
				add(value)
				i = closePos + 1
				continue
			}
			if next >= len(lower) || lower[next] != '[' {
				unsafe = true
				break
			}
			value, pos, ok := readQuoted(lower, skipSpace(lower, next+1))
			if !ok {
				unsafe = true
				break
			}
			closePos := skipSpace(lower, pos)
			if closePos >= len(lower) || lower[closePos] != ']' {
				unsafe = true
				break
			}
			add(value)
			i = closePos + 1

		case "params":
			next := skipSpace(lower, i)
			path := ""
			pos := next
			ok := false
			if next < len(lower) && lower[next] == '.' {
				name, end := readIdentifier(lower, skipSpace(lower, next+1))
				if name != "_source" {
					break
				}
				path, pos, ok = readAccessChain(lower, end)
				if !ok {
					unsafe = true
					break
				}
				if nextNonSpaceIs(lower, pos, '(') {
					unsafe = true
					break
				}
				if path == "" {
					// Returning the entire source cannot be bounded to one
					// field.
					unsafe = true
					break
				}
				add(path)
				i = pos
				continue
			}
			if next < len(lower) && lower[next] == '[' {
				name, end, quotedOK := readQuoted(lower, skipSpace(lower, next+1))
				if !quotedOK {
					unsafe = true
					break
				}
				closePos := skipSpace(lower, end)
				if closePos >= len(lower) || lower[closePos] != ']' {
					unsafe = true
					break
				}
				if name != "_source" {
					i = closePos + 1
					continue
				}
				path, pos, ok = readAccessChain(lower, closePos+1)
				if !ok {
					unsafe = true
					break
				}
				if nextNonSpaceIs(lower, pos, '(') {
					unsafe = true
					break
				}
				if path == "" {
					unsafe = true
					break
				}
				add(path)
				i = pos
				continue
			}

		case "_source":
			// Only params._source / params['_source'] is treated as the
			// document source. A bare _source could be a local alias created
			// from an unbounded expression, so mark it unsafe.
			next := skipSpace(lower, i)
			if next < len(lower) && (lower[next] == '.' || lower[next] == '[') {
				unsafe = true
			}
		}
	}

	for _, match := range scriptFieldFunction.FindAllStringSubmatch(source, -1) {
		add(match[1])
	}

	return fields, !unsafe
}

func readAccessChain(source string, position int) (string, int, bool) {
	parts := make([]string, 0, 3)
	position = skipSpace(source, position)
	for position < len(source) {
		switch source[position] {
		case '.':
			name, end := readIdentifier(source, skipSpace(source, position+1))
			if name == "" {
				return "", position, false
			}
			parts = append(parts, name)
			position = end
		case '[':
			value, end, ok := readQuoted(source, skipSpace(source, position+1))
			if !ok {
				return "", position, false
			}
			closePos := skipSpace(source, end)
			if closePos >= len(source) || source[closePos] != ']' {
				return "", position, false
			}
			parts = append(parts, value)
			position = closePos + 1
		default:
			return strings.Join(parts, "."), position, true
		}
		position = skipSpace(source, position)
	}
	return strings.Join(parts, "."), position, true
}

func readQuoted(source string, position int) (string, int, bool) {
	if position >= len(source) || (source[position] != '\'' && source[position] != '"') {
		return "", position, false
	}
	quote := source[position]
	position++
	start := position
	for position < len(source) {
		if source[position] == '\\' {
			position += 2
			continue
		}
		if source[position] == quote {
			return source[start:position], position + 1, true
		}
		position++
	}
	return "", position, false
}

func readIdentifier(source string, position int) (string, int) {
	if position >= len(source) || !isIdentStart(source[position]) {
		return "", position
	}
	start := position
	position++
	for position < len(source) && isIdentPart(source[position]) {
		position++
	}
	return source[start:position], position
}

func skipSpace(source string, position int) int {
	for position < len(source) && (source[position] == ' ' || source[position] == '\t' || source[position] == '\n' || source[position] == '\r') {
		position++
	}
	return position
}

func nextNonSpaceIs(source string, position int, want byte) bool {
	position = skipSpace(source, position)
	return position < len(source) && source[position] == want
}

func isIdentStart(value byte) bool {
	return value == '_' || (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z')
}

func isIdentPart(value byte) bool {
	return isIdentStart(value) || (value >= '0' && value <= '9')
}

func scriptSource(value any) (string, bool, string) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return "", false, "empty script"
		}
		return typed, true, ""
	case map[string]any:
		if lang, ok := typed["lang"].(string); ok {
			lang = strings.ToLower(strings.TrimSpace(lang))
			if lang != "" && lang != "painless" && lang != "expression" {
				return "", false, "unsupported script language " + lang
			}
		}
		if id, ok := typed["id"].(string); ok && strings.TrimSpace(id) != "" {
			if _, hasSource := typed["source"]; !hasSource {
				return "", false, "stored script id cannot be statically verified"
			}
		}
		if source, ok := typed["source"].(string); ok {
			if strings.TrimSpace(source) == "" {
				return "", false, "empty script source"
			}
			return source, true, ""
		}
		if nested, ok := typed["script"]; ok {
			return scriptSource(nested)
		}
		return "", false, "script has no inline source"
	default:
		return "", false, "script is not a string or object"
	}
}
