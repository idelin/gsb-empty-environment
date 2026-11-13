package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestEscapeDNComponent(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"comma and plus", "Doe, John+ACME", `Doe\, John\+ACME`},
		{"RFC 4514 specials", `a"b;c<d>e\f`, `a\"b\;c\<d\>e\\f`},
		{"leading space", " #root", `\ #root`},
		{"leading hash", "#root", `\#root`},
		{"trailing space", "root ", `root\ `},
		{"control bytes", "line\nbreak\x00", `line\0Abreak\00`},
		{"UTF-8 unchanged", "张三", "张三"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EscapeDNComponent(tc.value); got != tc.want {
				t.Errorf("EscapeDNComponent(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestNormalizeDistinguishedName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "comma and plus components",
			input: `CN=Doe\, John+O=ACME\, Inc.,OU=Research\+Dev,C=US`,
			want:  `CN=Doe\, John+O=ACME\, Inc.,OU=Research\+Dev,C=US`,
		},
		{
			name:  "canonicalizes hex escape and bare special",
			input: `CN=Doe\2C John,O=ACME`,
			want:  `CN=Doe\, John,O=ACME`,
		},
		{
			name:  "canonicalizes leading and trailing spaces",
			input: `CN= leading, O=trailing `,
			want:  `CN=\ leading,O=trailing`,
		},
		{
			name:  "quoted value",
			input: `CN="Doe, John",O=ACME`,
			want:  `CN=Doe\, John,O=ACME`,
		},
		{
			name:  "command syntax is data",
			input: "CN=peer$(touch x)+O=ACME\\, Inc.",
			want:  "CN=peer$(touch x)+O=ACME\\, Inc.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizePeerIdentity(tc.input)
			if err != nil {
				t.Fatalf("NormalizePeerIdentity: %v", err)
			}
			if got != tc.want {
				t.Errorf("NormalizePeerIdentity(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeDNRejectsEncodedControlCharacter(t *testing.T) {
	for _, input := range []string{`CN=line\0Abreak`, `CN=nul\00value`, `CN=del\7Fvalue`} {
		if _, err := NormalizePeerIdentity(input); !errors.Is(err, ErrPeerIdentityControlCharacter) {
			t.Errorf("NormalizePeerIdentity(%q) error = %v, want ErrPeerIdentityControlCharacter", input, err)
		}
	}
}

func TestIdentityControlsAndLength(t *testing.T) {
	for _, identity := range []string{"peer\x00.example", "peer\nexample", "peer\texample", "peer\x7f.example"} {
		if _, err := NormalizePeerIdentity(identity); !errors.Is(err, ErrPeerIdentityControlCharacter) {
			t.Errorf("NormalizePeerIdentity(%q) error = %v", identity, err)
		}
	}

	identity256 := strings.Repeat("a", MaxPeerIdentityBytes)
	got, err := NormalizePeerIdentity(identity256)
	if err != nil {
		t.Fatalf("256-byte identity rejected: %v", err)
	}
	if got != identity256 {
		t.Errorf("non-DN identity was changed")
	}
	if _, err := NormalizePeerIdentity(identity256 + "a"); !errors.Is(err, ErrPeerIdentityTooLong) {
		t.Errorf("257-byte error = %v, want ErrPeerIdentityTooLong", err)
	}
}

func TestNormalizeIdentityRejectsEscapedOverflow(t *testing.T) {
	input := "CN=" + strings.Repeat(";", MaxPeerIdentityBytes-3)
	if len(input) != MaxPeerIdentityBytes {
		t.Fatalf("test input length = %d, want %d", len(input), MaxPeerIdentityBytes)
	}
	if _, err := NormalizePeerIdentity(input); !errors.Is(err, ErrPeerIdentityTooLong) {
		t.Errorf("normalized overflow error = %v, want ErrPeerIdentityTooLong", err)
	}
}

func TestNormalizeIdentityRejectsMalformedDN(t *testing.T) {
	for _, input := range []string{`CN=unterminated\`, `CN=one,broken`, `CN=one+broken`, `CN=` + "a" + `"b`} {
		if _, err := NormalizePeerIdentity(input); !errors.Is(err, ErrInvalidDistinguishedName) {
			t.Errorf("NormalizePeerIdentity(%q) error = %v, want ErrInvalidDistinguishedName", input, err)
		}
	}
}
