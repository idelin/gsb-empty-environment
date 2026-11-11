package sqlifinger

import "strings"

// 词类编码：把 token 折叠成单字符类别，抹掉具体取值，
// 使大小写拼接、换值、换列名等绕过手段得到相同指纹。
//
//	1  数字 / 十六进制 / 二进制字面量，以及 TRUE、FALSE
//	s  字符串字面量
//	v  标识符、@变量
//	n  反引号标识符
//	k  SQL 关键字
//	U  UNION 关键字
//	&  逻辑连接：AND、OR、XOR、&&、||
//	f  函数调用（标识符紧跟左括号）
//	o  其他运算符
//	( ) , ;  标点原样保留
func classOf(t Token) byte {
	switch t.Type {
	case TokenKeyword:
		switch strings.ToUpper(t.Value) {
		case "AND", "OR", "XOR":
			return '&'
		case "UNION":
			return 'U'
		case "TRUE", "FALSE":
			return '1'
		}
		return 'k'
	case TokenFunction:
		return 'f'
	case TokenIdentifier, TokenVariable:
		return 'v'
	case TokenQuotedIdentifier:
		return 'n'
	case TokenNumber:
		return '1'
	case TokenString:
		return 's'
	case TokenOperator:
		if t.Value == "&&" || t.Value == "||" {
			return '&'
		}
		return 'o'
	case TokenPunctuation:
		return t.Value[0]
	}
	return '?'
}

// Fold 把 token 序列折叠成词类字符串。
func Fold(tokens []Token) string {
	var b strings.Builder
	b.Grow(len(tokens))
	for _, t := range tokens {
		b.WriteByte(classOf(t))
	}
	return b.String()
}
