package macroexp

func isAlpha(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }

// Lex tokenizes src. Every token's DefPos is initialized to its own
// position: source tokens are their own definition site.
func Lex(file, src string) []Token {
	var toks []Token
	line, col := 1, 1
	i := 0
	advance := func() {
		if src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
		i++
	}
	for i < len(src) {
		c := src[i]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			advance()
			continue
		}
		if c == '/' && i+1 < len(src) && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				advance()
			}
			continue
		}
		start := Pos{File: file, Line: line, Col: col}
		switch {
		case isAlpha(c):
			j := i
			for i < len(src) && isAlnum(src[i]) {
				advance()
			}
			toks = append(toks, Token{Kind: Ident, Text: src[j:i], Pos: start, DefPos: start})
		case isDigit(c):
			j := i
			for i < len(src) && (isAlnum(src[i]) || src[i] == '.') {
				advance()
			}
			toks = append(toks, Token{Kind: Number, Text: src[j:i], Pos: start, DefPos: start})
		case c == '"':
			advance()
			j := i
			for i < len(src) && src[i] != '"' {
				if src[i] == '\\' && i+1 < len(src) {
					advance()
				}
				advance()
			}
			text := src[j:i]
			if i < len(src) {
				advance() // closing quote
			}
			toks = append(toks, Token{Kind: Str, Text: text, Pos: start, DefPos: start})
		case c == '#':
			advance()
			toks = append(toks, Token{Kind: Hash, Text: "#", Pos: start, DefPos: start})
		default:
			advance()
			toks = append(toks, Token{Kind: Punct, Text: string(c), Pos: start, DefPos: start})
		}
	}
	return toks
}
