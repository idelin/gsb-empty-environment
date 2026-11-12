package netsec

import "testing"

func TestPunycodeKnownVectors(t *testing.T) {
	cases := []struct {
		unicode string
		ascii   string
	}{
		{"", ""},
		{"a", "a-"},
		{"a-", "a--"},
		{"-a", "-a-"},
		{"123", "123-"},
		{"bücher", "bcher-kva"},
		{"münchen", "mnchen-3ya"},
		{"bcher", "bcher-"},
		{"日本語", "wgv71a119e"},
		{"中文", "fiq228c"},
		{"☃", "n3h"},
		{"☃-⌘", "--dqo34k"},
		{"ドメイン", "eckwd4c7c"},
	}
	for _, tc := range cases {
		got := punyEncode([]rune(tc.unicode))
		if got != tc.ascii {
			t.Errorf("punyEncode(%q) = %q, want %q", tc.unicode, got, tc.ascii)
		}
		decoded, err := punyDecode(tc.ascii)
		if err != nil {
			t.Errorf("punyDecode(%q) error: %v", tc.ascii, err)
			continue
		}
		if string(decoded) != tc.unicode {
			t.Errorf("punyDecode(%q) = %q, want %q", tc.ascii, string(decoded), tc.unicode)
		}
	}
}

func TestPunyDecodeRejectsGarbage(t *testing.T) {
	for _, in := range []string{"0", "!", "a-0", "zzzzz99999"} {
		if _, err := punyDecode(in); err == nil {
			t.Errorf("punyDecode(%q) expected error, got nil", in)
		}
	}
}

func TestCanonicalHost(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercase", "example.com", "example.com"},
		{"uppercase", "Example.COM", "example.com"},
		{"trailing dot", "example.com.", "example.com"},
		{"uppercase + trailing dot", "EXAMPLE.com.", "example.com"},
		{"wildcard pattern", "*.Example.COM.", "*.example.com"},
		{"idn u-label", "bücher.de", "xn--bcher-kva.de"},
		{"idn uppercase", "BÜCHER.DE", "xn--bcher-kva.de"},
		{"mixed japanese label", "日本語.jp", "xn--wgv71a119e.jp"},
		{"already punycode", "xn--MNCHEN-3ya.de.", "xn--mnchen-3ya.de"},
		{"ipv4", "127.0.0.1", "127.0.0.1"},
		{"ipv6 uppercase", "2001:DB8::1", "2001:db8::1"},
		{"ipv6 zone", "fe80::1%lo0", "fe80::1"},
		{"single label idn", "☃", "xn--n3h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalHost(tc.in)
			if err != nil {
				t.Fatalf("canonicalHost(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("canonicalHost(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalHostErrors(t *testing.T) {
	bad := []string{
		"",
		".",
		"a..b",
		"xn--0",
		"xn--zzzzz99999.example",
	}
	for _, in := range bad {
		if _, err := canonicalHost(in); err == nil {
			t.Errorf("canonicalHost(%q) expected error, got nil", in)
		}
	}
}

func TestHostOnly(t *testing.T) {
	cases := map[string]string{
		"example.com:443":      "example.com",
		"example.com":          "example.com",
		"[2001:db8::1]:8443":   "2001:db8::1",
		"[2001:db8::1]":        "2001:db8::1",
		"xn--bcher-kva.de:443": "xn--bcher-kva.de",
	}
	for in, want := range cases {
		if got := hostOnly(in); got != want {
			t.Errorf("hostOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
