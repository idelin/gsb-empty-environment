package netsec

import (
	"testing"
)

func TestParseAndWireRoundTrip(t *testing.T) {
	cases := []string{
		".",
		"example.com.",
		"host.example.com",
		"*.sub.example.com.",
		"a.b.c.d.e.example.",
		"UPPER.Example.COM.",
	}
	for _, in := range cases {
		n, err := ParseName(in)
		if err != nil {
			t.Fatalf("ParseName(%q): %v", in, err)
		}
		wire, err := n.WireEncode()
		if err != nil {
			t.Fatalf("WireEncode(%q): %v", in, err)
		}
		back, err := ParseWireFormatName(wire)
		if err != nil {
			t.Fatalf("ParseWireFormatName(%q): %v", in, err)
		}
		if !back.Equal(n) {
			t.Fatalf("round trip mismatch: %s != %s", back, n)
		}
	}
}

func TestParseNameLowercases(t *testing.T) {
	n := MustParseName("Host.Example.COM.")
	want := MustParseName("host.example.com.")
	if !n.Equal(want) {
		t.Fatalf("got %s, want %s", n, want)
	}
}

func TestEffectiveLabelCount(t *testing.T) {
	cases := []struct {
		name string
		want int
	}{
		{".", 0},
		{"example.com.", 2},
		{"host.example.com.", 3},
		{"*.example.com.", 2}, // leading "*" not counted
		{"*.sub.example.com.", 3},
	}
	for _, tc := range cases {
		if got := MustParseName(tc.name).EffectiveLabelCount(); got != tc.want {
			t.Errorf("EffectiveLabelCount(%s) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestParentAndSubDomain(t *testing.T) {
	host := MustParseName("a.b.example.com.")
	if got := host.Parent(); !got.Equal(MustParseName("b.example.com.")) {
		t.Fatalf("parent = %s", got)
	}
	if !host.IsSubDomain(MustParseName("example.com.")) {
		t.Fatal("host should be subdomain of example.com")
	}
	if host.IsSubDomain(MustParseName("example.org.")) {
		t.Fatal("host must not be subdomain of example.org")
	}
	if !MustParseName("example.com.").IsSubDomain(MustParseName("example.com.")) {
		t.Fatal("name should be its own subdomain")
	}
}

func TestStringForm(t *testing.T) {
	if got := MustParseName(".").String(); got != "." {
		t.Fatalf("root string = %q", got)
	}
	if got := MustParseName("host.example.com").String(); got != "host.example.com." {
		t.Fatalf("string = %q", got)
	}
}

func TestParseNameErrors(t *testing.T) {
	bad := []string{"a..b.example.", "a\\", "a\\999.example."}
	for _, in := range bad {
		if _, err := ParseName(in); err == nil {
			t.Fatalf("ParseName(%q) expected error", in)
		}
	}
}

func TestParseWireFormatRejectsCompression(t *testing.T) {
	// 0xC0 0x00 is a compression pointer.
	if _, err := ParseWireFormatName([]byte{0xC0, 0x00}); err == nil {
		t.Fatal("expected error for compressed name")
	}
	if _, err := ParseWireFormatName([]byte{0x02, 'a', 'b'}); err == nil {
		t.Fatal("expected error for truncated name")
	}
}

func TestClosestEncloserAndWildcard(t *testing.T) {
	n := MustParseName("a.b.sub.example.com.")
	encloser, err := n.ClosestEncloser(3)
	if err != nil {
		t.Fatalf("encloser: %v", err)
	}
	if !encloser.Equal(MustParseName("sub.example.com.")) {
		t.Fatalf("encloser = %s", encloser)
	}
	wild := encloser.PrependWildcard()
	if !wild.Equal(MustParseName("*.sub.example.com.")) {
		t.Fatalf("wildcard = %s", wild)
	}
	if _, err := n.ClosestEncloser(99); err == nil {
		t.Fatal("expected out-of-range encloser error")
	}
}
