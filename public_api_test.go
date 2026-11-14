package netsec_test

import (
	"strings"
	"testing"

	netsec "example.org/netsec/q0384"
)

func TestPublicAnalysisAPI(t *testing.T) {
	p := &netsec.Program{
		Params: []netsec.Param{
			{Name: "payload", Tag: netsec.TagRaw, HasBytes: true, Bytes: 64},
			{Name: "net_len", Tag: netsec.TagBE, HasConst: true, Const: 4096},
			{Name: "host_off", Tag: netsec.TagLE, HasConst: true, Const: 16},
		},
		Instrs: []netsec.Instr{
			{Op: netsec.OpSlice, Dst: 3, Args: []int{0, 2, 1}},
		},
	}
	report, err := netsec.Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(report.Findings))
	}
	if text := report.String(); !strings.Contains(text, "mixed-byte-order") {
		t.Fatalf("report does not show the mixed order:\n%s", text)
	}
}

func TestPublicInterpreterAPI(t *testing.T) {
	p := &netsec.Program{
		Params: []netsec.Param{{Name: "data", Tag: netsec.TagRaw}},
		Instrs: []netsec.Instr{
			{Op: netsec.OpConst, Dst: 1, Imm: 0},
			{Op: netsec.OpReadBE, Dst: 2, Args: []int{0, 1}, Width: 4},
			{Op: netsec.OpReadLE, Dst: 3, Args: []int{0, 1}, Width: 4},
		},
	}
	exec, err := netsec.Interpret(p, []netsec.Concrete{
		netsec.RawData([]byte{0x00, 0x00, 0x01, 0x00}),
	})
	if err != nil {
		t.Fatal(err)
	}
	be, err := exec.Get(2)
	if err != nil {
		t.Fatal(err)
	}
	le, err := exec.Get(3)
	if err != nil {
		t.Fatal(err)
	}
	if be.Num != 256 || be.Label != netsec.TagBE {
		t.Fatalf("BE sample = %d/%s, want 256/be", be.Num, be.Label)
	}
	if le.Num != 65536 || le.Label != netsec.TagLE {
		t.Fatalf("LE sample = %d/%s, want 65536/le", le.Num, le.Label)
	}
}
