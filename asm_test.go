package seccomp

import "testing"

// testAsm is a tiny label-aware cBPF builder for readable fixtures.
type testAsm struct {
	ins    []Instruction
	labels map[string]int
	fixes  []testFix
}

type testFix struct {
	at    int
	which string // "jt", "jf", "ja"
	label string
}

func newAsm() *testAsm { return &testAsm{labels: map[string]int{}} }

func (a *testAsm) emit(code uint16, k uint32, jt, jf uint8) int {
	a.ins = append(a.ins, Instruction{Code: code, JT: jt, JF: jf, K: k})
	return len(a.ins) - 1
}

func (a *testAsm) stmt(code uint16, k uint32) int { return a.emit(code, k, 0, 0) }

func (a *testAsm) label(name string) { a.labels[name] = len(a.ins) }

func (a *testAsm) jeqK(k uint32, t, f string) {
	at := a.emit(JmpJEQ|SrcK|ClassJMP, k, 0, 0)
	a.fixes = append(a.fixes, testFix{at, "jt", t}, testFix{at, "jf", f})
}

func (a *testAsm) jgeK(k uint32, t, f string) {
	at := a.emit(JmpJGE|SrcK|ClassJMP, k, 0, 0)
	a.fixes = append(a.fixes, testFix{at, "jt", t}, testFix{at, "jf", f})
}

func (a *testAsm) jsetK(k uint32, t, f string) {
	at := a.emit(JmpJSET|SrcK|ClassJMP, k, 0, 0)
	a.fixes = append(a.fixes, testFix{at, "jt", t}, testFix{at, "jf", f})
}

func (a *testAsm) ja(label string) {
	at := a.emit(JmpJA|ClassJMP, 0, 0, 0)
	a.fixes = append(a.fixes, testFix{at, "ja", label})
}

func (a *testAsm) build(t *testing.T) []Instruction {
	t.Helper()
	for _, f := range a.fixes {
		target, ok := a.labels[f.label]
		if !ok {
			t.Fatalf("undefined label %q", f.label)
		}
		off := target - (f.at + 1)
		if off < 0 {
			t.Fatalf("label %q resolves backward (%d)", f.label, off)
		}
		switch f.which {
		case "jt":
			if off > 255 {
				t.Fatalf("jt offset %d too large", off)
			}
			a.ins[f.at].JT = uint8(off)
		case "jf":
			if off > 255 {
				t.Fatalf("jf offset %d too large", off)
			}
			a.ins[f.at].JF = uint8(off)
		case "ja":
			a.ins[f.at].K = uint32(off)
		}
	}
	if err := Validate(a.ins); err != nil {
		t.Fatalf("fixture failed validation: %v\n%s", err, Disassemble(a.ins))
	}
	return a.ins
}

// Common opcode words used by fixtures.
const (
	opLdNR   = ClassLD | SizeW | ModeABS
	opLdArch = ClassLD | SizeW | ModeABS
	opRetK   = ClassRET | RetK
)
