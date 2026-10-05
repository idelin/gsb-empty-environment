package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// OpKind enumerates the supported instruction forms.
type OpKind int

const (
	OpConst OpKind = iota // rd = const imm
	OpAlu                 // rd = op ra, rb
	OpLoad                // rd = load [lo, hi)
	OpStore               // store [lo, hi), rs
	OpFence               // fence
)

// Ref is a single read or write location: a named register or an
// explicit half-open memory interval [Lo, Hi).
type Ref struct {
	IsMem  bool
	Name   string // register name (IsMem == false)
	Lo, Hi uint64 // memory interval, half-open (IsMem == true)
}

func (r Ref) String() string {
	if r.IsMem {
		return fmt.Sprintf("[%d, %d)", r.Lo, r.Hi)
	}
	return r.Name
}

// overlaps reports precise aliasing: registers alias only the same
// register; memory intervals alias iff they share at least one byte.
func (r Ref) overlaps(o Ref) bool {
	if r.IsMem != o.IsMem {
		return false
	}
	if !r.IsMem {
		return r.Name == o.Name
	}
	return r.Lo < o.Hi && o.Lo < r.Hi
}

// Instr is one straight-line instruction with explicit read/write sets.
type Instr struct {
	Index int    // position in the original sequence
	Text  string // canonical text
	Op    OpKind

	Dst, A, B string // register operands
	Lo, Hi    uint64 // memory interval
	Imm       uint64 // const immediate
	AluOp     string // add/sub/mul/xor/and/or

	Reads  []Ref
	Writes []Ref
}

var (
	identRe = `[A-Za-z_][A-Za-z0-9_]*`
	numRe   = `(?:0x[0-9a-fA-F]+|[0-9]+)`

	reConst = regexp.MustCompile(`^(` + identRe + `)\s*=\s*const\s+(` + numRe + `)$`)
	reAlu   = regexp.MustCompile(`^(` + identRe + `)\s*=\s*(add|sub|mul|xor|and|or)\s+(` + identRe + `)\s*,\s*(` + identRe + `)$`)
	reLoad  = regexp.MustCompile(`^(` + identRe + `)\s*=\s*load\s*\[\s*(` + numRe + `)\s*,\s*(` + numRe + `)\s*\)$`)
	reStore = regexp.MustCompile(`^store\s*\[\s*(` + numRe + `)\s*,\s*(` + numRe + `)\s*\)\s*,\s*(` + identRe + `)$`)
)

func parseNum(s string) (uint64, error) {
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		return strconv.ParseUint(s[2:], 16, 64)
	}
	return strconv.ParseUint(s, 10, 64)
}

func regRef(name string) Ref { return Ref{Name: name} }

func memRef(lo, hi uint64) Ref { return Ref{IsMem: true, Lo: lo, Hi: hi} }

// ParseInstr parses one instruction line (comments already stripped).
func ParseInstr(line string, index int) (*Instr, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("empty instruction")
	}
	in := &Instr{Index: index}

	if line == "fence" {
		in.Op = OpFence
		in.Text = "fence"
		return in, nil
	}

	if m := reConst.FindStringSubmatch(line); m != nil {
		v, err := parseNum(m[2])
		if err != nil {
			return nil, fmt.Errorf("bad immediate in %q: %v", line, err)
		}
		in.Op = OpConst
		in.Dst, in.Imm = m[1], v
		in.Writes = []Ref{regRef(in.Dst)}
		in.Text = fmt.Sprintf("%s = const %d", in.Dst, in.Imm)
		return in, nil
	}

	if m := reAlu.FindStringSubmatch(line); m != nil {
		in.Op = OpAlu
		in.Dst, in.AluOp, in.A, in.B = m[1], m[2], m[3], m[4]
		in.Reads = []Ref{regRef(in.A), regRef(in.B)}
		in.Writes = []Ref{regRef(in.Dst)}
		in.Text = fmt.Sprintf("%s = %s %s, %s", in.Dst, in.AluOp, in.A, in.B)
		return in, nil
	}

	if m := reLoad.FindStringSubmatch(line); m != nil {
		lo, err := parseNum(m[2])
		if err != nil {
			return nil, fmt.Errorf("bad address in %q: %v", line, err)
		}
		hi, err := parseNum(m[3])
		if err != nil {
			return nil, fmt.Errorf("bad address in %q: %v", line, err)
		}
		if err := checkInterval(lo, hi); err != nil {
			return nil, fmt.Errorf("%s: %v", line, err)
		}
		in.Op = OpLoad
		in.Dst, in.Lo, in.Hi = m[1], lo, hi
		in.Reads = []Ref{memRef(lo, hi)}
		in.Writes = []Ref{regRef(in.Dst)}
		in.Text = fmt.Sprintf("%s = load [%d, %d)", in.Dst, lo, hi)
		return in, nil
	}

	if m := reStore.FindStringSubmatch(line); m != nil {
		lo, err := parseNum(m[1])
		if err != nil {
			return nil, fmt.Errorf("bad address in %q: %v", line, err)
		}
		hi, err := parseNum(m[2])
		if err != nil {
			return nil, fmt.Errorf("bad address in %q: %v", line, err)
		}
		if err := checkInterval(lo, hi); err != nil {
			return nil, fmt.Errorf("%s: %v", line, err)
		}
		in.Op = OpStore
		in.A, in.Lo, in.Hi = m[3], lo, hi
		in.Reads = []Ref{regRef(in.A)}
		in.Writes = []Ref{memRef(lo, hi)}
		in.Text = fmt.Sprintf("store [%d, %d), %s", lo, hi, in.A)
		return in, nil
	}

	return nil, fmt.Errorf("unrecognized instruction: %q", line)
}

func checkInterval(lo, hi uint64) error {
	if hi <= lo {
		return fmt.Errorf("empty or inverted interval [%d, %d)", lo, hi)
	}
	if hi-lo > 8 {
		return fmt.Errorf("interval [%d, %d) wider than 8 bytes", lo, hi)
	}
	return nil
}

// ParseProgram parses text into a sequence of instructions. Lines may
// carry '#' comments; blank lines are ignored.
func ParseProgram(src string) ([]*Instr, error) {
	var prog []*Instr
	for ln, raw := range strings.Split(src, "\n") {
		if i := strings.Index(raw, "#"); i >= 0 {
			raw = raw[:i]
		}
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		in, err := ParseInstr(raw, len(prog))
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", ln+1, err)
		}
		prog = append(prog, in)
	}
	return prog, nil
}
