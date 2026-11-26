package stlf

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// ParseProgram parses the small textual assembly used by the examples and CLI.
//
// Examples:
//
//	load  r1, [0x405]
//	store r1, [0x40a]
//	load  r2, [0x00a]
//	xor   r3, 0x01
//	cmp_branch r0, 0x00, leak, taken
//	probe(r2)
//	barrier
//
// Address operands may be [imm], [reg], or [imm + reg].  Comments start with
// ';' or '//'.  Labels are written "name:" and may share a line with an
// instruction.
func ParseProgram(src string) ([]Instruction, error) {
	type rawLine struct {
		text string
		line int
	}
	var raws []rawLine
	labels := map[string]int{}

	for i, original := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := stripComment(original)
		line = strings.TrimSpace(line)
		for {
			colon := strings.IndexByte(line, ':')
			if colon < 0 {
				break
			}
			name := strings.TrimSpace(line[:colon])
			if name == "" || !validLabel(name) {
				break
			}
			if _, exists := labels[name]; exists {
				return nil, fmt.Errorf("line %d: duplicate label %q", lineNo, name)
			}
			labels[name] = len(raws)
			line = strings.TrimSpace(line[colon+1:])
		}
		if line == "" {
			continue
		}
		raws = append(raws, rawLine{text: line, line: lineNo})
	}

	code := make([]Instruction, 0, len(raws))
	for _, raw := range raws {
		ins, err := parseInstruction(raw.text, labels, len(raws))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", raw.line, err)
		}
		code = append(code, ins)
	}
	if len(code) > MaxInstructions {
		return nil, fmt.Errorf("program has %d instructions; maximum is %d", len(code), MaxInstructions)
	}
	return code, nil
}

// MustParseProgram is ParseProgram with a panic on error.
func MustParseProgram(src string) []Instruction {
	code, err := ParseProgram(src)
	if err != nil {
		panic(err)
	}
	return code
}

func stripComment(s string) string {
	if i := strings.Index(s, "//"); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return s
}

func validLabel(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if unicode.IsLetter(r) || r == '_' || r == '.' || (i > 0 && unicode.IsDigit(r)) {
			continue
		}
		return false
	}
	return true
}

func parseInstruction(text string, labels map[string]int, endPC int) (Instruction, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Instruction{}, fmt.Errorf("empty instruction")
	}

	// probe(rN) is accepted both with and without spaces.
	if strings.HasPrefix(strings.ToLower(text), "probe") {
		rest := strings.TrimSpace(text[len("probe"):])
		if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
			rest = strings.TrimSpace(rest[1 : len(rest)-1])
		}
		reg, err := parseRegister(rest)
		if err != nil {
			return Instruction{}, fmt.Errorf("probe: %w", err)
		}
		return Probe(reg), nil
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Instruction{}, fmt.Errorf("empty instruction")
	}
	op := strings.ToLower(fields[0])
	rest := strings.TrimSpace(text[len(fields[0]):])

	switch op {
	case "barrier":
		if rest != "" {
			return Instruction{}, fmt.Errorf("barrier takes no operands")
		}
		return Barrier(), nil
	case "load":
		parts := splitOperands(rest)
		if len(parts) != 2 {
			return Instruction{}, fmt.Errorf("load wants: load reg, [address]")
		}
		dst, err := parseRegister(parts[0])
		if err != nil {
			return Instruction{}, err
		}
		addr, mode, index, err := parseAddress(parts[1])
		if err != nil {
			return Instruction{}, err
		}
		return Instruction{Op: OpLoad, Reg: dst, Addr: addr, AddrMode: mode, Index: index}, nil
	case "store":
		parts := splitOperands(rest)
		if len(parts) != 2 {
			return Instruction{}, fmt.Errorf("store wants: store value, [address]")
		}
		value := strings.TrimSpace(parts[0])
		addr, mode, index, err := parseAddress(parts[1])
		if err != nil {
			return Instruction{}, err
		}
		if strings.HasPrefix(value, "r") || strings.HasPrefix(value, "R") {
			reg, err := parseRegister(value)
			if err != nil {
				return Instruction{}, err
			}
			return Instruction{Op: OpStore, Reg: reg, Addr: addr, AddrMode: mode, Index: index}, nil
		}
		imm, err := parseNumber(value)
		if err != nil {
			return Instruction{}, fmt.Errorf("store value: %w", err)
		}
		return Instruction{Op: OpStore, Imm: imm, StoreImm: true, Addr: addr, AddrMode: mode, Index: index}, nil
	case "xor":
		parts := splitOperands(rest)
		if len(parts) != 2 {
			return Instruction{}, fmt.Errorf("xor wants: xor reg, value")
		}
		dst, err := parseRegister(parts[0])
		if err != nil {
			return Instruction{}, err
		}
		rhs := strings.TrimSpace(parts[1])
		if strings.HasPrefix(rhs, "r") || strings.HasPrefix(rhs, "R") {
			src, err := parseRegister(rhs)
			if err != nil {
				return Instruction{}, err
			}
			return XorReg(dst, src), nil
		}
		imm, err := parseNumber(rhs)
		if err != nil {
			return Instruction{}, fmt.Errorf("xor value: %w", err)
		}
		return XorImm(dst, imm), nil
	case "cmp_branch", "cmpbranch", "branch":
		return parseBranch(rest, labels, endPC)
	default:
		return Instruction{}, fmt.Errorf("unknown opcode %q", fields[0])
	}
}

func parseBranch(rest string, labels map[string]int, endPC int) (Instruction, error) {
	parts := splitOperands(rest)
	if len(parts) < 3 || len(parts) > 4 {
		return Instruction{}, fmt.Errorf("cmp_branch wants: cmp_branch reg, value, target, [taken|not]")
	}
	reg, err := parseRegister(parts[0])
	if err != nil {
		return Instruction{}, err
	}
	rhs := strings.TrimSpace(parts[1])
	ins := Instruction{Op: OpCmpBranch, Reg: reg}
	if strings.HasPrefix(rhs, "r") || strings.HasPrefix(rhs, "R") {
		src, err := parseRegister(rhs)
		if err != nil {
			return Instruction{}, err
		}
		ins.Src = src
		ins.SrcIsReg = true
	} else {
		imm, err := parseNumber(rhs)
		if err != nil {
			return Instruction{}, fmt.Errorf("cmp_branch value: %w", err)
		}
		ins.Imm = imm
	}
	target, err := parseTarget(parts[2], labels, endPC)
	if err != nil {
		return Instruction{}, err
	}
	ins.Target = target
	if len(parts) == 4 {
		switch strings.ToLower(strings.TrimSpace(parts[3])) {
		case "taken", "t", "yes", "1":
			ins.PredictedTaken = true
		case "not", "nt", "n", "not_taken", "nottaken", "0":
			ins.PredictedTaken = false
		default:
			return Instruction{}, fmt.Errorf("unknown branch prediction %q", parts[3])
		}
	}
	return ins, nil
}

func parseTarget(s string, labels map[string]int, endPC int) (int, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "->")
	if target, ok := labels[s]; ok {
		return target, nil
	}
	n, err := parseUintAuto(s)
	if err != nil {
		return 0, fmt.Errorf("branch target %q is neither a label nor a number", s)
	}
	if n > uint64(endPC) {
		return 0, fmt.Errorf("branch target %d out of range [0,%d]", n, endPC)
	}
	return int(n), nil
}

func parseRegister(s string) (uint8, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || (s[0] != 'r' && s[0] != 'R') {
		return 0, fmt.Errorf("expected register rN, got %q", s)
	}
	n, err := strconv.Atoi(s[1:])
	if err != nil || n < 0 || n >= RegisterCount {
		return 0, fmt.Errorf("register %q out of range r0..r15", s)
	}
	return uint8(n), nil
}

func parseNumber(s string) (uint8, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "#")
	n, err := parseUintAuto(s)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q", s)
	}
	if n > 0xff {
		return 0, fmt.Errorf("value %q exceeds 8 bits", s)
	}
	return uint8(n), nil
}

func parseAddress(s string) (uint16, AddrMode, uint8, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return 0, 0, 0, fmt.Errorf("expected [address], got %q", s)
	}
	body := strings.TrimSpace(s[1 : len(s)-1])
	if body == "" {
		return 0, 0, 0, fmt.Errorf("empty address")
	}
	terms := strings.Split(body, "+")
	if len(terms) == 1 {
		term := strings.TrimSpace(terms[0])
		if strings.HasPrefix(term, "r") || strings.HasPrefix(term, "R") {
			reg, err := parseRegister(term)
			if err != nil {
				return 0, 0, 0, err
			}
			return 0, AddrRegister, reg, nil
		}
		addr, err := parseAddressNumber(term)
		if err != nil {
			return 0, 0, 0, err
		}
		return addr, AddrImmediate, 0, nil
	}
	if len(terms) != 2 {
		return 0, 0, 0, fmt.Errorf("address must be [imm], [reg], or [imm + reg]")
	}
	a, b := strings.TrimSpace(terms[0]), strings.TrimSpace(terms[1])
	if strings.HasPrefix(a, "r") || strings.HasPrefix(a, "R") {
		a, b = b, a
	}
	base, err := parseAddressNumber(a)
	if err != nil {
		return 0, 0, 0, err
	}
	reg, err := parseRegister(b)
	if err != nil {
		return 0, 0, 0, err
	}
	return base, AddrIndexed, reg, nil
}

func parseAddressNumber(s string) (uint16, error) {
	n, err := parseUintAuto(s)
	if err != nil {
		return 0, fmt.Errorf("invalid address %q", s)
	}
	if n >= AddressSpace {
		return 0, fmt.Errorf("address %#x exceeds 12 bits", n)
	}
	return uint16(n), nil
}

// parseUintAuto accepts 0x/0X hexadecimal, 0b/0B binary, 0o/0O octal, and
// decimal (including values with a leading zero such as 08).
func parseUintAuto(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	base := 10
	digits := s
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		base, digits = 16, s[2:]
	case strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B"):
		base, digits = 2, s[2:]
	case strings.HasPrefix(s, "0o") || strings.HasPrefix(s, "0O"):
		base, digits = 8, s[2:]
	}
	if digits == "" {
		return 0, fmt.Errorf("empty number")
	}
	return strconv.ParseUint(digits, base, 64)
}

// splitOperands splits a comma-separated operand list while preserving commas
// inside brackets.  Bracketed addresses do not contain commas in this ISA, but
// the routine keeps error handling predictable.
func splitOperands(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(s[start:]); tail != "" || len(parts) > 0 {
		parts = append(parts, tail)
	}
	return parts
}
