package netsec

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad hex fixture %q: %v", s, err)
	}
	return b
}

func TestValidStreams(t *testing.T) {
	tests := []struct {
		name    string
		hex     string
		allowed []string
	}{
		{name: "nil", hex: "0408 30"},
		{name: "true", hex: "0408 54"},
		{name: "false", hex: "0408 46"},
		{name: "zero", hex: "0408 69 00"},
		{name: "positive fixnum", hex: "0408 69 06"},
		{name: "negative fixnum", hex: "0408 69 fa"},
		{name: "multi-byte fixnum", hex: "0408 69 01 7b"},
		{name: "negative multi-byte fixnum", hex: "0408 69 fe d4 fe"},
		{name: "negative two-byte fixnum", hex: "0408 69 ff 7f"},
		{name: "three-byte fixnum", hex: "0408 69 03 00 00 01"},
		{name: "raw string", hex: "0408 22 08 61 62 63"},
		{name: "ruby string with encoding ivar", hex: "0408 49 22 08 61 62 63 06 3a 06 45 54"},
		{name: "symbol", hex: "0408 3a 08 61 62 63"},
		{name: "symbol link", hex: "0408 5b 07 3a 06 61 3b 00"},
		{name: "array", hex: "0408 5b 07 69 06 69 07"},
		{name: "hash", hex: "0408 7b 06 69 06 69 07"},
		{name: "nested array", hex: "0408 5b 07 5b 06 69 06 5b 06 69 07"},
		{name: "self-referential array", hex: "0408 5b 06 40 00"},
		{name: "self-referential hash", hex: "0408 7b 06 5b 00 40 06"},
		{name: "string object link", hex: "0408 5b 07 49 22 08 61 62 63 06 3a 06 45 54 40 06"},
		{
			name:    "whitelisted object",
			hex:     "0408 6f 3a 09 54 69 6e 79 06 3a 07 40 61 69 06",
			allowed: []string{"Tiny"},
		},
		{
			name:    "object with linked class symbol",
			hex:     "0408 5b 07 3a 09 54 69 6e 79 6f 3b 00 00",
			allowed: []string{"Tiny"},
		},
		{
			name:    "self-referential ivar",
			hex:     "0408 6f 3a 09 54 69 6e 79 06 3a 07 40 61 40 00",
			allowed: []string{"Tiny"},
		},
		{
			name:    "symbol links for repeated class and ivar names",
			hex:     "0408 5b 07 6f 3a 09 54 69 6e 79 06 3a 07 40 61 69 06 6f 3b 00 06 3b 06 69 06",
			allowed: []string{"Tiny"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(mustHex(t, tc.hex), tc.allowed); err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestRejectsUserDefinedHooks(t *testing.T) {
	// u carries _dump data and would invoke _load in Ruby.
	u := "0408 75 3a 09 44 75 6d 70 07 78 79"
	// U would invoke marshal_load.
	u2 := "0408 55 3a 09 4c 6f 61 64 06 3a 07 40 61 69 06"
	// An IVar wrapper must not hide u from the type check.
	uWrapped := "0408 49 75 3a 09 44 75 6d 70 07 78 79 06 3a 06 45 54"

	for name, stream := range map[string]string{"u": u, "U": u2, "u in I": uWrapped} {
		t.Run(name, func(t *testing.T) {
			err := Validate(mustHex(t, stream), []string{"Dump", "Load"})
			if !errors.Is(err, ErrForbiddenType) {
				t.Fatalf("Validate() = %v, want ErrForbiddenType", err)
			}
		})
	}
}

func TestRejectsNonWhitelistedClass(t *testing.T) {
	stream := mustHex(t, "0408 6f 3a 09 54 69 6e 79 06 3a 07 40 61 69 06")

	err := Validate(stream, []string{"Object"})
	if !errors.Is(err, ErrClassNotAllowed) {
		t.Fatalf("Validate() = %v, want ErrClassNotAllowed", err)
	}
}

func TestRejectsForgedLinks(t *testing.T) {
	tests := []struct {
		name string
		hex  string
	}{
		{name: "forward symbol link at root", hex: "0408 3b 00"},
		{name: "forward symbol link in array", hex: "0408 5b 07 3b 00"},
		{name: "symbol link out of range", hex: "0408 5b 07 3a 06 61 3b 06"},
		{name: "forward object link at root", hex: "0408 40 00"},
		{name: "forward object link in array", hex: "0408 5b 07 40 06"},
		{name: "object link out of range", hex: "0408 5b 07 49 22 08 61 62 63 06 3a 06 45 54 40 08"},
		{name: "forward class symbol link", hex: "0408 6f 3b 00 00"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(mustHex(t, tc.hex), []string{"Tiny"})
			if !errors.Is(err, ErrInvalidReference) {
				t.Fatalf("Validate() = %v, want ErrInvalidReference", err)
			}
		})
	}
}

func TestRejectsOtherTypeCodes(t *testing.T) {
	codes := []byte{'u', 'U', 'e', 'C', 'd', 'S', 'M', 'm', 'l', 'f', 'c', '/', '}'}
	for _, code := range codes {
		t.Run(string(code), func(t *testing.T) {
			stream := []byte{4, 8, code}
			err := Validate(stream, []string{"Object"})
			if !errors.Is(err, ErrForbiddenType) {
				t.Fatalf("Validate() = %v, want ErrForbiddenType", err)
			}
		})
	}
}

func TestRejectsBadEnvelopeAndTruncation(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want error
	}{
		{name: "empty", hex: "", want: ErrInvalidHeader},
		{name: "one header byte", hex: "04", want: ErrInvalidHeader},
		{name: "wrong major", hex: "0508 30", want: ErrInvalidHeader},
		{name: "wrong minor", hex: "0409 30", want: ErrInvalidHeader},
		{name: "missing value", hex: "0408", want: ErrTruncated},
		{name: "truncated fixnum", hex: "0408 69", want: ErrTruncated},
		{name: "truncated string", hex: "0408 22 08 61", want: ErrTruncated},
		{name: "truncated array", hex: "0408 5b 07 30", want: ErrTruncated},
		{name: "truncated symbol", hex: "0408 3a 08 61", want: ErrTruncated},
		{name: "trailing byte", hex: "0408 30 30", want: ErrTrailingData},
		{name: "negative array length", hex: "0408 5b fa", want: ErrInvalidFormat},
		{name: "negative symbol length", hex: "0408 3a fa", want: ErrInvalidFormat},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(mustHex(t, tc.hex), nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Validate() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDepthBudget(t *testing.T) {
	nested := func(depth int) []byte {
		var b strings.Builder
		b.WriteString("0408")
		for i := 0; i < depth; i++ {
			b.WriteString("5b06")
		}
		b.WriteString("30")
		return mustHex(t, b.String())
	}

	if err := ValidateWithLimits(nested(3), nil, Limits{MaxDepth: 3, MaxElements: 100}); err != nil {
		t.Fatalf("depth 3 with limit 3: %v", err)
	}
	err := ValidateWithLimits(nested(4), nil, Limits{MaxDepth: 3, MaxElements: 100})
	if !errors.Is(err, ErrDepthLimit) {
		t.Fatalf("depth 4 with limit 3 = %v, want ErrDepthLimit", err)
	}
}

func TestElementBudget(t *testing.T) {
	// Three array elements plus the array itself.
	stream := mustHex(t, "0408 5b 08 30 30 30")
	if err := ValidateWithLimits(stream, nil, Limits{MaxDepth: 10, MaxElements: 4}); err != nil {
		t.Fatalf("four values with limit 4: %v", err)
	}
	err := ValidateWithLimits(stream, nil, Limits{MaxDepth: 10, MaxElements: 3})
	if !errors.Is(err, ErrElementLimit) {
		t.Fatalf("four values with limit 3 = %v, want ErrElementLimit", err)
	}

	// The declared size alone must be rejected before trying to read it.
	huge := mustHex(t, "0408 5b 03 00 00 01")
	err = ValidateWithLimits(huge, nil, Limits{MaxDepth: 10, MaxElements: 10})
	if !errors.Is(err, ErrElementLimit) {
		t.Fatalf("huge declared array = %v, want ErrElementLimit", err)
	}
}

func TestMultiByteLengthsAndLinks(t *testing.T) {
	t.Run("256-byte string", func(t *testing.T) {
		stream := append([]byte{4, 8, '"', 2, 0, 1}, bytes.Repeat([]byte{'a'}, 256)...)
		if err := Validate(stream, nil); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
	})

	t.Run("256-byte symbol", func(t *testing.T) {
		stream := append([]byte{4, 8, ':', 2, 0, 1}, bytes.Repeat([]byte{'s'}, 256)...)
		if err := Validate(stream, nil); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
	})

	t.Run("256-element array", func(t *testing.T) {
		stream := append([]byte{4, 8, '[', 2, 0, 1}, bytes.Repeat([]byte{'0'}, 256)...)
		if err := Validate(stream, nil); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
	})

	t.Run("multi-byte object link index", func(t *testing.T) {
		// ["a", "b", @1], with index 1 encoded little-endian as 02 01 00.
		stream := mustHex(t, "0408 5b 08 22 06 61 22 06 62 40 02 01 00")
		if err := Validate(stream, nil); err != nil {
			t.Fatalf("Validate() = %v, want nil", err)
		}
	})

	t.Run("multi-byte forged symbol link", func(t *testing.T) {
		// [:a, :b, ;256] where only two symbols exist. The link index must be
		// decoded as little-endian 256, not as 1.
		stream := mustHex(t, "0408 5b 08 3a 06 61 3a 06 62 3b 02 00 01")
		err := Validate(stream, nil)
		if !errors.Is(err, ErrInvalidReference) {
			t.Fatalf("Validate() = %v, want ErrInvalidReference", err)
		}
	})
}

func TestGateIsReusableAndCheck(t *testing.T) {
	gate := NewGate([]string{"Tiny"}, Limits{MaxDepth: 20, MaxElements: 100})
	ok := mustHex(t, "0408 6f 3a 09 54 69 6e 79 06 3a 07 40 61 69 06")
	bad := mustHex(t, "0408 75 00")

	for i := 0; i < 3; i++ {
		if !gate.Check(ok) {
			t.Fatal("gate rejected valid stream")
		}
		if gate.Check(bad) {
			t.Fatal("gate accepted forbidden stream")
		}
	}
}

func FuzzGateNeverPanics(f *testing.F) {
	seeds := [][]byte{
		{},
		{4, 8},
		{4, 8, '0'},
		{4, 8, 'u', 0},
		{4, 8, '[', 6, '@', 0},
		{
			4, 8, 'o', ':', 9, 'T', 'i', 'n', 'y', 6, ':', 7, '@', 'a', 'i', 6,
		},
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		gate := NewGate([]string{"Tiny"}, Limits{MaxDepth: 8, MaxElements: 64})
		_ = gate.Validate(data)
	})
}
