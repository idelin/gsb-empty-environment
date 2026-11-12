package netsec

import (
	"strings"
	"unicode/utf8"
)

// tokenKind 区分 Sieve 词法单元，字符串内容永远不会被当成命令名。
type tokenKind int

const (
	tokIdent  tokenKind = iota // 标识符 / 数字等原子
	tokString                  // 字符串字面量（含多行文本）
	tokPunct                   // ( ) [ ] { } ; , :
)

type sieveToken struct {
	kind tokenKind
	text string
}

// tokenizeSieve 按 RFC 5228 第 2 节把脚本体切成词法单元。
// 任何无法识别或未闭合的结构都返回 ErrSieveSyntax，采取失败关闭。
func tokenizeSieve(src []byte) ([]sieveToken, error) {
	var toks []sieveToken
	i := 0
	n := len(src)
	for i < n {
		// 跳过空白（含 CR、LF、TAB）。
		if isSpace(src[i]) {
			i++
			continue
		}
		// 注释：# 行注释与 /* 块注释。
		if src[i] == '#' {
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		if src[i] == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			closed := false
			for i+1 < n {
				if src[i] == '*' && src[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, ErrSieveSyntax
			}
			continue
		}
		// 带引号字符串。
		if src[i] == '"' {
			s, next, ok := readQuotedString(src, i)
			if !ok {
				return nil, ErrSieveSyntax
			}
			toks = append(toks, sieveToken{kind: tokString, text: s})
			i = next
			continue
		}
		// 多行字符串 text:...
		if isIdentStart(src[i]) {
			start := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			word := string(src[start:i])
			if strings.EqualFold(word, "text") && i < n && src[i] == ':' {
				body, next, ok := readMultiLine(src, i+1)
				if !ok {
					return nil, ErrSieveSyntax
				}
				toks = append(toks, sieveToken{kind: tokString, text: body})
				i = next
				continue
			}
			toks = append(toks, sieveToken{kind: tokIdent, text: word})
			continue
		}
		// 数字等其它原子按标识符读取，保证结构完整即可。
		if src[i] >= '0' && src[i] <= '9' {
			start := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			toks = append(toks, sieveToken{kind: tokIdent, text: string(src[start:i])})
			continue
		}
		switch src[i] {
		case '(', ')', '[', ']', '{', '}', ';', ',', ':':
			toks = append(toks, sieveToken{kind: tokPunct, text: string(src[i])})
			i++
			continue
		}
		// 其余字节（含非法字节）一律拒绝。
		return nil, ErrSieveSyntax
	}
	return toks, nil
}

// readQuotedString 从位置 start（指向开引号）读取带引号字符串，
// 返回解码后的字符串与闭引号之后的位置。
func readQuotedString(src []byte, start int) (string, int, bool) {
	var b strings.Builder
	i := start + 1
	n := len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '"':
			return b.String(), i + 1, true
		case c == '\\':
			if i+1 >= n {
				return "", 0, false
			}
			b.WriteByte(src[i+1])
			i += 2
		case c == '\r' || c == '\n':
			return "", 0, false
		default:
			b.WriteByte(c)
			i++
		}
	}
	return "", 0, false
}

// readMultiLine 处理 "text:" 之后的多行字符串：跳过标签到 CRLF，
// 读取到单独一行的 "."，处理点填充（dot-stuffing）。
// 参数 start 指向冒号之后的字节。
func readMultiLine(src []byte, start int) (string, int, bool) {
	i := start
	n := len(src)
	// 冒号后可能有标签（如 :raw 的另一种写法），跳到该行末尾的 CRLF。
	for i < n && src[i] != '\r' && src[i] != '\n' {
		i++
	}
	if i+1 >= n || src[i] != '\r' || src[i+1] != '\n' {
		return "", 0, false
	}
	i += 2

	var b strings.Builder
	firstLine := true
	for {
		lineStart := i
		for i < n && src[i] != '\r' && src[i] != '\n' {
			i++
		}
		if i+1 >= n || src[i] != '\r' || src[i+1] != '\n' {
			return "", 0, false
		}
		line := src[lineStart:i]
		i += 2
		if string(line) == "." {
			return b.String(), i, true
		}
		if !firstLine {
			b.WriteString("\r\n")
		}
		firstLine = false
		// 点填充：行首的 ".." 还原为 "."。
		if len(line) >= 2 && line[0] == '.' && line[1] == '.' {
			line = line[1:]
		}
		b.Write(line)
	}
}

// ValidateScript 校验已精确读出的脚本体内容。
//
// 规则：
//   - 合法 UTF-8 且词法/基本结构成立；
//   - 若使用 require，它必须是脚本的第一条命令，且所列扩展全部
//     位于 rules.AllowedExtensions 白名单；
//   - redirect 命令数量不超过 rules.MaxRedirects（字符串中的
//     "redirect" 文本不计入）。
func ValidateScript(body []byte, rules Rules) error {
	if !utf8.Valid(body) {
		return ErrInvalidUTF8
	}
	toks, err := tokenizeSieve(body)
	if err != nil {
		return err
	}

	allowed := make(map[string]struct{}, len(rules.AllowedExtensions))
	for _, ext := range rules.AllowedExtensions {
		allowed[strings.ToLower(ext)] = struct{}{}
	}

	// 先做括号配平检查，保证块结构判断可靠。
	depth := 0
	for _, t := range toks {
		if t.kind != tokPunct {
			continue
		}
		switch t.text {
		case "{", "(", "[":
			depth++
		case "}", ")", "]":
			depth--
		}
		if depth < 0 {
			return ErrSieveSyntax
		}
	}
	if depth != 0 {
		return ErrSieveSyntax
	}

	redirects := 0
	seenOtherCommand := false
	braceDepth := 0
	// 命令起点：脚本开头，或紧跟在 ; 、 { 、 } 之后。据此把命令标识符
	// 与测试标识符（如 header、contains）区分开。
	atCommandStart := true
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind == tokString {
			atCommandStart = false
			continue
		}
		if t.kind == tokPunct {
			switch t.text {
			case "{":
				braceDepth++
				atCommandStart = true
			case "}":
				braceDepth--
				atCommandStart = true
			case ";":
				atCommandStart = true
			default:
				// ( ) [ ] , : 不改变命令边界。
				atCommandStart = false
			}
			continue
		}

		// 标识符 token。
		if strings.EqualFold(t.text, "require") && atCommandStart {
			if seenOtherCommand || braceDepth > 0 {
				return ErrRequireNotFirst
			}
			next, err := checkRequire(toks, i+1, allowed)
			if err != nil {
				return err
			}
			i = next - 1 // 循环自身的 i++ 会落到分号后的第一个 token
			seenOtherCommand = true
			atCommandStart = false
			continue
		}
		if atCommandStart {
			seenOtherCommand = true
			if strings.EqualFold(t.text, "redirect") {
				redirects++
			}
		}
		atCommandStart = false
	}
	if braceDepth != 0 {
		return ErrSieveSyntax
	}

	if redirects > rules.MaxRedirects {
		return ErrRedirectLimit
	}
	return nil
}

// checkRequire 解析 require 的参数（单个字符串或字符串列表），
// 逐个核对白名单，返回分号之后的下一个 token 下标。
func checkRequire(toks []sieveToken, i int, allowed map[string]struct{}) (int, error) {
	if i >= len(toks) {
		return 0, ErrSieveSyntax
	}
	var exts []string
	switch {
	case toks[i].kind == tokString:
		exts = append(exts, toks[i].text)
		i++
	case toks[i].kind == tokPunct && toks[i].text == "[":
		i++
		for {
			if i >= len(toks) || toks[i].kind != tokString {
				return 0, ErrSieveSyntax
			}
			exts = append(exts, toks[i].text)
			i++
			if i < len(toks) && toks[i].kind == tokPunct && toks[i].text == "," {
				i++
				continue
			}
			if i < len(toks) && toks[i].kind == tokPunct && toks[i].text == "]" {
				i++
				break
			}
			return 0, ErrSieveSyntax
		}
	default:
		return 0, ErrSieveSyntax
	}

	if len(exts) == 0 {
		return 0, ErrSieveSyntax
	}
	for _, ext := range exts {
		if _, ok := allowed[strings.ToLower(ext)]; !ok {
			return 0, ErrExtensionNotAllowed
		}
	}
	if i >= len(toks) || toks[i].kind != tokPunct || toks[i].text != ";" {
		return 0, ErrSieveSyntax
	}
	return i + 1, nil
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func isIdentStart(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isIdentPart(b byte) bool {
	return isIdentStart(b) || (b >= '0' && b <= '9')
}
