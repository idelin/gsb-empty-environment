package main

import (
	"net/netip"
	"testing"
)

func TestLastAddr(t *testing.T) {
	cases := []struct {
		prefix string
		want   string
	}{
		{"10.0.0.0/8", "10.255.255.255"},
		{"10.1.0.0/16", "10.1.255.255"},
		{"192.168.1.7/32", "192.168.1.7"},
		{"2001:db8::/32", "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff"},
		{"::1/128", "::1"},
	}
	for _, c := range cases {
		got := lastAddr(netip.MustParsePrefix(c.prefix))
		if got != netip.MustParseAddr(c.want) {
			t.Errorf("lastAddr(%s) = %s, want %s", c.prefix, got, c.want)
		}
	}
}

func TestIntersectCrossFamily(t *testing.T) {
	v4 := ruleRect(Rule{ID: 1, Prefix: netip.MustParsePrefix("0.0.0.0/0"), Protocol: ProtoAny, PortLo: 0, PortHi: 65535})
	v6 := ruleRect(Rule{ID: 2, Prefix: netip.MustParsePrefix("::/0"), Protocol: ProtoAny, PortLo: 0, PortHi: 65535})
	if _, ok := intersect(v4, v6); ok {
		t.Fatal("IPv4 and IPv6 rects must never intersect")
	}
}

func TestSubtractDisjoint(t *testing.T) {
	a := ruleRect(Rule{ID: 1, Prefix: netip.MustParsePrefix("10.0.0.0/8"), Protocol: 6, PortLo: 80, PortHi: 80})
	b := ruleRect(Rule{ID: 2, Prefix: netip.MustParsePrefix("11.0.0.0/8"), Protocol: 6, PortLo: 80, PortHi: 80})
	rest := subtract(a, b)
	if len(rest) != 1 || rest[0] != a {
		t.Fatalf("subtract disjoint = %v, want [%v]", rest, a)
	}
}

func TestSubtractFullCover(t *testing.T) {
	inner := ruleRect(Rule{ID: 1, Prefix: netip.MustParsePrefix("10.1.0.0/16"), Protocol: 6, PortLo: 80, PortHi: 80})
	outer := ruleRect(Rule{ID: 2, Prefix: netip.MustParsePrefix("10.0.0.0/8"), Protocol: ProtoAny, PortLo: 0, PortHi: 65535})
	if rest := subtract(inner, outer); len(rest) != 0 {
		t.Fatalf("subtract fully covered = %v, want empty", rest)
	}
}

func TestSubtractPartial(t *testing.T) {
	// a covers ports 0..200, b removes ports 100..65535 -> remainder 0..99.
	a := ruleRect(Rule{ID: 1, Prefix: netip.MustParsePrefix("10.0.0.0/24"), Protocol: 6, PortLo: 0, PortHi: 200})
	b := ruleRect(Rule{ID: 2, Prefix: netip.MustParsePrefix("10.0.0.0/24"), Protocol: 6, PortLo: 100, PortHi: 65535})
	rest := subtract(a, b)
	if len(rest) != 1 {
		t.Fatalf("subtract partial produced %d rects, want 1", len(rest))
	}
	if rest[0].dLo != 0 || rest[0].dHi != 99 {
		t.Fatalf("remainder ports = %d-%d, want 0-99", rest[0].dLo, rest[0].dHi)
	}
}

func TestRemainderUnionCoverage(t *testing.T) {
	// Two blockers with adjacent port ranges jointly cover 100..300.
	target := ruleRect(Rule{ID: 9, Prefix: netip.MustParsePrefix("10.9.0.0/16"), Protocol: 6, PortLo: 100, PortHi: 300})
	b1 := ruleRect(Rule{ID: 7, Prefix: netip.MustParsePrefix("10.9.0.0/16"), Protocol: 6, PortLo: 100, PortHi: 200})
	b2 := ruleRect(Rule{ID: 8, Prefix: netip.MustParsePrefix("10.9.0.0/16"), Protocol: 6, PortLo: 201, PortHi: 300})
	if rest := remainder(target, []rect{b1, b2}); len(rest) != 0 {
		t.Fatalf("union of blockers should fully cover target, remainder=%v", rest)
	}
	// A gap (201..300 uncovered by b1 alone) keeps the target reachable.
	if rest := remainder(target, []rect{b1}); len(rest) == 0 {
		t.Fatal("single blocker must not fully cover target")
	}
}
