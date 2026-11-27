package dnssec

import "testing"

func TestParseMessageWithCompression(t *testing.T) {
	zone := MustParseName("example.")
	host := MustParseName("host.example.")
	aRR := RR{Name: host, Type: TypeA, Class: ClassIN, TTL: 120, Rdata: []byte{192, 0, 2, 10}}
	cnameRR := RR{Name: host, Type: TypeCNAME, Class: ClassIN, TTL: 120,
		Rdata: MustParseName("target.example.").Wire()}

	m := &Message{
		ID:    0x1234,
		Flags: FlagQR | FlagAA | RcodeOK,
		Question: []Question{
			{Name: host, Type: TypeA, Class: ClassIN},
		},
		Answer:     []RR{cnameRR, aRR},
		Authority:  []RR{nsecRR(zone, host, []uint16{TypeSOA})},
		Additional: nil,
	}
	wire := packMessage(m)

	parsed, err := ParseMessage(wire)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.ID != m.ID || parsed.Rcode() != RcodeOK {
		t.Fatalf("header mismatch: id=%x rcode=%d", parsed.ID, parsed.Rcode())
	}
	if len(parsed.Question) != 1 || !parsed.Question[0].Name.Equal(host) {
		t.Fatal("question section mismatch")
	}
	if len(parsed.Answer) != 2 {
		t.Fatalf("answers = %d want 2", len(parsed.Answer))
	}
	if !parsed.Answer[0].Name.Equal(host) || parsed.Answer[0].Type != TypeCNAME {
		t.Fatal("CNAME answer mismatch")
	}
	// The compressed CNAME target must be re-expanded to a full name.
	target, _, err := readName(parsed.Answer[0].Rdata, 0)
	if err != nil {
		t.Fatalf("read compressed target: %v", err)
	}
	if !target.Equal(MustParseName("target.example.")) {
		t.Fatalf("target = %s", target)
	}
	if len(parsed.Authority) != 1 || !parsed.Authority[0].Name.Equal(zone) {
		t.Fatal("authority section mismatch")
	}
}

func TestParseMessageTruncated(t *testing.T) {
	if _, err := ParseMessage([]byte{0, 1, 0}); err != ErrShortMessage {
		t.Fatalf("got %v want ErrShortMessage", err)
	}
}

func TestNameTraversal(t *testing.T) {
	n := MustParseName("a.b.c.example.")
	if n.LabelCount() != 4 {
		t.Fatalf("labels = %d want 4", n.LabelCount())
	}
	parent := n.Parent()
	if parent.String() != "b.c.example." {
		t.Fatalf("parent = %s", parent)
	}
	sub, err := parent.PrefixLabel("x")
	if err != nil {
		t.Fatal(err)
	}
	if sub.String() != "x.b.c.example." {
		t.Fatalf("prefix = %s", sub)
	}
	if !MustParseName("a.b.c.example.").IsSubdomain(MustParseName("c.example.")) {
		t.Fatal("subdomain relation failed")
	}
	if MustParseName("other.example.").IsSubdomain(MustParseName("c.example.")) {
		t.Fatal("unexpected subdomain relation")
	}
}
