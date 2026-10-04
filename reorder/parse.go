package reorder

import (
	"fmt"
	"strconv"
	"strings"
)

// 指令文本格式（# 之后为注释，空行忽略）：
//
//	barrier
//	r1 = li 42
//	r1 = mov r2
//	r1 = add r2, r3        （add/sub/xor/and/or 同理）
//	r1 = load [100, 104)   （半开区间；单字节可写 [100]）
//	[100, 104) = store r1

// Parse 解析整个指令序列文件。
func Parse(text string) ([]Inst, error) {
	var insts []Inst
	lines := strings.Split(text, "\n")
	for n, raw := range lines {
		line := raw
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		inst, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行: %v", n+1, err)
		}
		inst.Line = n + 1
		insts = append(insts, inst)
	}
	return insts, nil
}

func parseLine(line string) (Inst, error) {
	if line == "barrier" {
		return Inst{Kind: OpBarrier}, nil
	}
	if strings.HasPrefix(line, "[") {
		return parseStore(line)
	}
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return Inst{}, fmt.Errorf("缺少 '='：%q", line)
	}
	dest := strings.TrimSpace(line[:eq])
	if !isRegName(dest) {
		return Inst{}, fmt.Errorf("非法目的寄存器 %q", dest)
	}
	rhs := strings.TrimSpace(line[eq+1:])
	op := rhs
	args := ""
	if i := strings.IndexAny(rhs, " \t"); i >= 0 {
		op = rhs[:i]
		args = strings.TrimSpace(rhs[i+1:])
	}
	switch op {
	case "li":
		v, err := parseNum(args)
		if err != nil {
			return Inst{}, err
		}
		return Inst{Kind: OpLi, Dest: dest, Imm: v}, nil
	case "mov":
		if !isRegName(args) {
			return Inst{}, fmt.Errorf("mov 期望寄存器参数，得到 %q", args)
		}
		return Inst{Kind: OpMov, Dest: dest, Src: args}, nil
	case "add", "sub", "xor", "and", "or":
		parts := strings.Split(args, ",")
		if len(parts) != 2 {
			return Inst{}, fmt.Errorf("%s 期望两个寄存器参数（逗号分隔），得到 %q", op, args)
		}
		s1 := strings.TrimSpace(parts[0])
		s2 := strings.TrimSpace(parts[1])
		if !isRegName(s1) || !isRegName(s2) {
			return Inst{}, fmt.Errorf("%s 参数必须是寄存器：%q", op, args)
		}
		var kind OpKind
		switch op {
		case "add":
			kind = OpAdd
		case "sub":
			kind = OpSub
		case "xor":
			kind = OpXor
		case "and":
			kind = OpAnd
		case "or":
			kind = OpOr
		}
		return Inst{Kind: kind, Dest: dest, Src1: s1, Src2: s2}, nil
	case "load":
		loc, rest, err := parseInterval(args)
		if err != nil {
			return Inst{}, err
		}
		if strings.TrimSpace(rest) != "" {
			return Inst{}, fmt.Errorf("load 区间后有多余内容：%q", rest)
		}
		return Inst{Kind: OpLoad, Dest: dest, Addr: loc.Addr, Size: loc.Size}, nil
	default:
		return Inst{}, fmt.Errorf("未知操作 %q", op)
	}
}

func parseStore(line string) (Inst, error) {
	loc, rest, err := parseInterval(line)
	if err != nil {
		return Inst{}, err
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, "=") {
		return Inst{}, fmt.Errorf("store 缺少 '='：%q", line)
	}
	fields := strings.Fields(strings.TrimSpace(rest[1:]))
	if len(fields) != 2 || fields[0] != "store" || !isRegName(fields[1]) {
		return Inst{}, fmt.Errorf("期望 '= store <寄存器>'，得到 %q", rest)
	}
	return Inst{Kind: OpStore, Addr: loc.Addr, Size: loc.Size, Src: fields[1]}, nil
}

// parseInterval 解析 "[a]" 或 "[a, b)" 形式的半开区间，返回位置与剩余字符串。
func parseInterval(s string) (Location, string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return Location{}, s, fmt.Errorf("期望 '['：%q", s)
	}
	start, rest, err := takeNum(s[1:])
	if err != nil {
		return Location{}, s, err
	}
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, "]") {
		return Mem(start, 1), rest[1:], nil
	}
	if !strings.HasPrefix(rest, ",") {
		return Location{}, s, fmt.Errorf("区间期望 ',' 或 ']'：%q", s)
	}
	end, rest, err := takeNum(strings.TrimSpace(rest[1:]))
	if err != nil {
		return Location{}, s, err
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, ")") {
		return Location{}, s, fmt.Errorf("区间必须以 ')' 结尾（半开区间）：%q", s)
	}
	if end <= start {
		return Location{}, s, fmt.Errorf("区间终点 %d 必须大于起点 %d", end, start)
	}
	return Mem(start, end-start), rest[1:], nil
}

// takeNum 从字符串头部取一个十进制或 0x 十六进制数。
func takeNum(s string) (uint64, string, error) {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && isNumChar(s[i]) {
		i++
	}
	if i == 0 {
		return 0, s, fmt.Errorf("期望数字：%q", s)
	}
	v, err := parseNum(s[:i])
	if err != nil {
		return 0, s, err
	}
	return v, s[i:], nil
}

func isNumChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == 'x' || c == 'X'
}

func parseNum(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("非法数字 %q", s)
	}
	return v, nil
}

var keywords = map[string]bool{
	"barrier": true, "mov": true, "li": true,
	"add": true, "sub": true, "xor": true, "and": true, "or": true,
	"load": true, "store": true,
}

func isRegName(s string) bool {
	if s == "" || keywords[s] {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' ||
			i > 0 && c >= '0' && c <= '9'
		if !ok {
			return false
		}
	}
	return true
}
