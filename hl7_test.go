package netsec

import (
	"errors"
	"testing"
)

func TestDecodeFieldRejectsEncodedMLLPDelimiters(t *testing.T) {
	tests := []string{
		`\X0B\`,
		`\x0b\`,
		`\X1C\`,
		`\x1c\`,
		`\X0B0D\`,
		`\X0D\`,
		`prefix\X1C\suffix`,
	}
	for _, raw := range tests {
		_, err := DecodeField(raw)
		if !errors.Is(err, ErrForbiddenEscape) {
			t.Fatalf("DecodeField(%q) = %v, want ErrForbiddenEscape", raw, err)
		}
		if err := ValidateStoredField(raw); !errors.Is(err, ErrForbiddenEscape) {
			t.Fatalf("ValidateStoredField(%q) = %v, want ErrForbiddenEscape", raw, err)
		}
	}
}

func TestDecodeFieldRejectsRawDelimiters(t *testing.T) {
	for _, raw := range []string{
		"abc" + string(rune(StartBlock)),
		"abc" + string(rune(EndBlock)),
		"abc" + string(rune(CarriageReturn)),
	} {
		_, err := DecodeField(raw)
		if !errors.Is(err, ErrForbiddenByte) {
			t.Fatalf("DecodeField(%q) = %v, want ErrForbiddenByte", raw, err)
		}
	}
}

func TestDecodeFieldDecodesSafeEscapes(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{`\X41\`, "A"},
		{`\X0A\`, "\n"},
		{`\F\`, "|"},
		{`\S\`, "^"},
		{`\R\`, "~"},
		{`\T\`, "&"},
		{`\E\`, `\`},
		{`a\.br\b`, `a\.br\b`},
	}

	for _, tt := range tests {
		got, err := DecodeField(tt.raw)
		if err != nil {
			t.Fatalf("DecodeField(%q): %v", tt.raw, err)
		}
		if got != tt.want {
			t.Fatalf("DecodeField(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestDecodeFieldRejectsMalformedEscapes(t *testing.T) {
	tests := []string{
		`\X0\`,
		`\XZZ\`,
		`\X41`,
		`abc\`,
		`\\`,
	}
	for _, raw := range tests {
		_, err := DecodeField(raw)
		if !errors.Is(err, ErrMalformedEscape) {
			t.Fatalf("DecodeField(%q) = %v, want ErrMalformedEscape", raw, err)
		}
	}
}

func TestValidateOutboundMessageAllowsSegmentsAndTrailingCR(t *testing.T) {
	for _, body := range [][]byte{
		[]byte("MSH|^~\\&|A|B"),
		[]byte("MSH|^~\\&|A|B\r"),
		[]byte("MSH|^~\\&|A|B\rPID|1||12345"),
		[]byte("MSH|^~\\&|A|B\rPID|1||12345\r"),
		[]byte("Z01|custom\rPID|1"),
	} {
		if err := ValidateOutboundMessage(body); err != nil {
			t.Fatalf("ValidateOutboundMessage(%q) = %v, want nil", body, err)
		}
	}
}
