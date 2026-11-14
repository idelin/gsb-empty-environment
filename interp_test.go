package netsec

import (
	"strings"
	"testing"
)

func TestInterpreterByteOrderSamples(t *testing.T) {
	p := &Program{
		Name:   "byte-order samples",
		Params: []Param{{Name: "data", Tag: TagRaw, HasBytes: true, Bytes: 4}},
		Instrs: []Instr{
			{Op: OpConst, Dst: 1, Imm: 0},
			{Op: OpReadBE, Dst: 2, Args: []int{0, 1}, Width: 4},
			{Op: OpReadLE, Dst: 3, Args: []int{0, 1}, Width: 4},
			{Op: OpSwap, Dst: 4, Args: []int{3}, Width: 4},
		},
	}

	exec, err := Interpret(p, []Concrete{RawData([]byte{0x00, 0x00, 0x01, 0x00})})
	if err != nil {
		t.Fatal(err)
	}
	assertInt(t, exec, 2, 256, TagBE)
	assertInt(t, exec, 3, 65536, TagLE)
	assertInt(t, exec, 4, 256, TagBE)
}

func TestInterpreterPackAndSwap(t *testing.T) {
	p := &Program{
		Name:   "pack samples",
		Params: []Param{{Name: "data", Tag: TagRaw, HasBytes: true, Bytes: 4}},
		Instrs: []Instr{
			{Op: OpConst, Dst: 4, Imm: 0},
			{Op: OpConst, Dst: 5, Imm: 1},
			{Op: OpConst, Dst: 6, Imm: 2},
			{Op: OpConst, Dst: 7, Imm: 3},
			{Op: OpReadByte, Dst: 8, Args: []int{0, 4}},
			{Op: OpReadByte, Dst: 9, Args: []int{0, 5}},
			{Op: OpReadByte, Dst: 10, Args: []int{0, 6}},
			{Op: OpReadByte, Dst: 11, Args: []int{0, 7}},
			{Op: OpPack, Dst: 12, Args: []int{8, 9, 10, 11}, Order: OrderBig},
			{Op: OpPack, Dst: 13, Args: []int{8, 9, 10, 11}, Order: OrderLittle},
		},
	}

	exec, err := Interpret(p, []Concrete{RawData([]byte{0x00, 0x00, 0x01, 0x00})})
	if err != nil {
		t.Fatal(err)
	}
	assertInt(t, exec, 12, 256, TagBE)
	assertInt(t, exec, 13, 65536, TagLE)
}

func TestInterpreterVulnerableSlice(t *testing.T) {
	p := vulnerableProgram()
	payload := make([]byte, 64)
	meta := []byte{
		0x10, 0x00, 0x00, 0x00, // host-order offset 16
		0x00, 0x00, 0x10, 0x00, // network-order length 4096
	}
	_, err := Interpret(p, []Concrete{RawData(payload), RawData(meta)})
	if err == nil {
		t.Fatal("mixed-endian slice did not fail at run time")
	}
	if !strings.Contains(err.Error(), "out of bounds") {
		t.Fatalf("unexpected error: %v", err)
	}

	report, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("analyzer did not flag the vulnerable slice:\n%s", report)
	}
	if !contains(report.Findings[0].Codes(), ReasonMixedOrder) {
		t.Fatalf("codes = %v, want %s", report.Findings[0].Codes(), ReasonMixedOrder)
	}
}

func TestInterpreterSafeSlice(t *testing.T) {
	p := &Program{
		Name: "safe slice",
		Params: []Param{
			{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64},
			{Name: "meta", Tag: TagRaw, HasBytes: true, Bytes: 8},
		},
		Instrs: []Instr{
			{Op: OpConst, Dst: 2, Imm: 0},
			{Op: OpConst, Dst: 3, Imm: 4},
			{Op: OpReadBE, Dst: 4, Args: []int{1, 2}, Width: 4}, // offset 16
			{Op: OpReadBE, Dst: 5, Args: []int{1, 3}, Width: 4}, // length 16
			{Op: OpSlice, Dst: 6, Args: []int{0, 4, 5}},
		},
	}

	meta := []byte{
		0x00, 0x00, 0x00, 0x10,
		0x00, 0x00, 0x00, 0x10,
	}
	exec, err := Interpret(p, []Concrete{RawData(make([]byte, 64)), RawData(meta)})
	if err != nil {
		t.Fatal(err)
	}
	slice, err := exec.Get(6)
	if err != nil {
		t.Fatal(err)
	}
	data, err := slice.ByteSlice()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 16 {
		t.Fatalf("slice length = %d, want 16", len(data))
	}

	report, err := Analyze(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 {
		t.Fatalf("safe program was flagged:\n%s", report)
	}
}

func TestInterpreterConvertRelabelsWithoutChangingTheNumber(t *testing.T) {
	p := &Program{
		Params: []Param{{Name: "data", Tag: TagRaw}},
		Instrs: []Instr{
			{Op: OpConst, Dst: 1, Imm: 0},
			{Op: OpReadBE, Dst: 2, Args: []int{0, 1}, Width: 4},
			{Op: OpConvert, Dst: 3, Args: []int{2}, Order: OrderLittle},
		},
	}
	exec, err := Interpret(p, []Concrete{RawData([]byte{0x00, 0x00, 0x01, 0x00})})
	if err != nil {
		t.Fatal(err)
	}
	assertInt(t, exec, 3, 256, TagLE)
}

func TestInterpreterConvertRawBytes(t *testing.T) {
	p := &Program{
		Params: []Param{{Name: "data", Tag: TagRaw}},
		Instrs: []Instr{
			{Op: OpConvert, Dst: 1, Args: []int{0}, Order: OrderLittle},
		},
	}
	exec, err := Interpret(p, []Concrete{RawData([]byte{0x00, 0x00, 0x01, 0x00})})
	if err != nil {
		t.Fatal(err)
	}
	assertInt(t, exec, 1, 65536, TagLE)
}

func TestInterpreterReadOutOfBounds(t *testing.T) {
	p := &Program{
		Params: []Param{{Name: "data", Tag: TagRaw}},
		Instrs: []Instr{
			{Op: OpConst, Dst: 1, Imm: 2},
			{Op: OpReadBE, Dst: 2, Args: []int{0, 1}, Width: 4},
		},
	}
	if _, err := Interpret(p, []Concrete{RawData([]byte{1, 2, 3})}); err == nil {
		t.Fatal("read beyond the buffer did not fail")
	}
}

func vulnerableProgram() *Program {
	return &Program{
		Name: "network length plus host offset",
		Params: []Param{
			{Name: "payload", Tag: TagRaw, HasBytes: true, Bytes: 64},
			{Name: "meta", Tag: TagRaw, HasBytes: true, Bytes: 8},
		},
		Instrs: []Instr{
			{Op: OpConst, Dst: 2, Imm: 0},
			{Op: OpConst, Dst: 3, Imm: 4},
			{Op: OpReadLE, Dst: 4, Args: []int{1, 2}, Width: 4},
			{Op: OpReadBE, Dst: 5, Args: []int{1, 3}, Width: 4},
			{Op: OpSlice, Dst: 6, Args: []int{0, 4, 5}, Pos: Position{File: "wire.go", Line: 42}},
		},
	}
}

func assertInt(t *testing.T, exec *Execution, id int, want uint64, tag Tag) {
	t.Helper()
	value, err := exec.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := value.Integer()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("v%d = %d, want %d", id, got, want)
	}
	if value.Label != tag {
		t.Fatalf("v%d label = %s, want %s", id, value.Label, tag)
	}
}
