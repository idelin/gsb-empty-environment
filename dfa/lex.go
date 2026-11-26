package dfa

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type asmError struct {
	line int
	msg  string
}

func (e *asmError) Error() string {
	if e.line <= 0 {
		return "dfa: " + e.msg
	}
	return fmt.Sprintf("dfa: 第 %d 行: %s", e.line, e.msg)
}

func asmErr(line int, format string, args ...any) error {
	return &asmError{line: line, msg: fmt.Sprintf(format, args...)}
}

func splitLines(src string) []string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	return strings.Split(src, "\n")
}

func stripComment(line string) string {
	for i, r := range line {
		if r == '#' || r == ';' {
			return line[:i]
		}
	}
	return line
}

func tokenize(line string) ([]string, error) {
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range line {
		switch {
		case r == ' ' || r == '\t' || r == ',':
			flush()
		case r == ':':
			flush()
			toks = append(toks, ":")
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	// 把 "label" + ":" 重新拼成 "label:"，与 ParseText 的判定保持一致。
	var merged []string
	for i := 0; i < len(toks); i++ {
		if i+1 < len(toks) && toks[i+1] == ":" {
			merged = append(merged, toks[i]+":")
			i++
		} else if toks[i] == ":" {
			return nil, asmErr(0, "孤立的 ':' 标签分隔符")
		} else {
			merged = append(merged, toks[i])
		}
	}
	return merged, nil
}

func isLabelToken(tok string) bool {
	return strings.HasSuffix(tok, ":") && len(tok) > 1
}

func parseReg(tok string) (int, bool) {
	if len(tok) < 2 || tok[0] != 'r' {
		return 0, false
	}
	n, err := strconv.Atoi(tok[1:])
	if err != nil || n < 0 || n >= MaxBoolRegs {
		return 0, false
	}
	return n, true
}

func parseInputBit(tok string) (int, bool) {
	if len(tok) < 2 || tok[0] != 'p' {
		return 0, false
	}
	n, err := strconv.Atoi(tok[1:])
	if err != nil || n < 0 || n >= MaxInputBits {
		return 0, false
	}
	return n, true
}

func parseTag(tok string) (int, bool) {
	n, err := strconv.Atoi(tok)
	if err != nil || n < 0 || n > MaxEmitTag {
		return 0, false
	}
	return n, true
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
