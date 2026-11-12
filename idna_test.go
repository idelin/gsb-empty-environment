package netsec

import "testing"

func TestNormalizeHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"case and trailing dot", "EXAMPLE.COM.", "example.com"},
		{"multiple trailing dots", "example.com..", "example.com"},
		{"port ignored", "example.com:8443", "example.com"},
		{"port and trailing dot", "example.com.:443", "example.com"},
		{"ipv6 port ignored", "[::1]:8443", "::1"},
		{"idn to punycode", "BÜCHER.Example.", "xn--bcher-kva.example"},
		{"punycode unchanged", "XN--BCHER-KVA.EXAMPLE.", "xn--bcher-kva.example"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeHost(test.raw)
			if err != nil {
				t.Fatalf("normalizeHost(%q): %v", test.raw, err)
			}
			if got != test.want {
				t.Fatalf("normalizeHost(%q) = %q, want %q", test.raw, got, test.want)
			}
		})
	}
}

func TestPunycodeEncode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		label string
		want  string
	}{
		{"bücher", "bcher-kva"},
		{"münchen", "mnchen-3ya"},
	}

	for _, test := range tests {
		got, err := punycodeEncode(test.label)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("punycodeEncode(%q) = %q, want %q", test.label, got, test.want)
		}
	}
}
