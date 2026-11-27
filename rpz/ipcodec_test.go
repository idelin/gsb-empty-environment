package rpz

import (
	"net/netip"
	"testing"
)

func mustPrefix(t *testing.T, text string) netip.Prefix {
	t.Helper()
	prefix, err := ParsePrefix(text)
	if err != nil {
		t.Fatalf("ParsePrefix(%q): %v", text, err)
	}
	return prefix
}

func TestDecodeIPv4ReverseCIDR(t *testing.T) {
	cases := []struct {
		owner  string
		prefix string
	}{
		// Documented RPZ example: full four-octet reversed form.
		{"24.0.1.168.192.rpz-ip", "192.168.1.0/24"},
		// Minimal form: the single high-order octet covered by the prefix.
		{"24.1.168.192.rpz-ip", "192.168.1.0/24"},
		{"32.4.3.168.192.rpz-ip", "192.168.3.4/32"},
		{"8.10.rpz-client-ip", "10.0.0.0/8"},
	}
	for _, tc := range cases {
		labels := SplitLabels(tc.owner)
		prefix, err := DecodeIPOwner(labels[:len(labels)-1])
		if err != nil {
			t.Fatalf("DecodeIPOwner(%q): %v", tc.owner, err)
		}
		want := mustPrefix(t, tc.prefix)
		if prefix != want {
			t.Errorf("DecodeIPOwner(%q) = %s, want %s", tc.owner, prefix, want)
		}
	}
}

func TestDecodeIPv6ZZCompression(t *testing.T) {
	cases := []struct {
		owner  string
		prefix string
	}{
		// Documented zz example.
		{"48.zz.db8.2001.rpz-ip", "2001:db8::/48"},
		// Nibble-by-nibble form of the same prefix (12 nibbles for /48).
		{"48.0.0.0.0.8.b.d.0.1.0.0.2.rpz-ip", "2001:db8::/48"},
		{"32.8.b.d.0.1.0.0.2.rpz-ip", "2001:db8::/32"},
		// zz filling a middle zero run (single-nibble canonical spelling).
		{"64.zz.1.0.0.0.8.b.d.0.1.0.0.2.rpz-nsip", "2001:db8:1::/64"},
		// zz trailing compression for a short prefix; the "zz" label forces
		// IPv6 interpretation and fills the two trailing zero nibbles.
		{"16.1.0.0.2.zz.rpz-ip", "2001::/16"},
	}
	for _, tc := range cases {
		labels := SplitLabels(tc.owner)
		prefix, err := DecodeIPOwner(labels[:len(labels)-1])
		if err != nil {
			t.Fatalf("DecodeIPOwner(%q): %v", tc.owner, err)
		}
		want := mustPrefix(t, tc.prefix)
		if prefix != want {
			t.Errorf("DecodeIPOwner(%q) = %s, want %s", tc.owner, prefix, want)
		}
	}
}

func TestDecodeIPOwnerErrors(t *testing.T) {
	bad := [][]string{
		{"33", "1", "168", "192"},         // prefix too large for v4
		{"129", "zz", "db8", "2001"},      // prefix too large for v6
		{"48", "zz", "zz", "db8", "2001"}, // double zz
		{"xy", "1"},                       // non-numeric prefix
		{"24", "300", "168", "192"},       // octet out of range
		{"16", "gg", "2001"},              // non-hex nibble
		{},                                // empty owner
	}
	for _, labels := range bad {
		if _, err := DecodeIPOwner(labels); err == nil {
			t.Errorf("DecodeIPOwner(%v) expected error, got nil", labels)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	prefixes := []string{
		"192.168.1.0/24",
		"10.0.0.0/8",
		"0.0.0.0/0",
		"2001:db8::/48",
		"2001:db8::/32",
		"2001:db8:1::/64",
		"2001:2::/16",
		"2001:db8:abcd:1234::/56",
	}
	for _, text := range prefixes {
		prefix := mustPrefix(t, text)
		labels, err := EncodeIPOwner(prefix)
		if err != nil {
			t.Fatalf("EncodeIPOwner(%s): %v", text, err)
		}
		decoded, err := DecodeIPOwner(labels)
		if err != nil {
			t.Fatalf("DecodeIPOwner(%v) for %s: %v", labels, text, err)
		}
		if decoded != prefix {
			t.Errorf("round trip %s: got %s via %v", text, decoded, labels)
		}
	}
}

func TestEncodeIPv4CanonicalExample(t *testing.T) {
	labels, err := EncodeIPOwner(mustPrefix(t, "192.168.1.0/24"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"24", "0", "1", "168", "192"}
	if len(labels) != len(want) {
		t.Fatalf("got %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("got %v, want %v", labels, want)
		}
	}
}

func TestEncodeIPv6CanonicalZZ(t *testing.T) {
	labels, err := EncodeIPOwner(mustPrefix(t, "2001:db8::/48"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"48", "zz", "8", "b", "d", "0", "1", "0", "0", "2"}
	if len(labels) != len(want) {
		t.Fatalf("got %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("got %v, want %v", labels, want)
		}
	}
}

func TestLongestPrefixMatch(t *testing.T) {
	trie := newBitTrie()
	short := &Rule{Owner: "short", Prefix: mustPrefix(t, "192.168.0.0/16")}
	long := &Rule{Owner: "long", Prefix: mustPrefix(t, "192.168.1.0/24")}
	trie.Insert(short.Prefix, short)
	trie.Insert(long.Prefix, long)

	if got := trie.Lookup(mustAddr(t, "192.168.1.7")); got != long {
		t.Fatalf("expected /24 rule, got %v", got)
	}
	if got := trie.Lookup(mustAddr(t, "192.168.2.7")); got != short {
		t.Fatalf("expected /16 rule, got %v", got)
	}
	if got := trie.Lookup(mustAddr(t, "10.0.0.1")); got != nil {
		t.Fatalf("expected no match, got %v", got)
	}
}

func mustAddr(t *testing.T, text string) netip.Addr {
	t.Helper()
	addr, err := netip.ParseAddr(text)
	if err != nil {
		t.Fatal(err)
	}
	return addr
}
