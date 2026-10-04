package main

import (
	"fmt"
	"strconv"
	"strings"
)

var opTable = map[string]Op{
	"CONST": OpConst, "LOAD": OpLoad, "STORE": OpStore,
	"ADD": OpAdd, "SUB": OpSub, "MUL": OpMul, "DIV": OpDiv, "MOD": OpMod,
	"NEG": OpNeg,
	"EQ":  OpEq, "NE": OpNe, "LT": OpLt, "LE": OpLe, "GT": OpGt, "GE": OpGe,
	"JMP": OpJmp, "JZ": OpJz, "JNZ": OpJnz,
	"CALL": OpCall, "RET": OpRet, "POP": OpPop, "DUP": OpDup,
	"TRY": OpTry, "ENDTRY": OpEndTry, "THROW": OpThrow,
	"PRINT": OpPrint, "HALT": OpHalt,
}

// Assemble compiles assembly text into a Program.
//
// Syntax:
//   ; comment                (also '#' comments)
//   func <name> <nlocals>    starts a function; "main" is required
//   <label>:                 jump/catch target
//   CONST <int>              push literal
//   LOAD|STORE <slot>        local variable access
//   ADD SUB MUL DIV MOD NEG  arithmetic
//   EQ NE LT LE GT GE        comparisons (push 0/1)
//   JMP|JZ|JNZ <label>       jumps
//   CALL <func> <nargs>      args taken from operand stack
//   RET POP DUP HALT
//   TRY <label> ... ENDTRY   exception handler; THROW raises
//   PRINT                    append top of stack to output
func Assemble(src string) (*Program, error) {
	type line struct {
		text string
		no   int
	}
	var lines []line
	for i, raw := range strings.Split(src, "\n") {
		if j := strings.IndexAny(raw, ";#"); j >= 0 {
			raw = raw[:j]
		}
		raw = strings.TrimSpace(raw)
		if raw != "" {
			lines = append(lines, line{raw, i + 1})
		}
	}

	// Pass 1: collect functions and labels, count instructions.
	labels := map[string]int{}
	funcIdx := map[string]int{}
	var funcs []Func
	pc := 0
	for _, ln := range lines {
		text := ln.text
		if strings.HasSuffix(text, ":") {
			name := strings.TrimSuffix(text, ":")
			if _, dup := labels[name]; dup {
				return nil, fmt.Errorf("line %d: duplicate label %q", ln.no, name)
			}
			labels[name] = pc
			continue
		}
		fields := strings.Fields(text)
		if fields[0] == "func" {
			if len(fields) != 3 {
				return nil, fmt.Errorf("line %d: want: func <name> <nlocals>", ln.no)
			}
			nl, err := strconv.Atoi(fields[2])
			if err != nil || nl < 0 {
				return nil, fmt.Errorf("line %d: bad nlocals %q", ln.no, fields[2])
			}
			if _, dup := funcIdx[fields[1]]; dup {
				return nil, fmt.Errorf("line %d: duplicate func %q", ln.no, fields[1])
			}
			funcIdx[fields[1]] = len(funcs)
			funcs = append(funcs, Func{Name: fields[1], NumLocals: nl, Entry: pc})
			continue
		}
		if _, ok := opTable[strings.ToUpper(fields[0])]; !ok {
			return nil, fmt.Errorf("line %d: unknown instruction %q", ln.no, fields[0])
		}
		pc++
	}

	// Pass 2: emit instructions.
	code := make([]Instr, 0, pc)
	for _, ln := range lines {
		text := ln.text
		if strings.HasSuffix(text, ":") {
			continue
		}
		fields := strings.Fields(text)
		if fields[0] == "func" {
			continue
		}
		mnemonic := strings.ToUpper(fields[0])
		op := opTable[mnemonic]
		ins := Instr{Op: op}
		need := func(n int) error {
			if len(fields) != n+1 {
				return fmt.Errorf("line %d: %s wants %d operand(s)", ln.no, mnemonic, n)
			}
			return nil
		}
		switch op {
		case OpConst, OpLoad, OpStore:
			if err := need(1); err != nil {
				return nil, err
			}
			v, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: bad integer %q", ln.no, fields[1])
			}
			ins.Arg = v
		case OpJmp, OpJz, OpJnz, OpTry:
			if err := need(1); err != nil {
				return nil, err
			}
			target, ok := labels[fields[1]]
			if !ok {
				return nil, fmt.Errorf("line %d: unknown label %q", ln.no, fields[1])
			}
			ins.Arg = int64(target)
		case OpCall:
			if err := need(2); err != nil {
				return nil, err
			}
			fi, ok := funcIdx[fields[1]]
			if !ok {
				return nil, fmt.Errorf("line %d: unknown func %q", ln.no, fields[1])
			}
			n, err := strconv.Atoi(fields[2])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("line %d: bad nargs %q", ln.no, fields[2])
			}
			ins.Arg = int64(fi)
			ins.Arg2 = int64(n)
		default:
			if err := need(0); err != nil {
				return nil, err
			}
		}
		code = append(code, ins)
	}

	mainIdx, ok := funcIdx["main"]
	if !ok {
		return nil, fmt.Errorf("no main function")
	}
	return &Program{Code: code, Funcs: funcs, Main: mainIdx}, nil
}
