package netsec

import (
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestClassifyEffectiveTarget(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAddr   string
		wantFamily Family
		wantCat    Category
		wantMech   Mechanism
		wantPublic bool
	}{
		{
			name:       "mapped metadata address",
			input:      "::ffff:169.254.169.254",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismIPv4Mapped,
		},
		{
			name:       "mapped hexadecimal metadata address",
			input:      "::ffff:a9fe:a9fe",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismIPv4Mapped,
		},
		{
			name:       "compatible metadata address",
			input:      "::169.254.169.254",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismIPv4Compatible,
		},
		{
			name:       "compatible hexadecimal metadata address",
			input:      "::a9fe:a9fe",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismIPv4Compatible,
		},
		{
			name:       "translated metadata address",
			input:      "::ffff:0:169.254.169.254",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismIPv4Translated,
		},
		{
			name:       "six-to-four metadata address",
			input:      "2002:a9fe:a9fe::1",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   Mechanism6to4,
		},
		{
			name:       "six-to-four loopback address",
			input:      "2002:7f00:1::",
			wantAddr:   "127.0.0.1",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLoopback,
			wantMech:   Mechanism6to4,
		},
		{
			name:       "six-to-four private address",
			input:      "2002:0a00:0001::",
			wantAddr:   "10.0.0.1",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryPrivate,
			wantMech:   Mechanism6to4,
		},
		{
			name:       "six-to-four public address",
			input:      "2002:0808:0808::",
			wantAddr:   "8.8.8.8",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryPublic,
			wantMech:   Mechanism6to4,
			wantPublic: true,
		},
		{
			name:       "teredo link-local client",
			input:      "2001:0:4136:e378:8000:63bf:5601:5601",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismTeredo,
		},
		{
			name:       "teredo documentation client",
			input:      "2001:0:4136:e378:8000:63bf:3fff:fdd2",
			wantAddr:   "192.0.2.45",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryDocumentation,
			wantMech:   MechanismTeredo,
		},
		{
			name:       "teredo public client",
			input:      "2001:0:4136:e378:8000:63bf:f7f7:f7f7",
			wantAddr:   "8.8.8.8",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryPublic,
			wantMech:   MechanismTeredo,
			wantPublic: true,
		},
		{
			name:       "nat64 well-known prefix metadata address",
			input:      "64:ff9b::169.254.169.254",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismNAT64,
		},
		{
			name:       "nat64 well-known prefix public address",
			input:      "64:ff9b::808:808",
			wantAddr:   "8.8.8.8",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryPublic,
			wantMech:   MechanismNAT64,
			wantPublic: true,
		},
		{
			name:       "nat64 local-use prefix metadata address",
			input:      "64:ff9b:1:a9fe:a9:fe00:0:0",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismNAT64,
		},
		{
			name:       "nat64 local-use prefix public address",
			input:      "64:ff9b:1:808:8:800:0:0",
			wantAddr:   "8.8.8.8",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryPublic,
			wantMech:   MechanismNAT64,
			wantPublic: true,
		},
		{
			name:       "isatap metadata address",
			input:      "2001:4860::5efe:169.254.169.254",
			wantAddr:   "169.254.169.254",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryLinkLocal,
			wantMech:   MechanismISATAP,
		},
		{
			name:       "mapped public address",
			input:      "::ffff:8.8.8.8",
			wantAddr:   "8.8.8.8",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryPublic,
			wantMech:   MechanismIPv4Mapped,
			wantPublic: true,
		},
		{
			name:       "compatible public address",
			input:      "::8.8.8.8",
			wantAddr:   "8.8.8.8",
			wantFamily: FamilyIPv4,
			wantCat:    CategoryPublic,
			wantMech:   MechanismIPv4Compatible,
			wantPublic: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseAddress(test.input)
			if err != nil {
				t.Fatalf("ParseAddress(%q) error = %v", test.input, err)
			}
			if got.Address.String() != test.wantAddr {
				t.Errorf("Address = %s, want %s", got.Address, test.wantAddr)
			}
			if got.Family != test.wantFamily {
				t.Errorf("Family = %s, want %s", got.Family, test.wantFamily)
			}
			if got.Normalized != got.Address {
				t.Errorf("Normalized = %s, want Address %s", got.Normalized, got.Address)
			}
			if got.Category != test.wantCat {
				t.Errorf("Category = %s, want %s", got.Category, test.wantCat)
			}
			if got.Scope != got.Category {
				t.Errorf("Scope = %s, want Category %s", got.Scope, got.Category)
			}
			if got.Mechanism != test.wantMech {
				t.Errorf("Mechanism = %s, want %s", got.Mechanism, test.wantMech)
			}
			if got.Public != test.wantPublic {
				t.Errorf("Public = %v, want %v", got.Public, test.wantPublic)
			}
			if got.Internal == got.Public {
				t.Errorf("Internal and Public should be opposites: %+v", got)
			}
		})
	}
}

func TestClassifyDecimalAndOctal(t *testing.T) {
	tests := []struct {
		input    string
		wantAddr string
		wantCat  Category
	}{
		{"2852039166", "169.254.169.254", CategoryLinkLocal},
		{"0251.0376.0251.0376", "169.254.169.254", CategoryLinkLocal},
		{"0xA9.0xFE.0xA9.0xFE", "169.254.169.254", CategoryLinkLocal},
		{"169.254.0xA9FE", "169.254.169.254", CategoryLinkLocal},
		{"0xA9FEA9FE", "169.254.169.254", CategoryLinkLocal},
		{"0177.0.0.1", "127.0.0.1", CategoryLoopback},
		{"0177.1", "127.0.0.1", CategoryLoopback},
		{"134744072", "8.8.8.8", CategoryPublic},
		{"0251.0376.0251.0376:443", "169.254.169.254", CategoryLinkLocal},
		{"::ffff:0251.0376.0251.0376", "169.254.169.254", CategoryLinkLocal},
		{"::ffff:2852039166", "169.254.169.254", CategoryLinkLocal},
		{"::ffff:0xA9.0xFE.0xA9.0xFE", "169.254.169.254", CategoryLinkLocal},
		{"::ffff:0xA9FEA9FE", "169.254.169.254", CategoryLinkLocal},
		{"::ffff:0177.0.0.1", "127.0.0.1", CategoryLoopback},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseAddress(test.input)
			if err != nil {
				t.Fatalf("ParseAddress(%q) error = %v", test.input, err)
			}
			if got.Address.String() != test.wantAddr {
				t.Errorf("Address = %s, want %s", got.Address, test.wantAddr)
			}
			if got.Category != test.wantCat {
				t.Errorf("Category = %s, want %s", got.Category, test.wantCat)
			}
		})
	}
}

func TestEmbeddedIPv4SpellingsInIPv6(t *testing.T) {
	tests := []struct {
		input    string
		wantAddr string
		wantMech Mechanism
		wantCat  Category
	}{
		{"::ffff:0251.0376.0251.0376", "169.254.169.254", MechanismIPv4Mapped, CategoryLinkLocal},
		{"::ffff:2852039166", "169.254.169.254", MechanismIPv4Mapped, CategoryLinkLocal},
		{"::ffff:0xA9FEA9FE", "169.254.169.254", MechanismIPv4Mapped, CategoryLinkLocal},
		{"::ffff:0:0251.0376.0251.0376", "169.254.169.254", MechanismIPv4Translated, CategoryLinkLocal},
		{"::0251.0376.0251.0376", "169.254.169.254", MechanismIPv4Compatible, CategoryLinkLocal},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseAddress(test.input)
			if err != nil {
				t.Fatalf("ParseAddress(%q) error = %v", test.input, err)
			}
			if got.Address.String() != test.wantAddr {
				t.Errorf("Address = %s, want %s", got.Address, test.wantAddr)
			}
			if got.Mechanism != test.wantMech {
				t.Errorf("Mechanism = %s, want %s", got.Mechanism, test.wantMech)
			}
			if got.Category != test.wantCat {
				t.Errorf("Category = %s, want %s", got.Category, test.wantCat)
			}
		})
	}
}

func TestClassifyZoneSuffixes(t *testing.T) {
	tests := []struct {
		input    string
		wantAddr string
		wantZone string
		wantCat  Category
	}{
		{"fe80::1%en0", "fe80::1", "en0", CategoryLinkLocal},
		{"[fe80::1%25en0]:8080", "fe80::1", "en0", CategoryLinkLocal},
		{"::ffff:169.254.169.254%eth0", "169.254.169.254", "eth0", CategoryLinkLocal},
		{"::169.254.169.254%eth0", "169.254.169.254", "eth0", CategoryLinkLocal},
		{"2002:a9fe:a9fe::1%eth0", "169.254.169.254", "eth0", CategoryLinkLocal},
		{"2001:0:4136:e378:8000:63bf:5601:5601%eth0", "169.254.169.254", "eth0", CategoryLinkLocal},
		{"2001:4860:4860::8888%en0", "2001:4860:4860::8888", "en0", CategoryPublic},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseAddress(test.input)
			if err != nil {
				t.Fatalf("ParseAddress(%q) error = %v", test.input, err)
			}
			if got.Address.String() != test.wantAddr {
				t.Errorf("Address = %s, want %s", got.Address, test.wantAddr)
			}
			if got.Zone != test.wantZone {
				t.Errorf("Zone = %q, want %q", got.Zone, test.wantZone)
			}
			if got.Category != test.wantCat {
				t.Errorf("Category = %s, want %s", got.Category, test.wantCat)
			}
		})
	}
}

func TestClassifyRanges(t *testing.T) {
	tests := []struct {
		input string
		want  Category
	}{
		{"0.0.0.0", CategoryUnspecified},
		{"255.255.255.255", CategoryReserved},
		{"10.1.2.3", CategoryPrivate},
		{"172.16.0.1", CategoryPrivate},
		{"172.31.255.254", CategoryPrivate},
		{"192.168.1.1", CategoryPrivate},
		{"100.64.0.1", CategoryShared},
		{"127.0.0.1", CategoryLoopback},
		{"169.254.1.1", CategoryLinkLocal},
		{"192.0.2.1", CategoryDocumentation},
		{"198.51.100.1", CategoryDocumentation},
		{"203.0.113.1", CategoryDocumentation},
		{"198.18.0.1", CategoryBenchmark},
		{"198.19.255.255", CategoryBenchmark},
		{"224.0.0.1", CategoryMulticast},
		{"239.255.255.255", CategoryMulticast},
		{"240.0.0.1", CategoryReserved},
		{"192.0.0.1", CategoryReserved},
		{"192.0.0.8", CategoryReserved},
		{"192.0.0.9", CategoryPublic},
		{"192.0.0.10", CategoryPublic},
		{"192.31.196.1", CategoryPublic},
		{"8.8.8.8", CategoryPublic},
		{"1.1.1.1", CategoryPublic},
		{"::", CategoryUnspecified},
		{"::1", CategoryLoopback},
		{"100::1", CategoryDiscard},
		{"2001::1", CategoryReserved},
		{"2001:2::1", CategoryBenchmark},
		{"2001:db8::1", CategoryDocumentation},
		{"2001:db8::ffff:169.254.169.254", CategoryDocumentation},
		{"2001:db8::5efe:8.8.8.8", CategoryDocumentation},
		{"2001:1::1", CategoryPublic},
		{"2001:3::1", CategoryPublic},
		{"2001:4860:4860::8888", CategoryPublic},
		{"2001:470::1", CategoryPublic},
		{"2606:4700:4700::1111", CategoryPublic},
		{"2a00::1", CategoryPublic},
		{"3fff::1", CategoryDocumentation},
		{"3ffe::1", CategoryReserved},
		{"5f00::1", CategoryReserved},
		{"fc00::1", CategoryUniqueLocal},
		{"fe80::1", CategoryLinkLocal},
		{"fe80::5efe:8.8.8.8", CategoryLinkLocal},
		{"fe80::5efe:169.254.169.254", CategoryLinkLocal},
		{"fec0::1", CategorySiteLocal},
		{"ff02::1", CategoryMulticast},
		{"4000::1", CategoryReserved},
		{"0200::1", CategoryReserved},
	}

	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseAddress(test.input)
			if err != nil {
				t.Fatalf("ParseAddress(%q) error = %v", test.input, err)
			}
			if got.Category != test.want {
				t.Errorf("Category = %s, want %s (%s)", got.Category, test.want, got.Reason)
			}
			if test.want == CategoryPublic && !got.Public {
				t.Errorf("Public = false, want true")
			}
			if test.want != CategoryPublic && got.Public {
				t.Errorf("Public = true, want false")
			}
		})
	}
}

func TestInvalidAddresses(t *testing.T) {
	inputs := []string{
		"",
		"1.2.3.256",
		"1.2.3.4.5",
		"08.0.0.1",
		"0x",
		"::gg",
		"[::1",
		"1.2.3.4:99999",
		"1.2.3.4:not-a-port",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			got, err := ParseAddress(input)
			if err == nil {
				t.Fatalf("ParseAddress(%q) = %+v, want error", input, got)
			}
			if !errors.Is(err, ErrInvalidAddress) {
				t.Errorf("error = %v, want ErrInvalidAddress", err)
			}
			if !IsInternal(input) {
				t.Errorf("IsInternal(%q) = false, want true", input)
			}
		})
	}
}

func TestClassifyIP(t *testing.T) {
	tests := []struct {
		ip     net.IP
		want   Category
		wantIP string
	}{
		{net.ParseIP("169.254.169.254"), CategoryLinkLocal, "169.254.169.254"},
		{net.ParseIP("::ffff:169.254.169.254"), CategoryLinkLocal, "169.254.169.254"},
		{net.ParseIP("8.8.8.8"), CategoryPublic, "8.8.8.8"},
		{nil, CategoryReserved, ""},
	}
	for _, test := range tests {
		got := ClassifyIP(test.ip)
		if got.Category != test.want {
			t.Errorf("ClassifyIP(%v) category = %s, want %s", test.ip, got.Category, test.want)
		}
		if test.wantIP != "" && got.Address.String() != test.wantIP {
			t.Errorf("ClassifyIP(%v) address = %s, want %s", test.ip, got.Address, test.wantIP)
		}
	}
}

func TestNormalizeReturnsComparableAddress(t *testing.T) {
	addr, err := Normalize("::ffff:169.254.169.254")
	if err != nil {
		t.Fatal(err)
	}
	if !addr.Is4() {
		t.Fatalf("Normalize returned %s, want an IPv4 address", addr)
	}
	if !mustPrefix("169.254.0.0/16").Contains(addr) {
		t.Errorf("%s is not in the link-local prefix", addr)
	}
}

func TestTargetsForTeredo(t *testing.T) {
	got, err := ParseAddress("2001:0:4136:e378:8000:63bf:5601:5601")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Targets) != 2 {
		t.Fatalf("Targets = %v, want client and server", got.Targets)
	}
	if got.Targets[0].String() != "169.254.169.254" {
		t.Errorf("client target = %s", got.Targets[0])
	}
	if got.Targets[1].String() != "65.54.227.120" {
		t.Errorf("server target = %s", got.Targets[1])
	}
}

func TestDialAddressKeepsTransitionFamily(t *testing.T) {
	tests := []struct {
		input        string
		wantTarget   string
		wantDialAddr string
	}{
		{"::ffff:169.254.169.254", "169.254.169.254", "169.254.169.254"},
		{"::169.254.169.254", "169.254.169.254", "169.254.169.254"},
		{"2002:a9fe:a9fe::1", "169.254.169.254", "2002:a9fe:a9fe::1"},
		{"2001:0:4136:e378:8000:63bf:5601:5601", "169.254.169.254", "2001:0:4136:e378:8000:63bf:5601:5601"},
		{"64:ff9b::a9fe:a9fe", "169.254.169.254", "64:ff9b::a9fe:a9fe"},
		{"2001:4860::5efe:169.254.169.254", "169.254.169.254", "2001:4860::5efe:a9fe:a9fe"},
		{"fe80::1%eth0", "fe80::1", "fe80::1%eth0"},
		{"2001:4860:4860::8888", "2001:4860:4860::8888", "2001:4860:4860::8888"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseAddress(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Address.String() != test.wantTarget {
				t.Errorf("Address = %s, want %s", got.Address, test.wantTarget)
			}
			if got.DialAddr.String() != test.wantDialAddr {
				t.Errorf("DialAddr = %s, want %s", got.DialAddr, test.wantDialAddr)
			}
		})
	}
}

func TestPublicAndInternalHelpers(t *testing.T) {
	if !IsPublic("8.8.8.8") || IsInternal("8.8.8.8") {
		t.Error("8.8.8.8 should be public")
	}
	if IsPublic("::ffff:169.254.169.254") || !IsInternal("::ffff:169.254.169.254") {
		t.Error("mapped metadata address should be internal")
	}
	if IsPublic("not an address") || !IsInternal("not an address") {
		t.Error("parse errors must fail closed")
	}
	if err := Check("8.8.8.8"); err != nil {
		t.Errorf("Check(public) = %v", err)
	}
	if err := Check("::ffff:169.254.169.254"); !errors.Is(err, ErrBlocked) {
		t.Errorf("Check(internal) = %v, want ErrBlocked", err)
	}
	if !IsPrivate("10.0.0.1") || !IsPrivate("::ffff:10.0.0.1") || !IsPrivate("fc00::1") {
		t.Error("private helpers should detect private and unique-local targets")
	}
	if !IsLoopback("::ffff:127.0.0.1") || !IsLoopback("2002:7f00:1::") {
		t.Error("loopback helper should detect embedded loopback targets")
	}
	if !IsLinkLocal("::ffff:169.254.169.254") || !IsLinkLocal("fe80::1") {
		t.Error("link-local helper should detect link-local targets")
	}
	if !IsMulticast("224.0.0.1") || !IsMulticast("ff02::1") {
		t.Error("multicast helper should detect multicast targets")
	}
	if !IsReserved("240.0.0.1") || !IsReserved("2001::1") {
		t.Error("reserved helper should detect reserved targets")
	}
	if !IsPublicIP(net.ParseIP("8.8.8.8")) || IsInternalIP(net.ParseIP("8.8.8.8")) {
		t.Error("IP helpers should classify public addresses")
	}
	if !IsLinkLocalIP(net.ParseIP("::ffff:169.254.169.254")) {
		t.Error("IP helper should classify mapped link-local targets")
	}
}

func TestPrefixComparison(t *testing.T) {
	// The normalized target must work with ordinary netip.Prefix.Contains,
	// which is the property an outbound proxy needs.
	got, err := ParseAddress("2002:a9fe:a9fe::1")
	if err != nil {
		t.Fatal(err)
	}
	if !mustPrefix("169.254.0.0/16").Contains(got.Address) {
		t.Errorf("normalized target %s is not in 169.254.0.0/16", got.Address)
	}
	if mustPrefix("169.254.0.0/16").Contains(netip.MustParseAddr("::ffff:a9fe:a9fe")) {
		t.Error("raw IPv6 address should not match an IPv4 prefix")
	}
}
