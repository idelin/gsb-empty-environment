package netsec

import (
	"bytes"
	"strings"
	"testing"
)

func defaultMemory() []byte {
	return make([]byte, MaxMemoryBytes)
}

func TestReplayCountsAndWrites(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		args      []uintptr
		initial   []byte
		wantCount int
		wantWrite []Write
		wantMem   []byte
	}{
		{
			name:      "ordinary printable bytes",
			format:    "Az~",
			initial:   defaultMemory(),
			wantCount: 3,
			wantMem:   defaultMemory(),
		},
		{
			name:      "percent escape",
			format:    "%%",
			initial:   defaultMemory(),
			wantCount: 1,
			wantMem:   defaultMemory(),
		},
		{
			name:      "mixed ordinary escape and count",
			format:    "a%%%3c",
			initial:   defaultMemory(),
			wantCount: 5,
			wantMem:   defaultMemory(),
		},
		{
			name:      "zero count write",
			format:    "%1$hhn",
			args:      []uintptr{0},
			initial:   []byte{0xaa},
			wantCount: 0,
			wantWrite: []Write{{Arg: 1, Address: 0, Offset: 0, FormatOffset: 0, Count: 0, Value: 0}},
			wantMem:   []byte{0},
		},
		{
			name:      "escape before write",
			format:    "%%%1$hhn",
			args:      []uintptr{0},
			initial:   []byte{0xaa},
			wantCount: 1,
			wantWrite: []Write{{Arg: 1, Address: 0, Offset: 0, FormatOffset: 2, Count: 1, Value: 1}},
			wantMem:   []byte{1},
		},
		{
			name:      "count conversion",
			format:    "%255c%1$hhn",
			args:      []uintptr{0},
			initial:   []byte{0},
			wantCount: 255,
			wantWrite: []Write{{Arg: 1, Address: 0, Offset: 0, FormatOffset: 5, Count: 255, Value: 255}},
			wantMem:   []byte{255},
		},
		{
			name:      "target value wraps at 256",
			format:    "%256c%1$hhn",
			args:      []uintptr{0},
			initial:   []byte{0xaa},
			wantCount: 256,
			wantWrite: []Write{{Arg: 1, Address: 0, Offset: 0, FormatOffset: 5, Count: 256, Value: 0}},
			wantMem:   []byte{0},
		},
		{
			name:      "target value wraps at 257",
			format:    "%257c%1$hhn",
			args:      []uintptr{0},
			initial:   []byte{0xaa},
			wantCount: 257,
			wantWrite: []Write{{Arg: 1, Address: 0, Offset: 0, FormatOffset: 5, Count: 257, Value: 1}},
			wantMem:   []byte{1},
		},
		{
			name:      "writing the current value leaves it unchanged",
			format:    "%7c%1$hhn",
			args:      []uintptr{0},
			initial:   []byte{7},
			wantCount: 7,
			wantWrite: []Write{{Arg: 1, Address: 0, Offset: 0, FormatOffset: 3, Count: 7, Value: 7}},
			wantMem:   []byte{7},
		},
		{
			name:      "same address arguments are distinct parameters",
			format:    "%1$hhn%3c%2$hhn",
			args:      []uintptr{0, 0},
			initial:   []byte{0xaa},
			wantCount: 3,
			wantWrite: []Write{
				{Arg: 1, Address: 0, Offset: 0, FormatOffset: 0, Count: 0, Value: 0},
				{Arg: 2, Address: 0, Offset: 0, FormatOffset: 9, Count: 3, Value: 3},
			},
			wantMem: []byte{3},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := bytes.Clone(test.initial)
			result, err := ReplayString(test.format, test.args, test.initial)
			if err != nil {
				t.Fatalf("ReplayString() error = %v", err)
			}
			if !bytes.Equal(test.initial, original) {
				t.Fatalf("ReplayString() modified the caller's initial memory")
			}
			if result.FinalCount != test.wantCount {
				t.Fatalf("FinalCount = %d, want %d", result.FinalCount, test.wantCount)
			}
			if !bytes.Equal(result.FinalMemory, test.wantMem) {
				t.Fatalf("FinalMemory = % x, want % x", result.FinalMemory, test.wantMem)
			}
			if len(result.Writes) != len(test.wantWrite) {
				t.Fatalf("len(Writes) = %d, want %d", len(result.Writes), len(test.wantWrite))
			}
			for i := range test.wantWrite {
				if result.Writes[i] != test.wantWrite[i] {
					t.Fatalf("Writes[%d] = %+v, want %+v", i, result.Writes[i], test.wantWrite[i])
				}
			}
		})
	}
}

func TestReplayRejectsStrictlyAndAtomically(t *testing.T) {
	tooManyArgs := make([]uintptr, MaxArgs+1)
	overflowedMemory := make([]byte, MaxMemoryBytes)

	tests := []struct {
		name    string
		format  string
		args    []uintptr
		initial []byte
	}{
		{name: "unknown s", format: "%s"},
		{name: "unknown x", format: "%x"},
		{name: "unknown n", format: "%n"},
		{name: "unknown hn", format: "%1$hn", args: []uintptr{0}},
		{name: "unknown n with argument", format: "%1$n", args: []uintptr{0}},
		{name: "missing count", format: "%c"},
		{name: "bare percent", format: "%"},
		{name: "incomplete count", format: "%1"},
		{name: "incomplete hhn", format: "%1$hh", args: []uintptr{0}},
		{name: "zero count", format: "%0c"},
		{name: "leading zero count", format: "%01c"},
		{name: "multiple leading zeros", format: "%00c"},
		{name: "zero argument", format: "%0$hhn", args: []uintptr{0}},
		{name: "leading zero argument", format: "%01$hhn", args: []uintptr{0}},
		{name: "argument out of range", format: "%1$hhn"},
		{name: "second argument out of range", format: "%2$hhn", args: []uintptr{0}},
		{name: "argument address outside memory", format: "%1$hhn", args: []uintptr{MaxMemoryBytes}},
		{name: "duplicate argument write", format: "%1$hhn%1$hhn", args: []uintptr{0}},
		{name: "non-printable low", format: "abc\x1f"},
		{name: "non-printable high", format: "abc\x7f"},
		{name: "non-printable after count", format: "%1c\x80"},
		{name: "huge count", format: "%999999999999999999999999999999999c"},
		{name: "count overflow", format: "%4097c"},
		{name: "overflow after escape", format: "%4096c%%"},
		{name: "too many arguments", format: "", args: tooManyArgs},
		{name: "empty memory", format: "", initial: []byte{}},
		{name: "memory too large", format: "", initial: make([]byte, MaxMemoryBytes+1)},
		{name: "write then unknown conversion", format: "%1$hhn%s", args: []uintptr{0}, initial: overflowedMemory},
		{name: "write then duplicate", format: "%1$hhn%1$hhn", args: []uintptr{0}, initial: overflowedMemory},
		{name: "write then overflow", format: "%4096c%1$hhn%%", args: []uintptr{0}, initial: overflowedMemory},
		{name: "write then non-printable", format: "%1$hhn\x01", args: []uintptr{0}, initial: overflowedMemory},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initial := test.initial
			if initial == nil {
				initial = defaultMemory()
			}
			for i := range initial {
				initial[i] = byte(0xa0 + i)
			}
			original := bytes.Clone(initial)

			result, err := ReplayString(test.format, test.args, initial)
			if err == nil {
				t.Fatalf("ReplayString(%q) succeeded, want an error", test.format)
			}
			if result != nil {
				t.Fatalf("ReplayString(%q) returned a result on error", test.format)
			}
			if !bytes.Equal(initial, original) {
				t.Fatalf("ReplayString(%q) committed a partial write", test.format)
			}
		})
	}
}

func TestReplayCountBoundary(t *testing.T) {
	t.Run("4096 ordinary bytes", func(t *testing.T) {
		result, err := ReplayString(strings.Repeat(" ", MaxCount), nil, defaultMemory())
		if err != nil {
			t.Fatalf("ReplayString() error = %v", err)
		}
		if result.FinalCount != MaxCount {
			t.Fatalf("FinalCount = %d, want %d", result.FinalCount, MaxCount)
		}
	})

	t.Run("4096 count conversion", func(t *testing.T) {
		result, err := ReplayString("%4096c", nil, defaultMemory())
		if err != nil {
			t.Fatalf("ReplayString() error = %v", err)
		}
		if result.FinalCount != MaxCount {
			t.Fatalf("FinalCount = %d, want %d", result.FinalCount, MaxCount)
		}
	})

	t.Run("4095 plus escape", func(t *testing.T) {
		result, err := ReplayString("%4095c%%", nil, defaultMemory())
		if err != nil {
			t.Fatalf("ReplayString() error = %v", err)
		}
		if result.FinalCount != MaxCount {
			t.Fatalf("FinalCount = %d, want %d", result.FinalCount, MaxCount)
		}
	})

	t.Run("write at 4096", func(t *testing.T) {
		result, err := ReplayString("%4096c%1$hhn", []uintptr{0}, []byte{0xaa})
		if err != nil {
			t.Fatalf("ReplayString() error = %v", err)
		}
		if result.FinalCount != MaxCount {
			t.Fatalf("FinalCount = %d, want %d", result.FinalCount, MaxCount)
		}
		if result.FinalMemory[0] != 0 {
			t.Fatalf("written value = 0x%02x, want 0x00", result.FinalMemory[0])
		}
	})

	t.Run("two zero-output writes at 4096", func(t *testing.T) {
		result, err := ReplayString("%4096c%1$hhn%2$hhn", []uintptr{0, 1}, defaultMemory())
		if err != nil {
			t.Fatalf("ReplayString() error = %v", err)
		}
		if result.FinalCount != MaxCount {
			t.Fatalf("FinalCount = %d, want %d", result.FinalCount, MaxCount)
		}
		if len(result.Writes) != 2 {
			t.Fatalf("len(Writes) = %d, want 2", len(result.Writes))
		}
	})

	t.Run("4097 ordinary bytes rejected", func(t *testing.T) {
		initial := defaultMemory()
		original := bytes.Clone(initial)
		if _, err := ReplayString(strings.Repeat(" ", MaxCount+1), nil, initial); err == nil {
			t.Fatal("ReplayString() succeeded at 4097, want overflow error")
		}
		if !bytes.Equal(initial, original) {
			t.Fatal("memory changed after overflow")
		}
	})

	t.Run("4097 count conversion rejected", func(t *testing.T) {
		if _, err := ReplayString("%4097c", nil, defaultMemory()); err == nil {
			t.Fatal("ReplayString() succeeded with 4097, want overflow error")
		}
	})
}

func TestReplayAtBaseAddress(t *testing.T) {
	const base = uintptr(0x1000)
	initial := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	result, err := ReplayAt(base, []byte("%1$hhn"), []uintptr{base + 3}, initial)
	if err != nil {
		t.Fatalf("ReplayAt() error = %v", err)
	}
	if result.Writes[0].Offset != 3 || result.Writes[0].Address != base+3 {
		t.Fatalf("write = %+v", result.Writes[0])
	}
	if !bytes.Equal(result.FinalMemory, []byte{0xaa, 0xbb, 0xcc, 0}) {
		t.Fatalf("FinalMemory = % x", result.FinalMemory)
	}

	for _, address := range []uintptr{base - 1, base + 4} {
		initial := []byte{1, 2, 3, 4}
		original := bytes.Clone(initial)
		if _, err := ReplayAt(base, []byte("%1$hhn"), []uintptr{address}, initial); err == nil {
			t.Fatalf("address 0x%x was accepted outside memory", address)
		}
		if !bytes.Equal(initial, original) {
			t.Fatalf("address 0x%x changed memory on error", address)
		}
	}
}
