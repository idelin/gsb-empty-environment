package netsec

import (
	"strings"
	"testing"
)

func TestAnalyzeNetworkLengthWithHostOffset(t *testing.T) {
	p := &Program{
		Name: "mixed protocol header",
		Params: []Param{
			{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64},
			{Name: "net_len", Tag: TagBE, HasConst: true, Const: 4096, Pos: Position{File: "wire.go", Line: 10}},
			{Name: "host_off", Tag: TagLE, HasConst: true, Const: 16, Pos: Position{File: "wire.go", Line: 11}},
		},
		Instrs: []Instr{
			{Op: OpConst, Dst: 3, Imm: 4, Pos: Position{File: "wire.go", Line: 12}},
			{Op: OpAdd, Dst: 4, Args: []int{2, 3}, Pos: Position{File: "wire.go", Line: 13}},
			{Op: OpSlice, Dst: 5, Args: []int{0, 4, 1}, Pos: Position{File: "wire.go", Line: 14, Col: 7}},
		},
	}

	report, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1:\n%s", len(report.Findings), report)
	}
	finding := report.Findings[0]
	if finding.Pos.String() != "wire.go:14:7" {
		t.Fatalf("finding position = %q", finding.Pos)
	}
	codes := finding.Codes()
	if !contains(codes, ReasonMixedOrder) {
		t.Fatalf("missing %s in %v", ReasonMixedOrder, codes)
	}
	if !contains(codes, ReasonOutOfBounds) {
		t.Fatalf("missing %s in %v", ReasonOutOfBounds, codes)
	}

	text := finding.String()
	for _, want := range []string{
		"offset [le, known=20]",
		"length [be, known=4096]",
		"v4 = add(v2, v3)",
		"v2 = param host_off",
		"v1 = param net_len",
		"range [20,4116) exceeds buffer length 64",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("finding does not contain %q:\n%s", want, text)
		}
	}
}

func TestAnalyzeMergeConflictIsNotChosenArbitrarily(t *testing.T) {
	p := &Program{
		Name: "merge without conversion",
		Params: []Param{
			{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64},
			{Name: "be_len", Tag: TagBE, HasConst: true, Const: 8},
			{Name: "le_len", Tag: TagLE, HasConst: true, Const: 8},
		},
		Instrs: []Instr{
			{Op: OpPhi, Dst: 3, Args: []int{1, 2}},
			{Op: OpConst, Dst: 4, Imm: 0},
			{Op: OpSlice, Dst: 5, Args: []int{0, 4, 3}},
		},
	}

	report, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Values[3].Tag; got != TagConflict {
		t.Fatalf("phi tag = %s, want conflict", got)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("got %d findings, want 1:\n%s", len(report.Findings), report)
	}
	if !contains(report.Findings[0].Codes(), ReasonOrderConflict) {
		t.Fatalf("finding codes = %v, want %s", report.Findings[0].Codes(), ReasonOrderConflict)
	}
	text := report.Findings[0].String()
	if !strings.Contains(text, "be_len") || !strings.Contains(text, "le_len") {
		t.Fatalf("finding lost one merge arm:\n%s", text)
	}
}

func TestAnalyzeExplicitConversionResolvesMergeConflict(t *testing.T) {
	p := &Program{
		Name: "merge then convert",
		Params: []Param{
			{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64},
			{Name: "be_len", Tag: TagBE, HasConst: true, Const: 8},
			{Name: "le_len", Tag: TagLE, HasConst: true, Const: 8},
		},
		Instrs: []Instr{
			{Op: OpPhi, Dst: 3, Args: []int{1, 2}},
			{Op: OpConvert, Dst: 4, Args: []int{3}, Order: OrderBig},
			{Op: OpConst, Dst: 5, Imm: 0},
			{Op: OpSlice, Dst: 6, Args: []int{0, 5, 4}},
		},
	}

	report, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Values[4].Tag; got != TagBE {
		t.Fatalf("converted tag = %s, want be", got)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("explicit conversion should resolve the conflict:\n%s", report)
	}
}

func TestAnalyzeSwapDoesNotResolveMergeConflict(t *testing.T) {
	p := &Program{
		Name: "merge then swap",
		Params: []Param{
			{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64},
			{Name: "be_len", Tag: TagBE, HasConst: true, Const: 8},
			{Name: "le_len", Tag: TagLE, HasConst: true, Const: 8},
		},
		Instrs: []Instr{
			{Op: OpPhi, Dst: 3, Args: []int{1, 2}},
			{Op: OpSwap, Dst: 4, Args: []int{3}, Width: 4},
			{Op: OpConst, Dst: 5, Imm: 0},
			{Op: OpSlice, Dst: 6, Args: []int{0, 5, 4}},
		},
	}

	report, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("swap must not silently resolve a conflict:\n%s", report)
	}
	if !contains(report.Findings[0].Codes(), ReasonOrderConflict) {
		t.Fatalf("codes = %v, want %s", report.Findings[0].Codes(), ReasonOrderConflict)
	}
}

func TestAnalyzeBoundsAndNegativeRanges(t *testing.T) {
	t.Run("offset beyond buffer", func(t *testing.T) {
		p := &Program{
			Params: []Param{{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64}},
			Instrs: []Instr{
				{Op: OpConst, Dst: 1, Imm: 100},
				{Op: OpConst, Dst: 2, Imm: 10},
				{Op: OpSlice, Dst: 3, Args: []int{0, 1, 2}},
			},
		}
		report, err := Analyze(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Findings) != 1 || !contains(report.Findings[0].Codes(), ReasonOutOfBounds) {
			t.Fatalf("expected out-of-bounds finding:\n%s", report)
		}
	})

	t.Run("negative length", func(t *testing.T) {
		p := &Program{
			Params: []Param{{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64}},
			Instrs: []Instr{
				{Op: OpConst, Dst: 1, Imm: 0},
				{Op: OpConst, Dst: 2, Imm: -4},
				{Op: OpSlice, Dst: 3, Args: []int{0, 1, 2}},
			},
		}
		report, err := Analyze(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Findings) != 1 || !contains(report.Findings[0].Codes(), ReasonNegativeRange) {
			t.Fatalf("expected negative-range finding:\n%s", report)
		}
	})

	t.Run("raw offset", func(t *testing.T) {
		p := &Program{
			Params: []Param{{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64}},
			Instrs: []Instr{
				{Op: OpConst, Dst: 1, Imm: 0},
				{Op: OpConst, Dst: 2, Imm: 1},
				{Op: OpReadByte, Dst: 3, Args: []int{0, 1}},
				{Op: OpSlice, Dst: 4, Args: []int{0, 3, 2}},
			},
		}
		report, err := Analyze(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Findings) != 1 || !contains(report.Findings[0].Codes(), ReasonRawOperand) {
			t.Fatalf("expected raw-range-operand finding:\n%s", report)
		}
	})
}

func TestAnalyzeSafeBigEndianRange(t *testing.T) {
	p := &Program{
		Name: "safe network-order slice",
		Params: []Param{
			{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64},
			{Name: "net_len", Tag: TagBE, HasConst: true, Const: 16},
			{Name: "net_off", Tag: TagBE, HasConst: true, Const: 8},
		},
		Instrs: []Instr{
			{Op: OpSlice, Dst: 3, Args: []int{0, 2, 1}},
		},
	}

	report, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("safe range was reported:\n%s", report)
	}
}

func TestHostCallResultEffect(t *testing.T) {
	p := &Program{
		Name:   "host call effect",
		Params: []Param{{Name: "wire", Tag: TagRaw, HasBytes: true, Bytes: 4}},
		Instrs: []Instr{
			{
				Op:        OpHost,
				Dst:       1,
				Args:      []int{0},
				Callee:    "binary.BigEndian.Uint32",
				ResultTag: TagBE,
			},
		},
	}
	report, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := report.Values[1].Tag; got != TagBE {
		t.Fatalf("host result tag = %s, want be", got)
	}
	lines := strings.Join(report.Values[1].Trace.Lines(), "\n")
	if !strings.Contains(lines, "binary.BigEndian.Uint32") {
		t.Fatalf("host call provenance is missing the callee:\n%s", lines)
	}
}

func TestProgramValidation(t *testing.T) {
	p := &Program{
		Params: []Param{{Name: "payload", Tag: TagRaw}},
		Instrs: []Instr{{Op: OpAdd, Dst: 1, Args: []int{0, 99}}},
	}
	if err := p.Validate(); err == nil {
		t.Fatal("Validate accepted an undefined operand")
	}
	if _, err := Analyze(p); err == nil {
		t.Fatal("Analyze accepted an invalid program")
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
