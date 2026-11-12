package netsec

import (
	"encoding/binary"
	"errors"
	"testing"
)

const (
	testNOP   uint32 = 0xd503201f
	testRET   uint32 = 0xd65f03c0
	testB     uint32 = 0x14000000
	testBL    uint32 = 0x94000000
	testBCond uint32 = 0x54000000
	testCBZ   uint32 = 0x34000000
	testBR    uint32 = 0xd61f0000
	testBLR   uint32 = 0xd63f0000
)

func aarch64Text(words ...uint32) []byte {
	text := make([]byte, len(words)*InstructionSize)
	for i, word := range words {
		binary.LittleEndian.PutUint32(text[i*InstructionSize:], word)
	}
	return text
}

func TestInlineShortSequences(t *testing.T) {
	tests := []struct {
		name    string
		text    []byte
		entries []uint64
		base    uint64
		k       int
		budget  int
		want    Result
	}{
		{
			name:   "terminator only",
			text:   aarch64Text(testRET),
			k:      0,
			budget: 1,
			want:   Result{Count: 1, FirstAddress: 0, Found: true},
		},
		{
			name:   "two instructions before ret",
			text:   aarch64Text(testNOP, testNOP, testRET),
			k:      2,
			budget: 3 + 2,
			want:   Result{Count: 3, FirstAddress: 0, Found: true},
		},
		{
			name:    "function entry is excluded but earlier start remains",
			text:    aarch64Text(testNOP, testNOP, testRET),
			entries: []uint64{0, 8},
			k:       2,
			budget:  5,
			want:    Result{Count: 1, FirstAddress: 4, Found: true},
		},
		{
			name:    "entry and result use virtual addresses",
			text:    aarch64Text(testNOP, testNOP, testRET),
			entries: []uint64{0x400000, 0x400008},
			base:    0x400000,
			k:       2,
			budget:  5,
			want:    Result{Count: 1, FirstAddress: 0x400004, Found: true},
		},
		{
			name:   "ret br and blr endings",
			text:   aarch64Text(testNOP, testBR|5<<5, testNOP, testBLR|3<<5, testNOP, testRET),
			k:      1,
			budget: 6 + 3,
			want:   Result{Count: 6, FirstAddress: 0, Found: true},
		},
		{
			name:   "no terminator",
			text:   aarch64Text(testNOP, testNOP),
			k:      2,
			budget: 2,
			want:   Result{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FindReturnGadgets(Config{
				Text:            tt.text,
				FunctionEntries: tt.entries,
				MaxInstructions: tt.k,
				Budget:          tt.budget,
				BaseAddress:     tt.base,
			})
			if err != nil {
				t.Fatalf("FindReturnGadgets() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("FindReturnGadgets() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDirectBranchTruncatesBackwardSearch(t *testing.T) {
	tests := []struct {
		name   string
		branch uint32
	}{
		{name: "unconditional B", branch: testB},
		{name: "BL", branch: testBL},
		{name: "conditional B", branch: testBCond},
		{name: "CBZ", branch: testCBZ},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := aarch64Text(testNOP, tt.branch, testNOP, testNOP, testRET)
			got, err := FindReturnGadgets(Config{
				Text:            text,
				MaxInstructions: 10,
				Budget:          100,
			})
			if err != nil {
				t.Fatalf("FindReturnGadgets() error = %v", err)
			}

			want := Result{Count: 3, FirstAddress: 8, Found: true}
			if got != want {
				t.Fatalf("FindReturnGadgets() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestKWindowBoundary(t *testing.T) {
	text := aarch64Text(testNOP, testNOP, testNOP, testNOP, testRET)
	tests := []struct {
		k    int
		want Result
	}{
		{k: 0, want: Result{Count: 1, FirstAddress: 16, Found: true}},
		{k: 1, want: Result{Count: 2, FirstAddress: 12, Found: true}},
		{k: 2, want: Result{Count: 3, FirstAddress: 8, Found: true}},
		{k: 4, want: Result{Count: 5, FirstAddress: 0, Found: true}},
		{k: 8, want: Result{Count: 5, FirstAddress: 0, Found: true}},
	}

	for _, tt := range tests {
		got, err := FindReturnGadgets(Config{
			Text:            text,
			MaxInstructions: tt.k,
			Budget:          100,
		})
		if err != nil {
			t.Fatalf("K=%d: FindReturnGadgets() error = %v", tt.k, err)
		}
		if got != tt.want {
			t.Fatalf("K=%d: FindReturnGadgets() = %+v, want %+v", tt.k, got, tt.want)
		}
	}
}

func TestRepeatedSuffixesCountDistinctStarts(t *testing.T) {
	text := aarch64Text(
		testNOP, testNOP, testRET,
		testNOP, testNOP, testRET,
	)

	got, err := FindReturnGadgets(Config{
		Text:            text,
		MaxInstructions: 2,
		Budget:          100,
	})
	if err != nil {
		t.Fatalf("FindReturnGadgets() error = %v", err)
	}

	want := Result{Count: 6, FirstAddress: 0, Found: true}
	if got != want {
		t.Fatalf("FindReturnGadgets() = %+v, want %+v", got, want)
	}
}

func TestOverlappingSuffixCountsUniqueAddresses(t *testing.T) {
	text := aarch64Text(testNOP, testNOP, testRET, testRET)

	got, err := FindReturnGadgets(Config{
		Text:            text,
		MaxInstructions: 2,
		Budget:          100,
	})
	if err != nil {
		t.Fatalf("FindReturnGadgets() error = %v", err)
	}

	want := Result{Count: 4, FirstAddress: 0, Found: true}
	if got != want {
		t.Fatalf("FindReturnGadgets() = %+v, want %+v", got, want)
	}
}

func TestBudgetIsEnforcedExactly(t *testing.T) {
	text := aarch64Text(testNOP, testNOP, testNOP, testNOP, testRET)

	got, err := FindReturnGadgets(Config{
		Text:            text,
		MaxInstructions: 4,
		Budget:          8,
	})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("FindReturnGadgets() error = %v, want ErrBudgetExceeded", err)
	}
	if got != (Result{}) {
		t.Fatalf("FindReturnGadgets() = %+v, want zero result on budget rejection", got)
	}

	got, err = FindReturnGadgets(Config{
		Text:            text,
		MaxInstructions: 4,
		Budget:          9,
	})
	if err != nil {
		t.Fatalf("FindReturnGadgets() error = %v", err)
	}
	if want := (Result{Count: 5, FirstAddress: 0, Found: true}); got != want {
		t.Fatalf("FindReturnGadgets() = %+v, want %+v", got, want)
	}
}

func TestBudgetAppliesToInitialTextScan(t *testing.T) {
	text := aarch64Text(testNOP, testNOP, testNOP)

	_, err := FindReturnGadgets(Config{
		Text:            text,
		MaxInstructions: 1,
		Budget:          2,
	})
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("FindReturnGadgets() error = %v, want ErrBudgetExceeded", err)
	}

	got, err := FindReturnGadgets(Config{
		Text:            text,
		MaxInstructions: 1,
		Budget:          3,
	})
	if err != nil {
		t.Fatalf("FindReturnGadgets() error = %v", err)
	}
	if got != (Result{}) {
		t.Fatalf("FindReturnGadgets() = %+v, want zero result", got)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "unaligned text",
			cfg:  Config{Text: []byte{0, 0, 0, 0, 0}, Budget: 10},
		},
		{
			name: "negative K",
			cfg:  Config{MaxInstructions: -1, Budget: 10},
		},
		{
			name: "negative budget",
			cfg:  Config{Budget: -1},
		},
		{
			name: "unaligned function entry",
			cfg:  Config{Text: aarch64Text(testRET), FunctionEntries: []uint64{2}, Budget: 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FindReturnGadgets(tt.cfg)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("FindReturnGadgets() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestEmptyTextAndOutOfRangeEntries(t *testing.T) {
	got, err := FindReturnGadgets(Config{
		Budget:          0,
		FunctionEntries: []uint64{0x10, 0x20},
	})
	if err != nil {
		t.Fatalf("FindReturnGadgets() error = %v", err)
	}
	if got != (Result{}) {
		t.Fatalf("FindReturnGadgets() = %+v, want zero result", got)
	}
}
