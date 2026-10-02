// Package logtmpl implements explainable online log-template clustering.
//
// Tokenization follows explicit, declared rules (see Classify). Variable
// numbers and IDs are abstracted into typed wildcards; error codes keep
// their family (e.g. ERR vs E) and are never generalized into plain
// numbers. See README.md for the full policy declarations.
package logtmpl

import (
	"regexp"
	"strings"
	"unicode"
)

// TokenType is the declared, public classification of a token.
type TokenType int

const (
	TokWord TokenType = iota
	TokNumber
	TokErrorCode
	TokID
	TokPunct
)

func (t TokenType) String() string {
	switch t {
	case TokWord:
		return "WORD"
	case TokNumber:
		return "NUM"
	case TokErrorCode:
		return "ERR"
	case TokID:
		return "ID"
	case TokPunct:
		return "PUNCT"
	}
	return "?"
}

// Token is a lexical unit of a log line.
type Token struct {
	Type TokenType
	Text string
}

// Explicit token rules. Order matters: first match wins in Classify.
var (
	// Plain integers and decimals: 200, 42, 3.14
	reNumber = regexp.MustCompile(`^\d+(\.\d+)?$`)
	// Error codes, rule A: err/error/e followed by >=2 digits, optional
	// separator: ERR-503, error_42, E1234. Case-insensitive.
	reErrCodeA = regexp.MustCompile(`^(?i)(?:err(?:or)?|e)[-_]?\d{2,}$`)
	// Error codes, rule B: >=2 UPPERCASE letters, separator, >=2 digits:
	// DB-1042, AUTH_77. Lowercase words like worker-1 are NOT error codes.
	reErrCodeB = regexp.MustCompile(`^[A-Z]{2,}[-_]\d{2,}$`)
	// UUID-like: 8 hex chars followed by >=2 dash-separated hex groups.
	reUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4,}){2,}$`)
	// Long hex (>=12 chars). Pure-digit strings already matched TokNumber.
	reHexID = regexp.MustCompile(`^[0-9a-fA-F]{12,}$`)
)

// Classify applies the explicit token rules to one token text.
func Classify(text string) TokenType {
	switch {
	case reNumber.MatchString(text):
		return TokNumber
	case reErrCodeA.MatchString(text), reErrCodeB.MatchString(text):
		return TokErrorCode
	case reUUID.MatchString(text):
		return TokID
	case reHexID.MatchString(text):
		return TokID
	default:
		return TokWord
	}
}

// ErrFamily extracts the declared error-code family: the leading letters,
// uppercased, with ERROR normalized to ERR. ERR-503 -> ERR, E900 -> E,
// DB-1042 -> DB. Distinct families must never merge into one wildcard.
func ErrFamily(text string) string {
	i := 0
	for i < len(text) && (text[i] >= 'A' && text[i] <= 'Z' || text[i] >= 'a' && text[i] <= 'z') {
		i++
	}
	fam := strings.ToUpper(text[:i])
	if fam == "ERROR" {
		return "ERR"
	}
	return fam
}

func isTokenRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) ||
		r == '-' || r == '_' || r == '.' || r == '/' || r == ':'
}

// Tokenize splits a log line into tokens. Whitespace separates; characters
// outside the token alphabet (e.g. '=', ',', '(') become single PUNCT
// tokens, which are always literal in templates and never generalized.
func Tokenize(line string) []Token {
	var toks []Token
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			s := cur.String()
			toks = append(toks, Token{Type: Classify(s), Text: s})
			cur.Reset()
		}
	}
	for _, r := range line {
		switch {
		case unicode.IsSpace(r):
			flush()
		case isTokenRune(r):
			cur.WriteRune(r)
		default:
			flush()
			toks = append(toks, Token{Type: TokPunct, Text: string(r)})
		}
	}
	flush()
	return toks
}
