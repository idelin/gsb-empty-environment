package scim

import "fmt"

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokIdent
	tokString
	tokNumber
	tokDot
	tokLParen
	tokRParen
	tokLBracket
	tokRBracket
)

func (k tokenKind) String() string {
	switch k {
	case tokEOF:
		return "EOF"
	case tokIdent:
		return "identifier"
	case tokString:
		return "string"
	case tokNumber:
		return "number"
	case tokDot:
		return "'.'"
	case tokLParen:
		return "'('"
	case tokRParen:
		return "')'"
	case tokLBracket:
		return "'['"
	case tokRBracket:
		return "']'"
	default:
		return "unknown"
	}
}

type token struct {
	kind tokenKind
	lit  string
	pos  int
}

type lexer struct {
	input string
	pos   int
}

// lexSCIM 把完整输入切分为 token。属性路径中的 '.' 与 ':' 会被并入
// 同一个 identifier token（URN 与点分子属性）；独立的 '.'（例如 PATCH
// 路径 members[value eq "x"].displayName 中括号后的点）则产生 tokDot。
func lexSCIM(input string) ([]token, error) {
	l := &lexer{input: input}
	var toks []token
	for {
		t, err := l.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, t)
		if t.kind == tokEOF {
			return toks, nil
		}
	}
}

func (l *lexer) next() (token, error) {
	l.skipWhitespace()
	start := l.pos
	if start >= len(l.input) {
		return token{kind: tokEOF, pos: start}, nil
	}
	ch := l.input[start]

	switch ch {
	case '(':
		l.pos++
		return token{kind: tokLParen, lit: "(", pos: start}, nil
	case ')':
		l.pos++
		return token{kind: tokRParen, lit: ")", pos: start}, nil
	case '[':
		l.pos++
		return token{kind: tokLBracket, lit: "[", pos: start}, nil
	case ']':
		l.pos++
		return token{kind: tokRBracket, lit: "]", pos: start}, nil
	case '.':
		l.pos++
		return token{kind: tokDot, lit: ".", pos: start}, nil
	case '"':
		end, err := scanString(l.input, start)
		if err != nil {
			return token{}, err
		}
		l.pos = end
		return token{kind: tokString, lit: l.input[start:end], pos: start}, nil
	}

	if isAlpha(ch) {
		return l.scanIdent(start), nil
	}
	if isDigit(ch) || (ch == '-' && start+1 < len(l.input) && isDigit(l.input[start+1])) {
		end, err := scanNumber(l.input, start)
		if err != nil {
			return token{}, &LexError{Pos: start, Msg: err.Error()}
		}
		l.pos = end
		return token{kind: tokNumber, lit: l.input[start:end], pos: start}, nil
	}

	return token{}, &LexError{Pos: start, Msg: fmt.Sprintf("unexpected character %q", string(ch))}
}

func (l *lexer) skipWhitespace() {
	for l.pos < len(l.input) {
		switch l.input[l.pos] {
		case ' ', '\t', '\r', '\n':
			l.pos++
		default:
			return
		}
	}
}

func (l *lexer) scanIdent(start int) token {
	i := start
	for i < len(l.input) {
		ch := l.input[i]
		if isNameChar(ch) || ch == '.' || ch == ':' {
			i++
			continue
		}
		break
	}
	l.pos = i
	return token{kind: tokIdent, lit: l.input[start:i], pos: start}
}

func isAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func isDigit(ch byte) bool {
	return ch >= '0' && ch <= '9'
}

func isNameChar(ch byte) bool {
	return isAlpha(ch) || isDigit(ch) || ch == '_' || ch == '-'
}

// scanString 返回闭合引号之后的位置，严格校验 JSON 字符串转义。
func scanString(s string, start int) (int, error) {
	i := start + 1
	for i < len(s) {
		ch := s[i]
		if ch == '"' {
			return i + 1, nil
		}
		if ch < 0x20 {
			return 0, &LexError{Pos: i, Msg: "unescaped control character in string"}
		}
		if ch == '\\' {
			if i+1 >= len(s) {
				return 0, &LexError{Pos: i, Msg: "unterminated escape sequence"}
			}
			esc := s[i+1]
			switch esc {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
				continue
			case 'u':
				if i+5 >= len(s) {
					return 0, &LexError{Pos: i, Msg: "incomplete unicode escape"}
				}
				for j := i + 2; j < i+6; j++ {
					if !isHex(s[j]) {
						return 0, &LexError{Pos: j, Msg: "invalid hex digit in unicode escape"}
					}
				}
				i += 6
				continue
			default:
				return 0, &LexError{Pos: i + 1, Msg: fmt.Sprintf("invalid escape sequence \\%c", esc)}
			}
		}
		i++
	}
	return 0, &LexError{Pos: start, Msg: "unterminated string literal"}
}

func isHex(ch byte) bool {
	return isDigit(ch) || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

// scanNumber 按 JSON number 文法扫描：-?(0|[1-9]digits)(.digits)?([eE][+-]?digits)?
func scanNumber(s string, start int) (int, error) {
	i := start
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i >= len(s) {
		return 0, fmt.Errorf("malformed number")
	}
	if s[i] == '0' {
		i++
	} else if isDigit(s[i]) {
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	} else {
		return 0, fmt.Errorf("malformed number")
	}
	if i < len(s) && s[i] == '.' {
		i++
		if i >= len(s) || !isDigit(s[i]) {
			return 0, fmt.Errorf("digits required after decimal point")
		}
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || !isDigit(s[i]) {
			return 0, fmt.Errorf("digits required in exponent")
		}
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}
	return i, nil
}
