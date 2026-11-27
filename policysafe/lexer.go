package policysafe

import "fmt"

type tokenKind int

const (
	tkEOF tokenKind = iota
	tkIdent
	tkVar
	tkString
	tkNumber
	tkLParen
	tkRParen
	tkComma
	tkDot
	tkColonDash
	tkColonEq
)

type token struct {
	kind tokenKind
	text string
	line int
	col  int
}

type lexer struct {
	src    string
	pos    int
	line   int
	col    int
	tokens []token
}

func tokenize(src string) ([]token, error) {
	l := &lexer{src: src, line: 1, col: 1}
	if err := l.run(); err != nil {
		return nil, err
	}
	return l.tokens, nil
}

func (l *lexer) run() error {
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		startLine, startCol := l.line, l.col
		switch {
		case ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n':
			l.advance()
			continue
		case ch == '#':
			l.skipLineComment()
			continue
		case ch == '/' && l.peek(1) == '/':
			l.skipLineComment()
			continue
		case ch == '(':
			l.push(tkLParen, "(", startLine, startCol)
			l.advance()
		case ch == ')':
			l.push(tkRParen, ")", startLine, startCol)
			l.advance()
		case ch == ',':
			l.push(tkComma, ",", startLine, startCol)
			l.advance()
		case ch == '.':
			l.push(tkDot, ".", startLine, startCol)
			l.advance()
		case ch == ':':
			switch {
			case l.peek(1) == '-':
				l.push(tkColonDash, ":-", startLine, startCol)
				l.advance()
				l.advance()
			case l.peek(1) == '=':
				l.push(tkColonEq, ":=", startLine, startCol)
				l.advance()
				l.advance()
			default:
				return l.errorf(startLine, startCol, "unexpected ':'")
			}
		case ch == '"':
			value, err := l.readString()
			if err != nil {
				return err
			}
			l.tokens = append(l.tokens, token{tkString, value, startLine, startCol})
		case isDigit(ch):
			text := l.readWord(isWordChar)
			l.tokens = append(l.tokens, token{tkNumber, text, startLine, startCol})
		case ch == '_':
			text := l.readWord(isWordChar)
			l.tokens = append(l.tokens, token{tkVar, text, startLine, startCol})
		case isLower(ch):
			text := l.readWord(isWordChar)
			l.tokens = append(l.tokens, token{tkIdent, text, startLine, startCol})
		case isUpper(ch):
			text := l.readWord(isWordChar)
			l.tokens = append(l.tokens, token{tkVar, text, startLine, startCol})
		default:
			return l.errorf(startLine, startCol, "unexpected character %q", ch)
		}
	}
	l.tokens = append(l.tokens, token{kind: tkEOF, line: l.line, col: l.col})
	return nil
}

func (l *lexer) push(kind tokenKind, text string, line, col int) {
	l.tokens = append(l.tokens, token{kind: kind, text: text, line: line, col: col})
}

func (l *lexer) advance() {
	if l.pos < len(l.src) && l.src[l.pos] == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	l.pos++
}

func (l *lexer) peek(offset int) byte {
	idx := l.pos + offset
	if idx >= len(l.src) {
		return 0
	}
	return l.src[idx]
}

func (l *lexer) skipLineComment() {
	for l.pos < len(l.src) && l.src[l.pos] != '\n' {
		l.advance()
	}
}

func (l *lexer) readWord(keep func(byte) bool) string {
	start := l.pos
	for l.pos < len(l.src) && keep(l.src[l.pos]) {
		l.advance()
	}
	return l.src[start:l.pos]
}

func (l *lexer) readString() (string, error) {
	line, col := l.line, l.col
	l.advance() // opening quote
	var b []byte
	for l.pos < len(l.src) && l.src[l.pos] != '"' {
		ch := l.src[l.pos]
		if ch == '\n' {
			return "", l.errorf(line, col, "unterminated string literal")
		}
		if ch != '\\' {
			b = append(b, ch)
			l.advance()
			continue
		}
		l.advance()
		if l.pos >= len(l.src) {
			break
		}
		esc := l.src[l.pos]
		switch esc {
		case 'n':
			b = append(b, '\n')
		case 't':
			b = append(b, '\t')
		case 'r':
			b = append(b, '\r')
		case '"':
			b = append(b, '"')
		case '\\':
			b = append(b, '\\')
		default:
			return "", l.errorf(l.line, l.col, "unknown escape sequence \\%c", esc)
		}
		l.advance()
	}
	if l.pos >= len(l.src) {
		return "", l.errorf(line, col, "unterminated string literal")
	}
	l.advance() // closing quote
	return string(b), nil
}

func (l *lexer) errorf(line, col int, format string, args ...any) error {
	return fmt.Errorf("policysafe: line %d col %d: %s", line, col, fmt.Sprintf(format, args...))
}

func isDigit(ch byte) bool { return ch >= '0' && ch <= '9' }

func isLower(ch byte) bool { return ch >= 'a' && ch <= 'z' }

func isUpper(ch byte) bool { return ch >= 'A' && ch <= 'Z' }

func isWordChar(ch byte) bool {
	return isLower(ch) || isUpper(ch) || isDigit(ch) || ch == '_'
}
