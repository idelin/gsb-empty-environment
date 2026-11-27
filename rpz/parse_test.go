package rpz

import (
	"net/netip"
	"strings"
	"testing"
)

const sampleZone = `
$TTL 3600
@                      SOA ns1 hostmaster ( 1 7200 3600 1209600 3600 )
@                      NS  ns1
ns1                    A   192.0.2.53

; QNAME actions
nx.example.            CNAME .
nodata.example.        CNAME *.
pass.example.          CNAME rpz-passthru.
drop.example.          CNAME rpz-drop.
tcp.example.           CNAME rpz-tcp-only.
*.wild.example.        CNAME .
hole.example.          A   192.0.2.10
hole.example.          TXT "quarantine zone"

; IP triggers with documented encodings
24.0.1.168.192.rpz-ip            CNAME .
48.zz.db8.2001.rpz-ip            CNAME rpz-drop.
32.9.9.9.10.rpz-client-ip        CNAME *.
ns.evil.example.rpz-nsdname      CNAME .
24.53.10.rpz-nsip                CNAME rpz-passthru.
`

func TestParseZoneAndDecide(t *testing.T) {
	zone, err := ParseZone("block.local.", strings.NewReader(sampleZone))
	if err != nil {
		t.Fatalf("ParseZone: %v", err)
	}
	engine := NewEngine([]*Zone{zone})

	special := []struct {
		qname  string
		action Action
	}{
		{"nx.example.", ActionNXDOMAIN},
		{"nodata.example.", ActionNODATA},
		{"pass.example.", ActionPASSTHRU},
		{"drop.example.", ActionDROP},
		{"tcp.example.", ActionTCPOnly},
	}
	for _, tc := range special {
		verdict := engine.Decide(Request{QName: tc.qname})
		if !verdict.Hit || verdict.Rule.Action != tc.action {
			t.Fatalf("%s: want action %s, got %+v", tc.qname, tc.action, verdict)
		}
	}

	// Wildcard semantics survive parsing.
	if verdict := engine.Decide(Request{QName: "wild.example."}); verdict.Hit {
		t.Fatalf("bare wildcard parent must not hit: %+v", verdict)
	}
	if verdict := engine.Decide(Request{QName: "a.wild.example."}); !verdict.Hit {
		t.Fatal("wildcard subdomain must hit")
	}

	// Local data merges RRs across lines.
	verdict := engine.Decide(Request{QName: "hole.example."})
	if !verdict.Hit || verdict.Rule.Action != ActionLocalData || len(verdict.Rule.Records) != 2 {
		t.Fatalf("merged local data expected, got %+v", verdict)
	}
	if verdict.Rule.Records[0].TTL != 3600 {
		t.Fatalf("default $TTL must apply, got %d", verdict.Rule.Records[0].TTL)
	}

	// IPv4 and IPv6 reverse-CIDR triggers from zone text.
	verdict = engine.Decide(Request{QName: "x.", AnswerIPs: []netip.Addr{mustAddr(t, "192.168.1.50")}})
	if !verdict.Hit || verdict.Rule.Action != ActionNXDOMAIN {
		t.Fatalf("v4 rpz-ip rule expected, got %+v", verdict)
	}
	verdict = engine.Decide(Request{QName: "x.", AnswerIPs: []netip.Addr{mustAddr(t, "2001:db8::5:1")}})
	if !verdict.Hit || verdict.Rule.Action != ActionDROP {
		t.Fatalf("v6 zz rpz-ip rule expected, got %+v", verdict)
	}

	// Client IP and NS triggers.
	verdict = engine.Decide(Request{ClientIP: mustAddr(t, "10.9.9.9"), QName: "x."})
	if !verdict.Hit || verdict.Rule.Trigger != TriggerClientIP || verdict.Rule.Action != ActionNODATA {
		t.Fatalf("client-ip rule expected, got %+v", verdict)
	}
	verdict = engine.Decide(Request{
		QName:       "x.",
		NameServers: []NSHost{{Name: "ns.evil.example.", IPs: []netip.Addr{mustAddr(t, "10.53.0.7")}}},
	})
	if !verdict.Hit || verdict.Rule.Trigger != TriggerNSDNAME {
		t.Fatalf("nsdname rule expected, got %+v", verdict)
	}
	verdict = engine.Decide(Request{
		QName:       "x.",
		NameServers: []NSHost{{Name: "ns.other.example.", IPs: []netip.Addr{mustAddr(t, "10.53.0.7")}}},
	})
	if !verdict.Hit || verdict.Rule.Trigger != TriggerNSIP || !verdict.PassThrough {
		t.Fatalf("nsip passthru expected, got %+v", verdict)
	}

	// Apex boilerplate never becomes a policy rule.
	if verdict := engine.Decide(Request{QName: "block.local."}); verdict.Hit {
		t.Fatalf("zone apex must not be a QNAME rule, got %+v", verdict)
	}
}

func TestParseRelativeOwnersAndContinuation(t *testing.T) {
	text := `
$ORIGIN rpz.corp.
rel.zone      60 IN CNAME .
multi         IN (
                  A 192.0.2.20
                  AAAA 2001:db8::20
                )
`
	zone, err := ParseZone("rpz.corp.", strings.NewReader(text))
	if err != nil {
		t.Fatalf("ParseZone: %v", err)
	}
	engine := NewEngine([]*Zone{zone})

	verdict := engine.Decide(Request{QName: "rel.zone.rpz.corp."})
	if !verdict.Hit || verdict.Rule.Action != ActionNXDOMAIN {
		t.Fatalf("relative owner must be qualified against origin, got %+v", verdict)
	}
	verdict = engine.Decide(Request{QName: "multi.rpz.corp."})
	if !verdict.Hit || len(verdict.Rule.Records) != 2 {
		t.Fatalf("parenthesized multi-record owner expected, got %+v", verdict)
	}
}

func TestParseRejectsUnsupportedType(t *testing.T) {
	text := "weird.example.rpz.local. MX 10 mail.example.\n"
	if _, err := ParseZone("rpz.local.", strings.NewReader(text)); err == nil {
		t.Fatal("unsupported RR type at a policy owner must error")
	}
}
