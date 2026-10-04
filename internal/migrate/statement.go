package migrate

import (
	"strings"
	"unicode"
)

// Statement 表示源文件中的一条逻辑语句（一个非空行），
// 附带用于结构匹配的签名信息。
type Statement struct {
	Index  int    // 在语句序列中的下标（0 起）
	Line   int    // 在源文件中的行号（1 起）
	Raw    string // 原始行文本
	Norm   string // 规范化文本：去首尾空白、压缩内部空白
	Indent int    // 前导空白宽度（tab 计 4）
	Kind   string // 语句类别（关键字启发式）
}

// ParseStatements 把源文本切分为语句序列，跳过空行。
func ParseStatements(src string) []Statement {
	lines := strings.Split(src, "\n")
	stmts := make([]Statement, 0, len(lines))
	for i, raw := range lines {
		norm := normalize(raw)
		if norm == "" {
			continue
		}
		stmts = append(stmts, Statement{
			Index:  len(stmts),
			Line:   i + 1,
			Raw:    raw,
			Norm:   norm,
			Indent: indentWidth(raw),
			Kind:   classify(norm),
		})
	}
	return stmts
}

func normalize(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func indentWidth(s string) int {
	w := 0
	for _, r := range s {
		switch r {
		case ' ':
			w++
		case '\t':
			w += 4
		default:
			return w
		}
	}
	return w
}

var kindKeywords = []string{
	"package", "import", "func", "if", "else", "for", "switch",
	"case", "default", "return", "var", "const", "type", "go",
	"defer", "select", "range", "struct", "interface", "map",
	"break", "continue", "goto", "fallthrough",
}

func classify(norm string) string {
	first := norm
	if i := strings.IndexAny(norm, " \t({"); i >= 0 {
		first = norm[:i]
	}
	for _, kw := range kindKeywords {
		if first == kw {
			return kw
		}
	}
	switch {
	case strings.HasPrefix(norm, "}"):
		return "block-close"
	case strings.HasSuffix(norm, "{"):
		return "block-open"
	case strings.Contains(norm, "="):
		return "assign"
	case strings.HasPrefix(norm, "//"):
		return "comment"
	default:
		return "expr"
	}
}

// stripSpace 移除全部空白，用于跨行拼接比较（语句拆分场景）。
func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}
