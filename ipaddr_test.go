package netsec

import (
	"net/netip"
	"testing"
)

func TestParseStrictIP(t *testing.T) {
	tests := []struct {
		name    string
		literal string
		want    string
		want4   bool
		wantOK  bool
	}{
		{name: "plain ipv4", literal: "192.168.1.10", want: "192.168.1.10", want4: true, wantOK: true},
		{name: "zero octet ipv4", literal: "10.0.0.1", want: "10.0.0.1", want4: true, wantOK: true},
		{name: "unspecified ipv4 parses", literal: "0.0.0.0", want: "0.0.0.0", want4: true, wantOK: true},
		{name: "max octets", literal: "255.255.255.255", want: "255.255.255.255", want4: true, wantOK: true},

		{name: "leading zero octet", literal: "010.0.0.1", wantOK: false},
		{name: "octal notation", literal: "0177.0.0.1", wantOK: false},
		{name: "hex notation", literal: "0x7f.0.0.1", wantOK: false},
		{name: "octet over 255", literal: "256.0.0.1", wantOK: false},
		{name: "three octets", literal: "1.2.3", wantOK: false},
		{name: "bare integer", literal: "2130706433", wantOK: false},
		{name: "empty", literal: "", wantOK: false},
		{name: "trailing dot", literal: "10.0.0.1.", wantOK: false},
		{name: "double dot", literal: "10..0.1", wantOK: false},
		{name: "non numeric octet", literal: "10.0.0.a", wantOK: false},
		{name: "whitespace", literal: " 10.0.0.1", wantOK: false},

		{name: "mapped ipv6 dotted form unmapped", literal: "::ffff:192.168.0.1", want: "192.168.0.1", want4: true, wantOK: true},
		{name: "mapped ipv6 with leading zero embedded rejected", literal: "::ffff:0177.0.0.1", wantOK: false},
		{name: "mapped hex form unmapped", literal: "0:0:0:0:0:ffff:7f00:1", want: "127.0.0.1", want4: true, wantOK: true},
		{name: "genuine ipv6", literal: "2001:db8::1", want: "2001:db8::1", want4: false, wantOK: true},
		{name: "ipv6 loopback", literal: "::1", want: "::1", want4: false, wantOK: true},
		{name: "ipv6 with zone rejected", literal: "fe80::1%eth0", wantOK: false},
		{name: "ipv6 zone without brackets rejected", literal: "2001:db8::1%0", wantOK: false},
		{name: "non-mapped dotted ipv6 rejected", literal: "fe80::1.2.3.4", wantOK: false},
		{name: "mapped but unparseable embedded ipv4", literal: "::ffff:256.0.0.1", wantOK: false},
		{name: "ipv6 parse error", literal: "2001:db8:::1", wantOK: false},
		{name: "garbage", literal: "not-an-ip", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, ok := parseStrictIP(tt.literal)
			if ok != tt.wantOK {
				t.Fatalf("parseStrictIP(%q) ok = %v, want %v (addr=%v)", tt.literal, ok, tt.wantOK, addr)
			}
			if !tt.wantOK {
				return
			}
			if addr.String() != tt.want {
				t.Errorf("parseStrictIP(%q) = %v, want %v", tt.literal, addr, tt.want)
			}
			if addr.Is4() != tt.want4 {
				t.Errorf("parseStrictIP(%q).Is4() = %v, want %v", tt.literal, addr.Is4(), tt.want4)
			}
			// A mapped address must always be reduced to IPv4, never left 4-in-6.
			if addr.Is4In6() {
				t.Errorf("parseStrictIP(%q) returned an unreduced 4-in-6 address %v", tt.literal, addr)
			}
		})
	}
}

func TestLooksLikeIPLiteral(t *testing.T) {
	ipLiterals := []string{
		"10.0.0.1",
		"::ffff:10.0.0.1",
		"2001:db8::1",
		"0177.0.0.1", // loose octal form: still recognised as an IP literal for ExternalName
		"0x7f.0.0.1", // loose hex form
		"010.0.0.1",  // leading-zero decimal/octal form
	}
	for _, literal := range ipLiterals {
		if !looksLikeIPLiteral(literal) {
			t.Errorf("looksLikeIPLiteral(%q) = false, want true", literal)
		}
	}

	notIPs := []string{"example.com", "foo.svc", "10.0.0.1.example.com", "", "1.2.3", "256.0.0.1"}
	for _, name := range notIPs {
		if looksLikeIPLiteral(name) {
			t.Errorf("looksLikeIPLiteral(%q) = true, want false", name)
		}
	}

	looseNotIPs := []string{
		"0xg.0.0.1",  // invalid hex digit
		"09.0.0.1",   // digit 9 is not a valid octal digit
		"0x.0.0.1",   // empty hex body
		"10..0.1",    // empty octet
		"10.0.0.1.5", // too many octets
	}
	for _, name := range looseNotIPs {
		if looksLikeLooseIPv4(name) {
			t.Errorf("looksLikeLooseIPv4(%q) = true, want false", name)
		}
	}
	looseIPs := []string{"0177.0.0.1", "0X7F.0.0.1", "010.0.0.1", "10.0.0.01", "255.255.255.255"}
	for _, name := range looseIPs {
		if !looksLikeLooseIPv4(name) {
			t.Errorf("looksLikeLooseIPv4(%q) = false, want true", name)
		}
	}

	// Sanity check: the well-known prefix value round-trips through netip.
	if _, err := netip.ParsePrefix("10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
}
