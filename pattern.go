package netsec

import "strings"

const (
	backslashByte = 0x5c // 反斜杠：转义下一个字符
	dotByte       = 0x2e // 点号：匹配任意单字符
	starByte      = 0x2a // 星号：重复前一个 token
)

// token 是 pathPattern 编译后的一个匹配单元：一个字面字节或
// 通配点号，可选地被星号修饰表示重复零次或多次。
type token struct {
	any     bool // 是否为点号通配（匹配任意单字符）
	literal byte // any 为 false 时的字面字节
	star    bool // 是否被星号修饰
}

func (t token) matches(c byte) bool {
	return t.any || t.literal == c
}

// compilePattern 把 pathPattern 编译为 token 序列。
// 反斜杠转义下一个字符（包括点号、星号与反斜杠本身）；
// 星号修饰紧邻的前一个 token，若前面没有可修饰的 token 则按字面星号处理。
func compilePattern(pattern string) []token {
	var tokens []token
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case backslashByte:
			if i+1 < len(pattern) {
				i++
				tokens = append(tokens, token{literal: pattern[i]})
			} else {
				// 末尾孤立的反斜杠按字面处理。
				tokens = append(tokens, token{literal: backslashByte})
			}
		case dotByte:
			tokens = append(tokens, token{any: true})
		case starByte:
			if n := len(tokens); n > 0 && !tokens[n-1].star {
				tokens[n-1].star = true
			} else {
				tokens = append(tokens, token{literal: starByte})
			}
		default:
			tokens = append(tokens, token{literal: c})
		}
	}
	return tokens
}

// MatchPathPattern 按简化的 pathPattern 语义匹配整个路径：
// 点号匹配任意单字符，星号表示前一个字符（或点号）重复零次或多次，
// 反斜杠转义下一个字符，其余字符按字面匹配。匹配必须覆盖整个路径。
func MatchPathPattern(pattern, path string) bool {
	return matchTokens(compilePattern(pattern), path)
}

func matchTokens(tokens []token, s string) bool {
	if len(tokens) == 0 {
		return len(s) == 0
	}
	head := tokens[0]
	rest := tokens[1:]
	if !head.star {
		return len(s) > 0 && head.matches(s[0]) && matchTokens(rest, s[1:])
	}
	// 星号：依次尝试消耗 0 到 k 个匹配字符。
	for i := 0; ; i++ {
		if matchTokens(rest, s[i:]) {
			return true
		}
		if i >= len(s) || !head.matches(s[i]) {
			return false
		}
	}
}

// matchPathSpec 按 data 元素上声明的某一种路径属性匹配路径。
// path 为全等比较，pathPrefix 为前缀比较，pathSuffix 为后缀比较，
// pathPattern 使用 MatchPathPattern 的简化语义。
func matchPathSpec(spec pathSpec, path string) bool {
	switch spec.kind {
	case pathExact:
		return spec.value == path
	case pathPrefix:
		return strings.HasPrefix(path, spec.value)
	case pathSuffix:
		return strings.HasSuffix(path, spec.value)
	case pathPattern:
		return MatchPathPattern(spec.value, path)
	}
	return false
}
