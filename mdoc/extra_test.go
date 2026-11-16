package mdoc

import (
	"encoding/hex"
	"errors"
	"testing"
)

func TestEncodeIntegerAndReflectPaths(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"int zero", int(0), "00"},
		{"int small", int(10), "0a"},
		{"int negative", int(-100), "3863"},
		{"int64 large positive", int64(1000), "1903e8"},
		{"uint", uint(7), "07"},
		{"uint64 four byte", uint64(1 << 16), "1a00010000"},
		{"uint64 eight byte", uint64(1) << 32, "1b0000000100000000"},
		{"int64 min boundary", int64(-1), "20"},
		{"generic byte slice", []any{[]byte{0xde, 0xad}}, "8142dead"},
		{"nested generic map", map[string]any{
			"k": []any{int64(1), true, nil, "s"},
		}, "a1616b8401f5f66173"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Encode(tc.value)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if hex.EncodeToString(got) != tc.want {
				t.Fatalf("Encode = %x, want %s", got, tc.want)
			}
		})
	}

	t.Run("nil pointer encodes as null", func(t *testing.T) {
		var ptr *int
		got, err := Encode(ptr)
		if err != nil {
			t.Fatalf("Encode nil pointer: %v", err)
		}
		if hex.EncodeToString(got) != "f6" {
			t.Fatalf("nil pointer = %x, want f6", got)
		}
	})

	t.Run("struct rejected", func(t *testing.T) {
		if _, err := Encode(struct{ X int }{X: 1}); !errors.Is(err, ErrEncoding) {
			t.Fatalf("struct error = %v, want ErrEncoding", err)
		}
	})

	t.Run("channel rejected", func(t *testing.T) {
		if _, err := Encode(make(chan int)); !errors.Is(err, ErrEncoding) {
			t.Fatalf("channel error = %v, want ErrEncoding", err)
		}
	})
}

func TestVerifyCallbackConfiguration(t *testing.T) {
	f := newFixture(t)
	response := buildResponse(t, f, f.transcript1)

	t.Run("missing hash callback", func(t *testing.T) {
		v := f.verifier()
		v.Hash = nil
		err := v.Verify(response, f.transcript1, f.requested)
		if err == nil {
			t.Fatal("expected configuration error")
		}
	})

	t.Run("missing signature callback", func(t *testing.T) {
		v := f.verifier()
		v.VerifyDeviceSignature = nil
		err := v.Verify(response, f.transcript1, f.requested)
		if err == nil {
			t.Fatal("expected configuration error")
		}
	})

	t.Run("hash failure surfaced", func(t *testing.T) {
		v := f.verifier()
		v.Hash = func(string, []byte) ([]byte, error) {
			return nil, errors.New("boom")
		}
		err := v.Verify(response, f.transcript1, f.requested)
		if !errors.Is(err, ErrHashCallback) {
			t.Fatalf("err = %v, want ErrHashCallback", err)
		}
	})
}
