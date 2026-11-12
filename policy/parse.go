package policy

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rule is one accepted policy entry.
type Rule struct {
	Line   int
	Action string
	Value  string
}

// Policy is the runtime-side view of a policy file. Hash is computed from the
// normalized bytes, not from the parsed rules or the original BOM-bearing file.
type Policy struct {
	Hash  [32]byte
	Rules []Rule
}

// DigestHex returns the SHA-256 of the normalized bytes.
func (p *Policy) DigestHex() string {
	return hex.EncodeToString(p.Hash[:])
}

// ParseError reports a malformed logical line.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("policy: line %d: %s", e.Line, e.Msg)
}

// Parse is the runtime entry point. It deliberately starts from Lex so BOM
// normalization, line boundaries, and the digest are identical to the verifier.
func Parse(src []byte) (*Policy, error) {
	doc, err := Lex(src)
	if err != nil {
		return nil, err
	}

	var rules []Rule
	for _, line := range doc.Lines {
		text := strings.TrimSpace(line.Text)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		key, value, err := splitRule(text)
		if err != nil {
			return nil, &ParseError{Line: line.Number, Msg: err.Error()}
		}
		switch key {
		case "allow", "deny":
		default:
			return nil, &ParseError{Line: line.Number, Msg: fmt.Sprintf("unknown key %q", key)}
		}
		if value == "" {
			return nil, &ParseError{Line: line.Number, Msg: fmt.Sprintf("key %q requires a value", key)}
		}
		rules = append(rules, Rule{Line: line.Number, Action: key, Value: value})
	}

	return &Policy{Hash: doc.Hash, Rules: rules}, nil
}

// ParseFile reads and parses a policy file.
func ParseFile(path string) (*Policy, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("policy: read %s: %w", path, err)
	}
	return Parse(src)
}

func splitRule(text string) (key, value string, err error) {
	separator := strings.IndexFunc(text, func(r rune) bool {
		return r == '=' || r == ':' || unicode.IsSpace(r)
	})
	if separator < 0 {
		return "", "", fmt.Errorf("entry %q has no value", text)
	}
	if separator == 0 {
		return "", "", fmt.Errorf("entry %q has no key", text)
	}

	key = text[:separator]
	_, width := utf8.DecodeRuneInString(text[separator:])
	value = strings.TrimSpace(text[separator+width:])
	return key, value, nil
}
