package interp

import (
	"fmt"
	"strconv"
	"strings"
)

// Assemble compiles a text script into a Program.
//
// Syntax (one instruction per line, ';' or '#' start a comment):
//
//	func main 0 1        ; function directive: name, nargs, nlocals
//	label:               ; jump label
//	push <int>           ; load/store <slot>
//	add|sub|mul|div|mod|neg
//	eq|ne|lt|le|gt|ge
//	jmp|jz|try <label>
//	call <func>          ; nargs filled in from the func directive
//	ret|untry|throw|print|pause|halt
//
// The function named "main" is the entry point (defaults to address 0).
func Assemble(src string) (*Program, error) {
	type patch struct {
		at    int
		label string
		call  bool
		line  int
	}
	var code []Instr
	funcs := map[int]FuncInfo{}
	labels := map[string]int{}
	var patches []patch
	entry := -1

	noArg := map[string]Op{
		"add": OpAdd, "sub": OpSub, "mul": OpMul, "div": OpDiv, "mod": OpMod,
		"neg": OpNeg,
		"eq":  OpEq, "ne": OpNe, "lt": OpLt, "le": OpLe, "gt": OpGt, "ge": OpGe,
		"ret": OpRet, "untry": OpUntry, "throw": OpThrow, "print": OpPrint,
		"pause": OpPause, "halt": OpHalt,
	}
	intArg := map[string]Op{"push": OpPush, "load": OpLoad, "store": OpStore}
	labelArg := map[string]Op{"jmp": OpJmp, "jz": OpJz, "try": OpTry}

	for ln, raw := range strings.Split(src, "\n") {
		line := raw
		if i := strings.IndexAny(line, ";#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		for len(fields) > 0 && strings.HasSuffix(fields[0], ":") {
			name := strings.TrimSuffix(fields[0], ":")
			if _, dup := labels[name]; dup {
				return nil, fmt.Errorf("line %d: duplicate label %q", ln+1, name)
			}
			labels[name] = len(code)
			fields = fields[1:]
		}
		if len(fields) == 0 {
			continue
		}
		op := fields[0]
		switch {
		case op == "func":
			if len(fields) != 4 {
				return nil, fmt.Errorf("line %d: usage: func <name> <nargs> <nlocals>", ln+1)
			}
			nargs, err := strconv.Atoi(fields[2])
			if err != nil {
				return nil, fmt.Errorf("line %d: bad nargs: %v", ln+1, err)
			}
			nlocals, err := strconv.Atoi(fields[3])
			if err != nil {
				return nil, fmt.Errorf("line %d: bad nlocals: %v", ln+1, err)
			}
			if nargs > nlocals {
				return nil, fmt.Errorf("line %d: nargs > nlocals", ln+1)
			}
			if _, dup := labels[fields[1]]; dup {
				return nil, fmt.Errorf("line %d: duplicate function %q", ln+1, fields[1])
			}
			labels[fields[1]] = len(code)
			funcs[len(code)] = FuncInfo{Name: fields[1], NArgs: nargs, NLocals: nlocals}
			if fields[1] == "main" {
				entry = len(code)
			}
		case hasOp(noArg, op):
			if len(fields) != 1 {
				return nil, fmt.Errorf("line %d: %s takes no operand", ln+1, op)
			}
			code = append(code, Instr{Op: noArg[op]})
		case hasOp(intArg, op):
			if len(fields) != 2 {
				return nil, fmt.Errorf("line %d: %s expects an integer operand", ln+1, op)
			}
			n, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: %v", ln+1, err)
			}
			code = append(code, Instr{Op: intArg[op], Arg: n})
		case hasOp(labelArg, op):
			if len(fields) != 2 {
				return nil, fmt.Errorf("line %d: %s expects a label operand", ln+1, op)
			}
			patches = append(patches, patch{at: len(code), label: fields[1], line: ln + 1})
			code = append(code, Instr{Op: labelArg[op]})
		case op == "call":
			if len(fields) != 2 {
				return nil, fmt.Errorf("line %d: call expects a function name", ln+1)
			}
			patches = append(patches, patch{at: len(code), label: fields[1], call: true, line: ln + 1})
			code = append(code, Instr{Op: OpCall})
		default:
			return nil, fmt.Errorf("line %d: unknown instruction %q", ln+1, op)
		}
	}

	for _, p := range patches {
		addr, ok := labels[p.label]
		if !ok {
			return nil, fmt.Errorf("line %d: undefined label %q", p.line, p.label)
		}
		code[p.at].Arg = int64(addr)
		if p.call {
			fi, ok := funcs[addr]
			if !ok {
				return nil, fmt.Errorf("line %d: call target %q is not a function", p.line, p.label)
			}
			code[p.at].Arg2 = int64(fi.NArgs)
		}
	}
	if entry < 0 {
		entry = 0
	}
	return &Program{Code: code, Funcs: funcs, Entry: entry}, nil
}

func hasOp(m map[string]Op, name string) bool {
	_, ok := m[name]
	return ok
}
