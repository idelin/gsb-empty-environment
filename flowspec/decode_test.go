package flowspec

import (
	"bytes"
	"errors"
	"testing"
)

// allComponentsHex exercises every component type 1..12 in canonical order.
//
//	1 dst 192.0.2.0/24 | 2 src 10.0.0.0/8 | 3 tcp | 4 port 8080
//	5 dport 80 | 6 sport 1234 | 7 icmp-type 8 | 8 icmp-code 0
//	9 tcp-flags SYN | 10 len 1500 | 11 dscp 46 | 12 fragment 0x01
const allComponentsHex = "" +
	"01 18 C0 00 02" +
	"02 08 0A" +
	"03 90 06" +
	"04 91 1F 90" +
	"05 90 50" +
	"06 91 04 D2" +
	"07 90 08" +
	"08 90 00" +
	"09 90 02" +
	"0A 91 05 DC" +
	"0B 90 2E" +
	"0C 90 01"

func TestDecodeAllComponentTypes(t *testing.T) {
	raw := mustHex(t, allComponentsHex)
	cs, err := DecodeComponents(raw, AFIPv4)
	if err != nil {
		t.Fatalf("DecodeComponents: %v", err)
	}
	if len(cs) != 12 {
		t.Fatalf("expected 12 components, got %d", len(cs))
	}

	checks := []struct {
		typ   ComponentType
		check func(*Component) bool
		what  string
	}{
		{TypeDestPrefix, func(c *Component) bool { return c.Prefix.String() == "192.0.2.0/24" }, "dst prefix"},
		{TypeSourcePrefix, func(c *Component) bool { return c.Prefix.String() == "10.0.0.0/8" }, "src prefix"},
		{TypeProtocol, func(c *Component) bool { return c.Terms[0].Value == 6 && c.Terms[0].Relation() == CmpEqual }, "tcp"},
		{TypePort, func(c *Component) bool { return c.Terms[0].Value == 8080 && c.Terms[0].Width == 2 }, "port 8080"},
		{TypeDestPort, func(c *Component) bool { return c.Terms[0].Value == 80 }, "dport 80"},
		{TypeSourcePort, func(c *Component) bool { return c.Terms[0].Value == 1234 }, "sport 1234"},
		{TypeICMPType, func(c *Component) bool { return c.Terms[0].Value == 8 }, "icmp type"},
		{TypeICMPCode, func(c *Component) bool { return c.Terms[0].Value == 0 }, "icmp code"},
		{TypeTCPFlags, func(c *Component) bool {
			return c.Terms[0].Value == 0x02 && c.Terms[0].MatchesBitmask(0x12) && !c.Terms[0].MatchesBitmask(0x10)
		}, "syn flag"},
		{TypePacketLength, func(c *Component) bool { return c.Terms[0].Value == 1500 }, "len"},
		{TypeDSCP, func(c *Component) bool { return c.Terms[0].Value == 46 }, "dscp"},
		{TypeFragment, func(c *Component) bool { return c.Terms[0].Value == 0x01 }, "fragment"},
	}

	byType := map[ComponentType]*Component{}
	for i := range cs {
		byType[cs[i].Type] = &cs[i]
	}
	for _, tc := range checks {
		c, ok := byType[tc.typ]
		if !ok {
			t.Errorf("missing component %s", tc.typ)
			continue
		}
		if !tc.check(c) {
			t.Errorf("component %s (%s) check failed: %+v", tc.typ, tc.what, c)
		}
		if len(c.Raw) == 0 {
			t.Errorf("component %s raw bytes not retained", tc.typ)
		}
	}
}

func TestCanonicalReEncodeMatchesWire(t *testing.T) {
	raw := mustHex(t, allComponentsHex)
	cs, err := DecodeComponents(raw, AFIPv4)
	if err != nil {
		t.Fatal(err)
	}
	out, err := EncodeComponents(cs, AFIPv4)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatalf("canonical re-encode mismatch\nwant %X\n got %X", raw, out)
	}
}

func TestOperatorBitPins(t *testing.T) {
	// Pin the RFC operator layout so accidental bit swaps fail loudly.
	if OpEndOfList != 0x80 || OpAnd != 0x40 || OpNot != 0x20 || OpMatch != 0x10 {
		t.Fatal("high operator bits mismatch")
	}
	if OpGreater != 0x08 || OpLess != 0x04 || OpLenMask != 0x03 {
		t.Fatal("relation/length bits mismatch")
	}
}

func TestGreaterThanAndLessThanOperators(t *testing.T) {
	// Type 10 packet-length: E + '>' + 1-byte, value 100 -> op 0x88.
	cs := mustDecodeComponents(t, "0A 88 64")
	term := cs[0].Terms[0]
	if term.Relation() != CmpGT {
		t.Fatalf("want GT, got %v (op=0x%02x)", term.Relation(), term.Raw)
	}
	if !term.MatchesNumeric(150) || term.MatchesNumeric(100) || term.MatchesNumeric(50) {
		t.Fatal("GT evaluation wrong")
	}

	// E + '<' + 1-byte, value 100 -> op 0x84.
	lt, err := DecodeTerms(mustHex(t, "84 64"))
	if err != nil {
		t.Fatal(err)
	}
	if lt[0].Relation() != CmpLT || !lt[0].MatchesNumeric(42) || lt[0].MatchesNumeric(100) {
		t.Fatal("LT evaluation wrong")
	}
}

func TestAndedPortRange(t *testing.T) {
	// dest-port > 1023 AND < 65535
	cs := mustDecodeComponents(t, "05 49 03 FF 85 FF FF")
	c := cs[0]
	if len(c.Terms) != 2 || !c.Terms[0].And {
		t.Fatalf("expected two AND-joined terms: %+v", c.Terms)
	}
	if !RuleMatchesPacket(&Rule{Components: []Component{c}}, &Packet{DstPort: 8080}) {
		t.Error("8080 should be in range")
	}
	if RuleMatchesPacket(&Rule{Components: []Component{c}}, &Packet{DstPort: 80}) {
		t.Error("80 should be out of range")
	}
}

func TestOredPortList(t *testing.T) {
	// dest-port == 80 OR == 443
	cs := mustDecodeComponents(t, "05 10 50 91 01 BB")
	c := cs[0]
	p80 := &Packet{DstPort: 80}
	p443 := &Packet{DstPort: 443}
	p8080 := &Packet{DstPort: 8080}
	r := &Rule{Components: []Component{c}}
	if !RuleMatchesPacket(r, p80) || !RuleMatchesPacket(r, p443) {
		t.Error("80/443 must match OR list")
	}
	if RuleMatchesPacket(r, p8080) {
		t.Error("8080 must not match")
	}
}

func TestIllegalOperatorMatrix(t *testing.T) {
	cases := []struct {
		name  string
		hex   string
		terms bool // decode as bare term body via DecodeTerms
		want  error
	}{
		{"reserved-length-code-3", "03 93 06", false, ErrReservedLength},
		{"no-relation-bit", "03 80 06", false, ErrOperatorNoRelation},
		{"truncated-value", "05 91 00", false, ErrTruncated},
		{"unknown-component-type", "0D 90 01", false, ErrUnknownComponent},
		{"prefix-too-long-v4", "01 21 0A 00 00", false, ErrInvalidPrefix},
		{"missing-end-of-list", "10 06 10 06", true, ErrEndOfList},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.terms {
				_, err = DecodeTerms(mustHex(t, tc.hex))
			} else {
				_, err = DecodeComponents(mustHex(t, tc.hex), AFIPv4)
			}
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want errors.Is %v, got %v", tc.want, err)
			}
			// Every operator-encoding case must also classify as illegal operator.
			if tc.want == ErrReservedLength || tc.want == ErrOperatorNoRelation || tc.want == ErrEndOfList {
				if !errors.Is(err, ErrIllegalOperator) {
					t.Fatalf("expected ErrIllegalOperator in chain, got %v", err)
				}
			}
		})
	}
}

func TestDuplicateAndOrder(t *testing.T) {
	dup := "03 90 06 03 90 11"
	if _, err := DecodeComponents(mustHex(t, dup), AFIPv4); !errors.Is(err, ErrDuplicateComponent) {
		t.Fatalf("want duplicate, got %v", err)
	}
	unordered := "03 90 06 01 18 C0 00 02"
	if _, err := DecodeComponents(mustHex(t, unordered), AFIPv4); !errors.Is(err, ErrComponentOrder) {
		t.Fatalf("want order error, got %v", err)
	}
}

func TestFramedNLRIRoundTrip(t *testing.T) {
	cs := mustDecodeComponents(t, allComponentsHex)
	r := &Rule{AF: AFIPv4, Components: cs}
	framed, err := r.EncodeNLRI()
	if err != nil {
		t.Fatal(err)
	}
	// First two octets must equal the component length.
	body := mustHex(t, allComponentsHex)
	if int(framed[0])<<8|int(framed[1]) != len(body) {
		t.Fatalf("bad NLRI length prefix: %02x%02x", framed[0], framed[1])
	}
	decoded, err := DecodeNLRI(framed, AFIPv4)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Components) != 12 {
		t.Fatalf("want 12 comps, got %d", len(decoded.Components))
	}

	// DecodeAuto accepts the bare component stream too.
	auto, err := DecodeAuto(body, AFIPv4)
	if err != nil || len(auto.Components) != 12 {
		t.Fatalf("DecodeAuto bare failed: %v", err)
	}

	// DecodeNLRI rejects trailing bytes.
	if _, err := DecodeNLRI(append(append([]byte{}, framed...), 0x00), AFIPv4); !errors.Is(err, ErrTrailingBytes) {
		t.Fatalf("want trailing bytes error, got %v", err)
	}

	// Sequence of two framed NLRIs.
	seq := append(append([]byte{}, framed...), framed...)
	rules, err := DecodeNLRISequence(seq, AFIPv4)
	if err != nil || len(rules) != 2 {
		t.Fatalf("sequence decode: %d rules err=%v", len(rules), err)
	}
}

func TestIPv6PrefixDecode(t *testing.T) {
	// Type 1 dst 2001:db8::/32 -> plen 32, 4 prefix bytes 20 01 0D B8.
	cs, err := DecodeComponents(mustHex(t, "01 20 20 01 0D B8"), AFIPv6)
	if err != nil {
		t.Fatal(err)
	}
	if got := cs[0].Prefix.String(); got != "2001:db8::/32" {
		t.Fatalf("v6 prefix = %s", got)
	}
	out, err := EncodeComponents(cs, AFIPv6)
	if err != nil {
		t.Fatal(err)
	}
	if want := mustHex(t, "01 20 20 01 0D B8"); !bytes.Equal(out, want) {
		t.Fatalf("v6 encode mismatch %X", out)
	}
}
