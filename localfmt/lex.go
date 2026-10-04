package localfmt

import "fmt"

type TokKind int

const (
	TNum TokKind = iota
	TIdent
	TOp // + - * /
	TLParen
	TRParen
	TComma
	TComment
	TEOF
)

type Token struct {
	Kind       TokKind
	Text       string
	Start, End int // byte offsets into source, [Start, End)
}

func (t Token) String() string {
	return fmt.Sprintf("%d:%q@[%d,%d)", t.Kind, t.Text, t.Start, t.End)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// Lex tokenizes src. Whitespace is skipped; comments become TComment tokens
// so they keep their attachment to surrounding nodes.
func Lex(src string) ([]Token, error) {
	var toks []Token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case isSpace(c):
			i++
		case isDigit(c) || (c == '.' && i+1 < len(src) && isDigit(src[i+1])):
			j := i
			for j < len(src) && (isDigit(src[j]) || src[j] == '.') {
				j++
			}
			toks = append(toks, Token{TNum, src[i:j], i, j})
			i = j
		case isAlpha(c):
			j := i
			for j < len(src) && (isAlpha(src[j]) || isDigit(src[j])) {
				j++
			}
			toks = append(toks, Token{TIdent, src[i:j], i, j})
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/':
			if c == '/' && i+1 < len(src) && src[i+1] == '/' {
				j := i
				for j < len(src) && src[j] != '\n' {
					j++
				}
				toks = append(toks, Token{TComment, src[i:j], i, j})
				i = j
				break
			}
			if c == '/' && i+1 < len(src) && src[i+1] == '*' {
				j := i + 2
				for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
					j++
				}
				if j+1 >= len(src) {
					return nil, fmt.Errorf("unterminated block comment at %d", i)
				}
				j += 2
				toks = append(toks, Token{TComment, src[i:j], i, j})
				i = j
				break
			}
			toks = append(toks, Token{TOp, src[i : i+1], i, i + 1})
			i++
		case c == '(':
			toks = append(toks, Token{TLParen, "(", i, i + 1})
			i++
		case c == ')':
			toks = append(toks, Token{TRParen, ")", i, i + 1})
			i++
		case c == ',':
			toks = append(toks, Token{TComma, ",", i, i + 1})
			i++
		default:
			return nil, fmt.Errorf("unexpected byte %q at %d", c, i)
		}
	}
	toks = append(toks, Token{TEOF, "", len(src), len(src)})
	return toks, nil
}
