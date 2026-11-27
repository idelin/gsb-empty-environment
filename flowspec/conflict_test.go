package flowspec

import (
	"net"
	"testing"
)

func prefixRule(cidr string, as uint32) *Rule {
	return &Rule{AF: AFIPv4, OriginAS: as, Components: []Component{
		PrefixComponent(TypeDestPrefix, MustParsePrefix(cidr)),
	}}
}

func TestPriorityLongestPrefixWins(t *testing.T) {
	broad := prefixRule("198.51.100.0/24", 0)
	specific := prefixRule("198.51.100.128/25", 0)
	if c := ComparePriority(specific, broad); c != -1 {
		t.Fatalf("specific vs broad = %d, want -1 (higher precedence)", c)
	}
	ordered := SortByPriority([]*Rule{broad, specific})
	if ordered[0] != specific {
		t.Fatal("longer prefix must sort first")
	}
}

func TestMatchRelationPrefixes(t *testing.T) {
	b24 := prefixRule("198.51.100.0/24", 0)
	b25 := prefixRule("198.51.100.128/25", 0)

	if r := MatchRelation(b25, b24); r != RelBCoversA {
		t.Fatalf("/25 vs /24 = %v, want BCoversA (/25 subset /24)", r)
	}
	if r := MatchRelation(b24, b25); r != RelACoversB {
		t.Fatalf("/24 vs /25 = %v, want ACoversB", r)
	}
	other := prefixRule("203.0.113.0/24", 0)
	if r := MatchRelation(b24, other); r != RelDisjoint {
		t.Fatalf("different /24s = %v, want disjoint", r)
	}
	if r := MatchRelation(b24, prefixRule("198.51.100.0/24", 0)); r != RelEqual {
		t.Fatalf("identical prefixes = %v, want equal", r)
	}
}

func TestProtocolDisjointSuppressesShadow(t *testing.T) {
	tcp := &Rule{Components: []Component{
		PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
		TermsComponent(TypeProtocol, EqualTerm(6)),
	}}
	udp := &Rule{Components: []Component{
		PrefixComponent(TypeDestPrefix, MustParsePrefix("198.51.100.0/24")),
		TermsComponent(TypeProtocol, EqualTerm(17)),
	}}
	if r := MatchRelation(tcp, udp); r != RelDisjoint {
		t.Fatalf("tcp vs udp = %v, want disjoint (no false shadow)", r)
	}
}

func TestBitmaskRelations(t *testing.T) {
	syn := TermsComponent(TypeTCPFlags, Term{Match: true, Value: 0x02, Width: 1})
	synACK := TermsComponent(TypeTCPFlags, Term{Match: true, Value: 0x12, Width: 1})
	if r := bitmaskRelation(&syn, &synACK); r != RelACoversB {
		t.Fatalf("SYN vs SYN+ACK = %v, want ACoversB", r)
	}
	if r := bitmaskRelation(&synACK, &syn); r != RelBCoversA {
		t.Fatalf("SYN+ACK vs SYN = %v, want BCoversA", r)
	}

	fragAll := TermsComponent(TypeFragment, Term{Match: true, Value: 0x04, Width: 1})
	fragSub := TermsComponent(TypeFragment, Term{Match: true, Value: 0x05, Width: 1})
	if r := bitmaskRelation(&fragAll, &fragSub); r != RelACoversB {
		t.Fatalf("fragment mask relation = %v, want ACoversB", r)
	}

	// DSCP is ordered/numeric: EF(46) vs AF41(34) singleton sets are disjoint.
	ef := TermsComponent(TypeDSCP, EqualTerm(46))
	af := TermsComponent(TypeDSCP, EqualTerm(34))
	if r := numericRelation(&ef, &af); r != RelDisjoint {
		t.Fatalf("dscp 46 vs 34 = %v, want disjoint", r)
	}
}

func TestResolvePicksPriorityWinner(t *testing.T) {
	permit := prefixRule("198.51.100.0/24", 65010)
	permit.ID = "permit"
	blackhole := prefixRule("198.51.100.128/25", 65066)
	blackhole.ID = "blackhole"
	blackhole.Actions = ActionSet{TrafficRate: &TrafficRate{RateBits: 0}}

	rules := []*Rule{permit, blackhole}

	inHole := &Packet{}
	inHole.SetDstIP(net.ParseIP("198.51.100.200"))
	winner, idx, verdict := Resolve(inHole, rules)
	if verdict != VerdictDrop || winner.ID != "blackhole" || idx != 1 {
		t.Fatalf("in-hole winner=%s idx=%d verdict=%s, want blackhole/drop", winnerLabel(winner), idx, verdict)
	}

	outside := &Packet{}
	outside.SetDstIP(net.ParseIP("198.51.100.10"))
	winner2, _, verdict2 := Resolve(outside, rules)
	if verdict2 != VerdictPermit || winner2.ID != "permit" {
		t.Fatalf("outside winner=%s verdict=%s, want permit", winnerLabel(winner2), verdict2)
	}
}

func winnerLabel(r *Rule) string {
	if r == nil {
		return "<nil>"
	}
	return r.ID
}

func TestResolveDefaultPermitWhenNoRule(t *testing.T) {
	pkt := &Packet{}
	pkt.SetDstIP(net.ParseIP("203.0.113.9"))
	r, idx, v := Resolve(pkt, nil)
	if r != nil || idx != -1 || v != VerdictPermit {
		t.Fatal("empty rule set must default to permit")
	}
}
