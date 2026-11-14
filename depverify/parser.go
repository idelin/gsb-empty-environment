package depverify

import (
	"fmt"
	"strings"
)

// Requirement is a single module listed by a require directive.
type Requirement struct {
	Path     string
	Version  string
	Indirect bool
	Line     int
}

// Exclusion is a single module version listed by an exclude directive.
type Exclusion struct {
	Path    string
	Version string
	Line    int
}

// Replacement is a single replace directive.
//
// Local is true for filesystem replacements (the right hand side has no
// version). NewPath/NewVersion describe the replacement target; for local
// replacements NewPath is the directory path and NewVersion is empty.
// Wildcard is true for path-only replacements such as "example.com/mod =>
// example.com/fork", which apply to every version of the module.
type Replacement struct {
	OldPath    string
	OldVersion string
	NewPath    string
	NewVersion string
	Local      bool
	Wildcard   bool
	Line       int
}

// GoModFile is the parsed subset of a go.mod relevant to verification.
type GoModFile struct {
	Requires     []Requirement
	Replacements []Replacement
	Excludes     []Exclusion
}

// ParseError describes a syntax error in a go.mod or go.sum file.
type ParseError struct {
	File string
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	if e.File == "" {
		return fmt.Sprintf("go.mod:%d: %s", e.Line, e.Msg)
	}
	return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Msg)
}

type tokenKind int

const (
	tokenWord tokenKind = iota
	tokenString
	tokenPunct
	tokenNewline
	tokenComment
)

type token struct {
	kind tokenKind
	text string
	line int
}

// tokenize implements the go.mod lexical rules needed here: line comments,
// block comments, quoted and raw strings, identifiers and punctuation.
// Newlines are emitted as tokens because they terminate directives.
func tokenize(input string) ([]token, error) {
	var tokens []token
	line := 1
	for i := 0; i < len(input); {
		c := input[i]
		switch {
		case c == '\n':
			tokens = append(tokens, token{tokenNewline, "\n", line})
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case strings.HasPrefix(input[i:], "//"):
			// A line comment is retained as a token so directives such as
			// require can see the "// indirect" marker. Module paths never
			// contain "//" (slashes are single and surrounded by letters),
			// so this cannot consume path content.
			i += 2
			start := i
			for i < len(input) && input[i] != '\n' {
				i++
			}
			tokens = append(tokens, token{tokenComment, strings.TrimSpace(input[start:i]), line})
		case strings.HasPrefix(input[i:], "/*"):
			startLine := line
			i += 2
			closed := false
			for i < len(input) {
				if input[i] == '\n' {
					line++
				}
				if strings.HasPrefix(input[i:], "*/") {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, &ParseError{Line: startLine, Msg: "unterminated block comment"}
			}
		case c == '"' || c == '`':
			quote := c
			startLine := line
			i++
			var b strings.Builder
			closed := false
			for i < len(input) {
				ch := input[i]
				if quote == '`' {
					if ch == '`' {
						i++
						closed = true
						break
					}
					if ch == '\n' {
						line++
					}
					b.WriteByte(ch)
					i++
					continue
				}
				if ch == '"' {
					i++
					closed = true
					break
				}
				if ch == '\n' {
					break
				}
				if ch == '\\' && i+1 < len(input) {
					switch input[i+1] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					case 'r':
						b.WriteByte('\r')
					case '\\':
						b.WriteByte('\\')
					case '"':
						b.WriteByte('"')
					default:
						b.WriteByte(input[i+1])
					}
					i += 2
					continue
				}
				b.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, &ParseError{Line: startLine, Msg: "unterminated string literal"}
			}
			tokens = append(tokens, token{tokenString, b.String(), startLine})
		case c == '(' || c == ')' || c == '=' || c == '>':
			tokens = append(tokens, token{tokenPunct, string(c), line})
			i++
		default:
			start := i
			for i < len(input) {
				ch := input[i]
				if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' ||
					ch == '(' || ch == ')' || ch == '"' || ch == '`' {
					break
				}
				if strings.HasPrefix(input[i:], "//") || strings.HasPrefix(input[i:], "/*") {
					break
				}
				i++
			}
			tokens = append(tokens, token{tokenWord, input[start:i], line})
		}
	}
	return tokens, nil
}
