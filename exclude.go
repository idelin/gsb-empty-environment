package netsec

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type excludeRule struct {
	negate     bool
	anchored   bool
	basename   bool
	expression *regexp.Regexp
}

// PathExcluded reports whether a normalized module-relative path matches the
// module's exclude rules. Rules use rsync wildmatch semantics (* and ? never
// cross a slash, ** does), and a later negated rule can re-include a path.
func PathExcluded(patterns []string, cleanPath string) bool {
	return excludedBy(patterns, cleanPath)
}

func excludedBy(patterns []string, cleanPath string) bool {
	cleanPath = path.Clean(strings.TrimPrefix(filepath.ToSlash(cleanPath), "/"))
	if cleanPath == "." || cleanPath == "" {
		return false
	}

	excluded := false
	for _, pattern := range splitConfigList(patterns) {
		rule, ok := compileExcludeRule(pattern)
		if ok && rule.matches(cleanPath) {
			excluded = !rule.negate
		}
	}
	return excluded
}

func compileExcludeRule(pattern string) (excludeRule, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return excludeRule{}, false
	}

	rule := excludeRule{}
	if strings.HasPrefix(pattern, "!") {
		rule.negate = true
		pattern = strings.TrimPrefix(pattern, "!")
	}
	if pattern == "" {
		return excludeRule{}, false
	}

	pattern = filepath.ToSlash(pattern)
	if strings.HasPrefix(pattern, "./") {
		pattern = pattern[2:]
	}
	if strings.HasPrefix(pattern, "/") {
		rule.anchored = true
		pattern = strings.TrimPrefix(pattern, "/")
	}
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == "" {
		return excludeRule{}, false
	}

	rule.basename = !strings.Contains(pattern, "/") && !rule.anchored
	expression := globExpression(pattern, rule.basename)
	compiled, err := regexp.Compile("^" + expression + "$")
	if err != nil {
		return excludeRule{}, false
	}
	rule.expression = compiled
	return rule, true
}

func (rule excludeRule) matches(cleanPath string) bool {
	if rule.basename {
		if rule.expression.MatchString(path.Base(cleanPath)) {
			return true
		}
		for _, component := range strings.Split(cleanPath, "/") {
			if rule.expression.MatchString(component) {
				return true
			}
		}
		return false
	}

	if rule.expression.MatchString(cleanPath) {
		return true
	}
	for index := 0; index < len(cleanPath); index++ {
		if cleanPath[index] == '/' && rule.expression.MatchString(cleanPath[:index]) {
			return true
		}
	}
	if rule.anchored {
		return false
	}
	for index := 0; index < len(cleanPath); index++ {
		if cleanPath[index] == '/' && rule.expression.MatchString(cleanPath[index+1:]) {
			return true
		}
	}
	return false
}

// globExpression translates rsync wildmatch syntax into a regular expression.
func globExpression(pattern string, basename bool) string {
	var builder strings.Builder
	for index := 0; index < len(pattern); {
		switch pattern[index] {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				index += 2
				if index < len(pattern) && pattern[index] == '/' {
					builder.WriteString(`(?:.*/)?`)
					index++
				} else {
					builder.WriteString(`.*`)
				}
			} else {
				builder.WriteString(`[^/]*`)
				index++
			}
		case '?':
			builder.WriteString(`[^/]`)
			index++
		case '[':
			end := strings.IndexByte(pattern[index+1:], ']')
			if end < 0 {
				builder.WriteString(`\[`)
				index++
				continue
			}
			end += index + 1
			class := pattern[index+1 : end]
			if strings.HasPrefix(class, "!") {
				class = "^" + class[1:]
			}
			builder.WriteByte('[')
			builder.WriteString(class)
			builder.WriteByte(']')
			index = end + 1
		case '\\':
			if index+1 < len(pattern) {
				builder.WriteString(regexp.QuoteMeta(string(pattern[index+1])))
				index += 2
			} else {
				builder.WriteString(`\\`)
				index++
			}
		default:
			builder.WriteString(regexp.QuoteMeta(string(pattern[index])))
			index++
		}
	}

	expression := builder.String()
	if basename {
		return `(?:.*/)?` + expression
	}
	return expression
}
