package condbuilder

import (
	"fmt"
	"strings"
)

// sqlKeywords that may never appear in a generated single-statement query.
// Keeping an explicit denylist means a structural sanity check still fails
// closed if a whitelist/escaping bug ever lets unexpected text through.
var bannedKeywords = map[string]bool{
	"INSERT": true, "UPDATE": true, "DELETE": true, "DROP": true,
	"ALTER": true, "TRUNCATE": true, "CREATE": true, "REPLACE": true,
	"GRANT": true, "REVOKE": true, "ATTACH": true, "PRAGMA": true,
	"INTO": true,
}

// token is a minimal lexical token used only for structural validation.
type token struct {
	kind string // "word", "number", "string", "ident", "punct"
	text string
}

// tokenize performs a defensive lexical scan. It understands single-quoted
// strings (with ” escapes) and back-tick identifiers, so punctuation inside
// them cannot confuse the balance/keyword checks.
func tokenize(sql string) ([]token, error) {
	var tokens []token
	runes := []rune(sql)
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			i++
		case r == '\'':
			j := i + 1
			for j < len(runes) {
				if runes[j] == '\'' {
					if j+1 < len(runes) && runes[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(runes) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			tokens = append(tokens, token{"string", string(runes[i : j+1])})
			i = j + 1
		case r == '`':
			j := i + 1
			for j < len(runes) && runes[j] != '`' {
				j++
			}
			if j >= len(runes) {
				return nil, fmt.Errorf("unterminated quoted identifier")
			}
			tokens = append(tokens, token{"ident", string(runes[i+1 : j])})
			i = j + 1
		case r == '(' || r == ')' || r == ',' || r == '*' ||
			r == '=' || r == '<' || r == '>' || r == ';' || r == '?' || r == '.':
			tokens = append(tokens, token{"punct", string(r)})
			i++
		case (r >= '0' && r <= '9') || (r == '-' && i+1 < len(runes) && runes[i+1] >= '0' && runes[i+1] <= '9'):
			j := i + 1
			for j < len(runes) && (runes[j] >= '0' && runes[j] <= '9' || runes[j] == '.') {
				j++
			}
			tokens = append(tokens, token{"number", string(runes[i:j])})
			i = j
		case isIdentStart(r):
			j := i + 1
			for j < len(runes) && isIdentPart(runes[j]) {
				j++
			}
			tokens = append(tokens, token{"word", string(runes[i:j])})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q in query", r)
		}
	}
	return tokens, nil
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isIdentPart(r rune) bool {
	return isIdentStart(r) || (r >= '0' && r <= '9')
}

// ValidateStructure checks a fully assembled SQL string for structural
// integrity: exactly one statement, balanced parentheses, no statement
// terminator, no data-definition/modification keywords, and quoted
// identifiers that still match the safe identifier pattern. It complements
// (never replaces) whitelisting and parameter binding.
func ValidateStructure(sql string) error {
	if strings.TrimSpace(sql) == "" {
		return fmt.Errorf("empty SQL statement")
	}
	tokens, err := tokenize(sql)
	if err != nil {
		return err
	}
	if len(tokens) == 0 {
		return fmt.Errorf("no tokens produced")
	}

	depth := 0
	for _, t := range tokens {
		switch t.kind {
		case "punct":
			switch t.text {
			case "(":
				depth++
			case ")":
				depth--
				if depth < 0 {
					return fmt.Errorf("unbalanced parentheses: unexpected ')'")
				}
			case ";":
				return fmt.Errorf("statement terminator ';' is not allowed")
			}
		case "ident":
			if !validIdentifier(t.text) {
				return fmt.Errorf("quoted identifier %q fails structural check", t.text)
			}
		case "word":
			upper := strings.ToUpper(t.text)
			if bannedKeywords[upper] {
				return fmt.Errorf("keyword %q is not allowed in a read-only query", upper)
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("unbalanced parentheses: %d unclosed", depth)
	}
	return nil
}
