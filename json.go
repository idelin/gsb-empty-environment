package fwaudit

import (
	"encoding/json"
	"fmt"
)

// JSON wire format:
//
//	{
//	  "nregs": 4, "entry": 0,
//	  "arrays": [[10, 20]],
//	  "instrs": [
//	    {"op": "input", "dst": 0, "pos": 0, "next": [1]},
//	    {"op": "and",   "dst": 0, "imm": 240, "next": [2]},
//	    {"op": "jnz",   "src": 0, "next": [-1, 3]},
//	    {"op": "load",  "dst": 1, "src": 0, "array": 0, "next": [-1]}
//	  ]
//	}
//
// Successor -1 halts. "add" works like "and" but adds imm mod 256.

type jsonProgram struct {
	NRegs  int         `json:"nregs"`
	Entry  int         `json:"entry"`
	Arrays [][]uint8   `json:"arrays"`
	Instrs []jsonInstr `json:"instrs"`
}

type jsonInstr struct {
	Op    string `json:"op"`
	Dst   int    `json:"dst"`
	Src   int    `json:"src"`
	Imm   uint8  `json:"imm"`
	Pos   int    `json:"pos"`
	Array int    `json:"array"`
	Next  []int  `json:"next"`
}

// ParseJSON decodes a program from its JSON wire format and validates it.
func ParseJSON(data []byte) (*Program, error) {
	var jp jsonProgram
	if err := json.Unmarshal(data, &jp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	p := &Program{NRegs: jp.NRegs, Entry: jp.Entry, Arrays: jp.Arrays}
	for i, ji := range jp.Instrs {
		ins := Instr{
			Dst:  ji.Dst,
			Src:  ji.Src,
			Imm:  ji.Imm,
			Pos:  ji.Pos,
			Arr:  ji.Array,
			Next: [2]int{Halt, Halt},
		}
		switch ji.Op {
		case "input":
			ins.Op = OpInput
		case "and":
			ins.Op = OpAnd
		case "add":
			ins.Op = OpAdd
		case "jnz":
			ins.Op = OpJnz
		case "load":
			ins.Op = OpLoad
		default:
			return nil, fmt.Errorf("instr %d: unknown op %q", i, ji.Op)
		}
		want := 1
		if ins.Op == OpJnz {
			want = 2
		}
		if len(ji.Next) != want {
			return nil, fmt.Errorf("instr %d: op %q wants %d successor(s), got %d", i, ji.Op, want, len(ji.Next))
		}
		copy(ins.Next[:], ji.Next)
		p.Instrs = append(p.Instrs, ins)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}
