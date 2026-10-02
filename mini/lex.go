package mini

import "fmt"

type tokKind int

const (
	kEOF     tokKind = iota
	kNewline         // '\n' or ';'
	kIdent
	kInt
	kFloat
	kString
	kOp
)

type token struct {
	kind tokKind
	text string
	pos  Pos
}

func isLetter(c byte) bool { return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }
func isDigit(c byte) bool  { return '0' <= c && c <= '9' }

func lex(src string) ([]token, error) {
	var toks []token
	line, col := 1, 1
	i := 0
	emit := func(k tokKind, text string, p Pos) {
		toks = append(toks, token{kind: k, text: text, pos: p})
	}
	for i < len(src) {
		c := src[i]
		p := Pos{line, col}
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
			col++
		case c == '\n' || c == ';':
			emit(kNewline, ";", p)
			i++
			if c == '\n' {
				line++
				col = 1
			} else {
				col++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
				col++
			}
		case isLetter(c):
			start := i
			for i < len(src) && (isLetter(src[i]) || isDigit(src[i])) {
				i++
				col++
			}
			emit(kIdent, src[start:i], p)
		case isDigit(c):
			start := i
			kind := kInt
			for i < len(src) && (isDigit(src[i]) || src[i] == '.') {
				if src[i] == '.' {
					if kind == kFloat {
						return nil, fmt.Errorf("%s: malformed number", p)
					}
					kind = kFloat
				}
				i++
				col++
			}
			emit(kind, src[start:i], p)
		case c == '"':
			i++
			col++
			var text []byte
			for {
				if i >= len(src) {
					return nil, fmt.Errorf("%s: unterminated string literal", p)
				}
				if src[i] == '"' {
					i++
					col++
					break
				}
				if src[i] == '\\' && i+1 < len(src) {
					switch src[i+1] {
					case 'n':
						text = append(text, '\n')
					case 't':
						text = append(text, '\t')
					case '"':
						text = append(text, '"')
					case '\\':
						text = append(text, '\\')
					default:
						return nil, fmt.Errorf("%s: unknown escape \\%c", Pos{line, col}, src[i+1])
					}
					i += 2
					col += 2
					continue
				}
				text = append(text, src[i])
				i++
				col++
			}
			emit(kString, string(text), p)
		case i+1 < len(src) && isTwoCharOp(src[i:i+2]):
			emit(kOp, src[i:i+2], p)
			i += 2
			col += 2
		case isOneCharOp(c):
			emit(kOp, string(c), p)
			i++
			col++
		default:
			return nil, fmt.Errorf("%s: unexpected character %q", p, c)
		}
	}
	emit(kEOF, "", Pos{line, col})
	return toks, nil
}

func isTwoCharOp(s string) bool {
	switch s {
	case "==", "!=", "<=", ">=", "&&", "||":
		return true
	}
	return false
}

func isOneCharOp(c byte) bool {
	switch c {
	case '+', '-', '*', '/', '%', '(', ')', '=', '<', '>', '!':
		return true
	}
	return false
}
