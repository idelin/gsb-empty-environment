package sqlifinger

import "strings"

// TokenType 表示 MySQL 方言词法单元的类型。
type TokenType int

const (
	TokenKeyword TokenType = iota
	TokenIdentifier
	TokenQuotedIdentifier // 反引号标识符
	TokenNumber           // 含十六进制 0x../x'..' 与二进制 b'..' 字面量
	TokenString
	TokenOperator
	TokenPunctuation // ( ) , ;
	TokenVariable    // @var / @@var
	TokenFunction    // 紧跟 '(' 的标识符
)

// Token 是一个词法单元，Value 为原始文本（未解码转义）。
type Token struct {
	Type  TokenType
	Value string
}

// Context 表示参数值被拼入 SQL 时所处的起始上下文。
type Context int

const (
	ContextUnquoted    Context = iota // 不在引号内（数值位、标识符位等）
	ContextSingleQuote                // 处在单引号字符串内
	ContextDoubleQuote                // 处在双引号字符串内
)

func (c Context) String() string {
	switch c {
	case ContextSingleQuote:
		return "single-quote"
	case ContextDoubleQuote:
		return "double-quote"
	default:
		return "unquoted"
	}
}

// MySQL 结构关键字。函数名（SLEEP、VERSION 等）刻意不收录，
// 使其折叠为函数词类 f，便于用 "&f(" 之类的指纹捕获。
var sqlKeywords = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"DROP": true, "CREATE": true, "ALTER": true, "TRUNCATE": true,
	"REPLACE": true, "UNION": true, "ALL": true, "DISTINCT": true,
	"FROM": true, "WHERE": true, "AND": true, "OR": true, "XOR": true,
	"NOT": true, "INTO": true, "VALUES": true, "SET": true,
	"TABLE": true, "TABLES": true, "DATABASE": true, "SCHEMA": true,
	"GROUP": true, "ORDER": true, "BY": true, "HAVING": true,
	"LIMIT": true, "OFFSET": true, "JOIN": true, "INNER": true,
	"LEFT": true, "RIGHT": true, "OUTER": true, "CROSS": true,
	"STRAIGHT_JOIN": true, "ON": true, "USING": true, "AS": true,
	"ASC": true, "DESC": true, "CASE": true, "WHEN": true,
	"THEN": true, "ELSE": true, "END": true, "IF": true,
	"EXISTS": true, "IN": true, "IS": true, "NULL": true,
	"TRUE": true, "FALSE": true, "LIKE": true, "RLIKE": true,
	"REGEXP": true, "BETWEEN": true, "DIV": true, "MOD": true,
	"LOCK": true, "UNLOCK": true, "GRANT": true, "REVOKE": true,
	"USE": true, "SHOW": true, "DESCRIBE": true, "EXPLAIN": true,
	"DECLARE": true, "PROCEDURE": true, "TRIGGER": true, "VIEW": true,
	"INDEX": true, "KEY": true, "PRIMARY": true, "FOREIGN": true,
	"REFERENCES": true, "CONSTRAINT": true, "DEFAULT": true,
	"CHARACTER": true, "COLLATE": true, "ENGINE": true, "PARTITION": true,
	"OUTFILE": true, "DUMPFILE": true, "LOAD": true, "DATA": true,
	"INFILE": true, "CASCADE": true, "TEMPORARY": true, "SESSION": true,
	"GLOBAL": true, "LOCAL": true, "TRANSACTION": true, "COMMIT": true,
	"ROLLBACK": true, "SAVEPOINT": true, "BEGIN": true, "START": true,
	"WITH": true, "DO": true, "HANDLER": true, "CALL": true,
	"PREPARE": true, "EXECUTE": true, "DEALLOCATE": true, "KILL": true,
	"ANALYZE": true, "OPTIMIZE": true, "CHECK": true, "REPAIR": true,
	"FLUSH": true, "RESET": true, "PURGE": true, "USER": true,
	"PASSWORD": true, "IDENTIFIED": true, "SEPARATOR": true,
	"HIGH_PRIORITY": true, "LOW_PRIORITY": true, "DELAYED": true,
	"IGNORE": true, "SQL_NO_CACHE": true, "SQL_CACHE": true,
	"SQL_BIG_RESULT": true, "SQL_SMALL_RESULT": true, "DUAL": true,
	"NATURAL": true, "INTERVAL": true, "CURRENT_USER": true,
	"CURRENT_DATE": true, "CURRENT_TIME": true, "CURRENT_TIMESTAMP": true,
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isWordChar(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') || isDigit(c) || c >= 0x80
}

func isPunct(c byte) bool {
	return c == '(' || c == ')' || c == ',' || c == ';'
}

type lexer struct {
	src    string
	pos    int
	tokens []Token
}

// Tokenize 按 MySQL 方言把 input 切成词法单元序列。
// 空白与注释（-- 、#、/* */）被消费但不产生 token；
// 版本注释 /*! ... */ 的内容在 MySQL 中会被执行，因此递归切词保留。
func Tokenize(input string, ctx Context) []Token {
	lx := &lexer{src: input}
	switch ctx {
	case ContextSingleQuote:
		lx.scanString('\'')
	case ContextDoubleQuote:
		lx.scanString('"')
	}
	for lx.pos < len(lx.src) {
		lx.scanToken()
	}
	return lx.tokens
}

func (lx *lexer) emit(t TokenType, value string) {
	lx.tokens = append(lx.tokens, Token{Type: t, Value: value})
}

// scanString 从 lx.pos（字符串内容起点，开引号已消费）扫描到配对的闭引号。
// 识别反斜杠转义与引号双写转义；未闭合则吞掉剩余全部输入。
func (lx *lexer) scanString(q byte) {
	i := lx.pos
	for i < len(lx.src) {
		c := lx.src[i]
		if c == '\\' && i+1 < len(lx.src) {
			i += 2
			continue
		}
		if c == q {
			if i+1 < len(lx.src) && lx.src[i+1] == q {
				i += 2
				continue
			}
			lx.emit(TokenString, lx.src[lx.pos:i])
			lx.pos = i + 1
			return
		}
		i++
	}
	lx.emit(TokenString, lx.src[lx.pos:])
	lx.pos = len(lx.src)
}

func (lx *lexer) scanBacktick() {
	i := lx.pos + 1
	for i < len(lx.src) {
		if lx.src[i] == '`' {
			if i+1 < len(lx.src) && lx.src[i+1] == '`' {
				i += 2
				continue
			}
			lx.emit(TokenQuotedIdentifier, lx.src[lx.pos+1:i])
			lx.pos = i + 1
			return
		}
		i++
	}
	lx.emit(TokenQuotedIdentifier, lx.src[lx.pos+1:])
	lx.pos = len(lx.src)
}

func (lx *lexer) skipToEOL() {
	for lx.pos < len(lx.src) && lx.src[lx.pos] != '\n' {
		lx.pos++
	}
}

// scanComment 处理 /* ... */ 与 /*! ... */。
// 版本注释内容会被 MySQL 执行，故对其递归切词。
func (lx *lexer) scanComment() {
	start := lx.pos + 2
	versioned := start < len(lx.src) && lx.src[start] == '!'
	content := start
	if versioned {
		content++
		for content < len(lx.src) && isDigit(lx.src[content]) {
			content++
		}
	}
	closeIdx := strings.Index(lx.src[content:], "*/")
	end := len(lx.src)
	if closeIdx >= 0 {
		end = content + closeIdx
	}
	if versioned {
		lx.tokens = append(lx.tokens, Tokenize(lx.src[content:end], ContextUnquoted)...)
	}
	if closeIdx >= 0 {
		lx.pos = end + 2
	} else {
		lx.pos = len(lx.src)
	}
}

func (lx *lexer) scanNumber() {
	i := lx.pos
	if lx.src[i] == '0' && i+2 < len(lx.src) &&
		(lx.src[i+1] == 'x' || lx.src[i+1] == 'X') && isHexDigit(lx.src[i+2]) {
		i += 2
		for i < len(lx.src) && isHexDigit(lx.src[i]) {
			i++
		}
		lx.emit(TokenNumber, lx.src[lx.pos:i])
		lx.pos = i
		return
	}
	if lx.src[i] == '.' {
		i++
	}
	for i < len(lx.src) && isDigit(lx.src[i]) {
		i++
	}
	if i < len(lx.src) && lx.src[i] == '.' && i+1 < len(lx.src) && isDigit(lx.src[i+1]) {
		i++
		for i < len(lx.src) && isDigit(lx.src[i]) {
			i++
		}
	}
	if i < len(lx.src) && (lx.src[i] == 'e' || lx.src[i] == 'E') {
		j := i + 1
		if j < len(lx.src) && (lx.src[j] == '+' || lx.src[j] == '-') {
			j++
		}
		if j < len(lx.src) && isDigit(lx.src[j]) {
			for j < len(lx.src) && isDigit(lx.src[j]) {
				j++
			}
			i = j
		}
	}
	lx.emit(TokenNumber, lx.src[lx.pos:i])
	lx.pos = i
}

func (lx *lexer) scanWord() {
	i := lx.pos
	for i < len(lx.src) && isWordChar(lx.src[i]) {
		i++
	}
	word := lx.src[lx.pos:i]
	upper := strings.ToUpper(word)
	// 十六进制 / 二进制字面量：x'1F'、X'1F'、b'01'、B'01'
	if (upper == "X" || upper == "B") && i < len(lx.src) && lx.src[i] == '\'' {
		lx.pos = i + 1
		lx.scanString('\'')
		lx.tokens[len(lx.tokens)-1].Type = TokenNumber
		return
	}
	lx.pos = i
	if sqlKeywords[upper] {
		lx.emit(TokenKeyword, word)
		return
	}
	j := i
	for j < len(lx.src) && isSpace(lx.src[j]) {
		j++
	}
	if j < len(lx.src) && lx.src[j] == '(' {
		lx.emit(TokenFunction, word)
		return
	}
	lx.emit(TokenIdentifier, word)
}

func (lx *lexer) scanVariable() {
	i := lx.pos + 1
	if i < len(lx.src) && lx.src[i] == '@' {
		i++
	}
	if i < len(lx.src) && (lx.src[i] == '\'' || lx.src[i] == '"' || lx.src[i] == '`') {
		q := lx.src[i]
		i++
		for i < len(lx.src) && lx.src[i] != q {
			i++
		}
		if i < len(lx.src) {
			i++
		}
	} else {
		for i < len(lx.src) && isWordChar(lx.src[i]) {
			i++
		}
	}
	lx.emit(TokenVariable, lx.src[lx.pos:i])
	lx.pos = i
}

var multiOps = []string{"<=>", "<=", ">=", "<>", "!=", "&&", "||", "<<", ">>", ":="}

func (lx *lexer) scanToken() {
	c := lx.src[lx.pos]
	switch {
	case isSpace(c):
		lx.pos++
	case c == '#':
		lx.skipToEOL()
	case c == '-' && lx.pos+1 < len(lx.src) && lx.src[lx.pos+1] == '-' &&
		(lx.pos+2 == len(lx.src) || isSpace(lx.src[lx.pos+2])):
		// MySQL 要求 -- 后跟空白才算注释
		lx.skipToEOL()
	case c == '/' && lx.pos+1 < len(lx.src) && lx.src[lx.pos+1] == '*':
		lx.scanComment()
	case c == '\'':
		lx.pos++
		lx.scanString('\'')
	case c == '"':
		lx.pos++
		lx.scanString('"')
	case c == '`':
		lx.scanBacktick()
	case c == '@':
		lx.scanVariable()
	case isDigit(c) || (c == '.' && lx.pos+1 < len(lx.src) && isDigit(lx.src[lx.pos+1])):
		lx.scanNumber()
	case isWordChar(c):
		lx.scanWord()
	case isPunct(c):
		lx.emit(TokenPunctuation, string(c))
		lx.pos++
	default:
		for _, op := range multiOps {
			if strings.HasPrefix(lx.src[lx.pos:], op) {
				lx.emit(TokenOperator, op)
				lx.pos += len(op)
				return
			}
		}
		lx.emit(TokenOperator, string(c))
		lx.pos++
	}
}
