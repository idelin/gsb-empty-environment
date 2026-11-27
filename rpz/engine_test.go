package rpz

import (
	"net/netip"
	"testing"
)

func mustAdd(t *testing.T, zone *Zone, owner string, records ...Record) {
	t.Helper()
	if err := zone.AddRule(owner, records...); err != nil {
		t.Fatalf("AddRule(%s): %v", owner, err)
	}
}

func TestTriggerPrecedenceWithinZone(t *testing.T) {
	zone := NewZone("policy.example.")
	mustAdd(t, zone, "evil.example.policy.example.", Record{Type: "CNAME", Value: "."})
	mustAdd(t, zone, "32.9.9.9.10.rpz-client-ip.policy.example.", Record{Type: "CNAME", Value: "rpz-drop."})
	mustAdd(t, zone, "24.0.1.168.192.rpz-ip.policy.example.", Record{Type: "CNAME", Value: "rpz-tcp-only."})
	mustAdd(t, zone, "ns.bad.example.rpz-nsdname.policy.example.", Record{Type: "CNAME", Value: "*."})
	mustAdd(t, zone, "24.53.10.rpz-nsip.policy.example.", Record{Type: "CNAME", Value: "rpz-passthru."})

	engine := NewEngine([]*Zone{zone})
	fullRequest := Request{
		ClientIP:    mustAddr(t, "10.9.9.9"),
		QName:       "evil.example.",
		AnswerIPs:   []netip.Addr{mustAddr(t, "192.168.1.5")},
		NameServers: []NSHost{{Name: "ns.bad.example.", IPs: []netip.Addr{mustAddr(t, "10.53.0.1")}}},
	}

	// CLIENT-IP beats every other trigger even when all would match.
	verdict := engine.Decide(fullRequest)
	if !verdict.Hit || verdict.Rule.Trigger != TriggerClientIP || verdict.Rule.Action != ActionDROP {
		t.Fatalf("CLIENT-IP must win, got %+v", verdict)
	}

	// Without the client match, QNAME is next.
	fullRequest.ClientIP = mustAddr(t, "10.0.0.1")
	verdict = engine.Decide(fullRequest)
	if !verdict.Hit || verdict.Rule.Trigger != TriggerQNAME || verdict.Rule.Action != ActionNXDOMAIN {
		t.Fatalf("QNAME must win after CLIENT-IP, got %+v", verdict)
	}

	// Then NSDNAME.
	fullRequest.QName = "neutral.example."
	verdict = engine.Decide(fullRequest)
	if !verdict.Hit || verdict.Rule.Trigger != TriggerNSDNAME || verdict.Rule.Action != ActionNODATA {
		t.Fatalf("NSDNAME must win, got %+v", verdict)
	}

	// Then NSIP.
	fullRequest.NameServers[0].Name = "ns.other.example."
	verdict = engine.Decide(fullRequest)
	if !verdict.Hit || verdict.Rule.Trigger != TriggerNSIP || !verdict.PassThrough {
		t.Fatalf("NSIP must win as PASSTHRU, got %+v", verdict)
	}

	// Finally the answer-section IP trigger.
	fullRequest.NameServers[0].IPs = []netip.Addr{mustAddr(t, "8.8.8.8")}
	verdict = engine.Decide(fullRequest)
	if !verdict.Hit || verdict.Rule.Trigger != TriggerIP || verdict.Rule.Action != ActionTCPOnly {
		t.Fatalf("answer IP must win last, got %+v", verdict)
	}
}

func TestZoneOrderAndPassThroughShortCircuit(t *testing.T) {
	high := NewZone("high.example.")
	low := NewZone("low.example.")
	mustAdd(t, high, "safe.example.high.example.", Record{Type: "CNAME", Value: "rpz-passthru."})
	mustAdd(t, low, "safe.example.low.example.", Record{Type: "CNAME", Value: "."})
	mustAdd(t, low, "32.5.5.5.10.rpz-ip.low.example.", Record{Type: "CNAME", Value: "rpz-drop."})

	engine := NewEngine([]*Zone{high, low})
	verdict := engine.Decide(Request{
		QName:     "safe.example.",
		AnswerIPs: []netip.Addr{mustAddr(t, "10.5.5.5")},
	})
	if !verdict.Hit || !verdict.PassThrough || verdict.Rule.Zone != "high.example." {
		t.Fatalf("high-zone PASSTHRU must short-circuit, got %+v", verdict)
	}

	// Unmatched names fall through to the low zone.
	engine = NewEngine([]*Zone{NewZone("empty.example."), low})
	verdict = engine.Decide(Request{QName: "safe.example."})
	if !verdict.Hit || verdict.Rule.Zone != "low.example." || verdict.Rule.Action != ActionNXDOMAIN {
		t.Fatalf("low zone must decide once high zone misses, got %+v", verdict)
	}
}

func TestWildcardDoesNotMatchBareDomain(t *testing.T) {
	zone := NewZone("wild.example.")
	mustAdd(t, zone, "*.bad.example.wild.example.", Record{Type: "CNAME", Value: "."})
	engine := NewEngine([]*Zone{zone})

	if verdict := engine.Decide(Request{QName: "bad.example."}); verdict.Hit {
		t.Fatalf("bare domain must not match wildcard, got %+v", verdict)
	}
	if verdict := engine.Decide(Request{QName: "www.bad.example."}); !verdict.Hit {
		t.Fatal("subdomain must match wildcard")
	}
	if verdict := engine.Decide(Request{QName: "a.b.c.bad.example."}); !verdict.Hit {
		t.Fatal("deep subdomain must match wildcard")
	}
}

func TestExactBeatsWildcardAndLongestWins(t *testing.T) {
	zone := NewZone("exact.example.")
	mustAdd(t, zone, "*.bad.example.exact.example.", Record{Type: "CNAME", Value: "rpz-drop."})
	mustAdd(t, zone, "www.bad.example.exact.example.", Record{Type: "CNAME", Value: "*."})
	mustAdd(t, zone, "*.example.exact.example.", Record{Type: "CNAME", Value: "rpz-tcp-only."})
	engine := NewEngine([]*Zone{zone})

	verdict := engine.Decide(Request{QName: "www.bad.example."})
	if !verdict.Hit || verdict.Rule.Action != ActionNODATA {
		t.Fatalf("exact owner must beat wildcard, got %+v", verdict)
	}
	verdict = engine.Decide(Request{QName: "mail.bad.example."})
	if !verdict.Hit || verdict.Rule.Action != ActionDROP {
		t.Fatalf("more specific wildcard *.bad.example must win, got %+v", verdict)
	}
	verdict = engine.Decide(Request{QName: "www.other.example."})
	if !verdict.Hit || verdict.Rule.Action != ActionTCPOnly {
		t.Fatalf("fallback wildcard *.example must win, got %+v", verdict)
	}
}

func TestCNAMEChainHopByHop(t *testing.T) {
	zone := NewZone("chain.example.")
	mustAdd(t, zone, "evil.target.example.chain.example.", Record{Type: "CNAME", Value: "."})
	engine := NewEngine([]*Zone{zone})

	verdict := engine.Decide(Request{
		QName:      "www.legit.example.",
		CNAMEChain: []string{"cdn.legit.example.", "evil.target.example."},
	})
	if !verdict.Hit || verdict.Rule.Trigger != TriggerQNAME {
		t.Fatalf("CNAME chain target must trigger QNAME, got %+v", verdict)
	}
	if verdict.MatchedName != "evil.target.example." {
		t.Fatalf("matched name should be the CNAME target, got %q", verdict.MatchedName)
	}

	// An earlier matching hop wins over a later one.
	mustAdd(t, zone, "cdn.legit.example.chain.example.", Record{Type: "CNAME", Value: "rpz-drop."})
	verdict = engine.Decide(Request{
		QName:      "www.legit.example.",
		CNAMEChain: []string{"cdn.legit.example.", "evil.target.example."},
	})
	if !verdict.Hit || verdict.Rule.Action != ActionDROP || verdict.MatchedName != "cdn.legit.example." {
		t.Fatalf("first matching CNAME hop must win, got %+v", verdict)
	}
}

func TestMinNSDots(t *testing.T) {
	zone := NewZone("nsdots.example.")
	mustAdd(t, zone, "root.rpz-nsdname.nsdots.example.", Record{Type: "CNAME", Value: "."})
	mustAdd(t, zone, "a.gtld.rpz-nsdname.nsdots.example.", Record{Type: "CNAME", Value: "."})

	engine := NewEngine([]*Zone{zone})
	verdict := engine.Decide(Request{
		QName: "example.",
		NameServers: []NSHost{
			{Name: "root."},
			{Name: "a.gtld."},
		},
	})
	if verdict.Hit {
		t.Fatalf("min-ns-dots must protect root/TLD hosts, got %+v", verdict)
	}

	permissive := NewEngine([]*Zone{zone}, WithMinNSDots(0))
	verdict = permissive.Decide(Request{
		QName:       "example.",
		NameServers: []NSHost{{Name: "root."}},
	})
	if !verdict.Hit || verdict.MatchedName != "root." {
		t.Fatalf("min-ns-dots=0 should allow single-label hosts, got %+v", verdict)
	}
}

func TestIPTriggerLongestPrefixWins(t *testing.T) {
	zone := NewZone("lpm.example.")
	mustAdd(t, zone, "16.0.168.192.rpz-ip.lpm.example.", Record{Type: "CNAME", Value: "rpz-drop."})
	mustAdd(t, zone, "24.0.1.168.192.rpz-ip.lpm.example.", Record{Type: "CNAME", Value: "."})
	engine := NewEngine([]*Zone{zone})

	verdict := engine.Decide(Request{QName: "x.", AnswerIPs: []netip.Addr{mustAddr(t, "192.168.1.9")}})
	if !verdict.Hit || verdict.Rule.Action != ActionNXDOMAIN {
		t.Fatalf("/24 must beat /16, got %+v", verdict)
	}
	verdict = engine.Decide(Request{QName: "x.", AnswerIPs: []netip.Addr{mustAddr(t, "192.168.2.9")}})
	if !verdict.Hit || verdict.Rule.Action != ActionDROP {
		t.Fatalf("/16 should still cover other /24s, got %+v", verdict)
	}
}

func TestLocalDataActions(t *testing.T) {
	zone := NewZone("local.example.")
	mustAdd(t, zone, "hole.example.local.example.",
		Record{Type: "A", Value: "192.0.2.10", TTL: 60},
		Record{Type: "AAAA", Value: "2001:db8::10", TTL: 60},
		Record{Type: "TXT", Value: "blocked by policy", TTL: 30},
	)
	mustAdd(t, zone, "redirect.example.local.example.",
		Record{Type: "CNAME", Value: "landing.example.", TTL: 45},
	)
	engine := NewEngine([]*Zone{zone})

	verdict := engine.Decide(Request{QName: "hole.example."})
	if !verdict.Hit || verdict.Rule.Action != ActionLocalData || len(verdict.Rule.Records) != 3 {
		t.Fatalf("local data rule expected 3 records, got %+v", verdict)
	}
	verdict = engine.Decide(Request{QName: "redirect.example."})
	if !verdict.Hit || verdict.Rule.Action != ActionLocalData ||
		len(verdict.Rule.Records) != 1 || verdict.Rule.Records[0].Value != "landing.example." {
		t.Fatalf("CNAME rewrite expected, got %+v", verdict)
	}
}

func TestIPv6ZZTriggerEndToEnd(t *testing.T) {
	zone := NewZone("v6.example.")
	mustAdd(t, zone, "48.zz.db8.2001.rpz-ip.v6.example.", Record{Type: "CNAME", Value: "rpz-drop."})
	engine := NewEngine([]*Zone{zone})

	verdict := engine.Decide(Request{
		QName:     "v6host.example.",
		AnswerIPs: []netip.Addr{mustAddr(t, "2001:db8::abcd:123")},
	})
	if !verdict.Hit || verdict.Rule.Action != ActionDROP {
		t.Fatalf("IPv6 zz owner must match answer, got %+v", verdict)
	}
	verdict = engine.Decide(Request{
		QName:     "v6host.example.",
		AnswerIPs: []netip.Addr{mustAddr(t, "2001:db9::1")},
	})
	if verdict.Hit {
		t.Fatalf("address outside /48 must not match, got %+v", verdict)
	}
}

func TestNoHitReturnsOriginal(t *testing.T) {
	engine := NewEngine([]*Zone{NewZone("empty.example.")})
	verdict := engine.Decide(Request{
		ClientIP:  mustAddr(t, "198.51.100.7"),
		QName:     "clean.example.",
		AnswerIPs: []netip.Addr{mustAddr(t, "203.0.113.9")},
	})
	if verdict.Hit || verdict.PassThrough {
		t.Fatalf("clean request must return no verdict, got %+v", verdict)
	}
}
