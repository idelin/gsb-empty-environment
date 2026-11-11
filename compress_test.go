package netsec

import (
	"errors"
	"testing"
)

const (
	protoICMP uint8 = 1
	protoTCP  uint8 = 6
	protoUDP  uint8 = 17
)

func mustIPv4(t *testing.T, s string) uint32 {
	t.Helper()
	v, err := ParseIPv4(s)
	if err != nil {
		t.Fatalf("ParseIPv4(%q): %v", s, err)
	}
	return v
}

func findShard(t *testing.T, plan *Plan, point Point) Shard {
	t.Helper()
	for _, shard := range plan.Shards {
		if shardMatches(shard, point) {
			return shard
		}
	}
	t.Fatalf("no shard covers point %+v", point)
	return Shard{}
}

func TestCompressSingleRuleMergesToOne(t *testing.T) {
	rules := []Rule{{
		ID:        "allow-web",
		SrcIP:     IPRange{From: mustIPv4(t, "10.0.0.0"), To: mustIPv4(t, "10.0.0.255")},
		DstIP:     IPRange{From: mustIPv4(t, "192.168.1.0"), To: mustIPv4(t, "192.168.1.10")},
		SrcPort:   PortRange{From: 1000, To: 2000},
		DstPort:   PortRange{From: 80, To: 80},
		Protocols: []uint8{protoTCP},
		Action:    ActionAllow,
	}}

	plan, err := Compress(rules, Options{})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}
	if len(plan.Shards) != 1 {
		t.Fatalf("expected 1 merged shard, got %d", len(plan.Shards))
	}

	shard := plan.Shards[0]
	if shard.Action != ActionAllow || shard.SourceIndex != 0 || shard.SourceID != "allow-web" {
		t.Fatalf("bad provenance: %+v", shard)
	}
	if shard.SrcIP.To != mustIPv4(t, "10.0.0.255") || shard.DstPort.To != 80 {
		t.Fatalf("shard ranges lost: %+v", shard)
	}
	if shard.Protocols[0] != protoTCP || shard.Explanation == "" {
		t.Fatalf("protocol/explanation missing: %+v", shard)
	}
}

func TestNestedExceptionFirstMatch(t *testing.T) {
	rules := []Rule{
		{
			ID:        "narrow-exception",
			SrcIP:     IPRange{From: mustIPv4(t, "10.0.0.5"), To: mustIPv4(t, "10.0.0.5")},
			DstIP:     IPRange{From: mustIPv4(t, "1.1.1.1"), To: mustIPv4(t, "1.1.1.1")},
			SrcPort:   PortRange{From: 0, To: 65535},
			DstPort:   PortRange{From: 80, To: 80},
			Protocols: []uint8{protoTCP},
			Action:    ActionAllow,
		},
		{
			ID:        "broad-deny",
			SrcIP:     IPRange{From: mustIPv4(t, "10.0.0.0"), To: mustIPv4(t, "10.0.0.255")},
			DstIP:     IPRange{From: 0, To: 4294967295},
			SrcPort:   PortRange{From: 0, To: 65535},
			DstPort:   PortRange{From: 0, To: 65535},
			Protocols: []uint8{protoTCP, protoUDP},
			Action:    ActionDeny,
		},
	}

	exception := Point{
		SrcIP: mustIPv4(t, "10.0.0.5"), DstIP: mustIPv4(t, "1.1.1.1"),
		SourcePort: 40000, DestPort: 80, Protocol: protoTCP,
	}
	neighbor := Point{
		SrcIP: mustIPv4(t, "10.0.0.6"), DstIP: mustIPv4(t, "1.1.1.1"),
		SourcePort: 40000, DestPort: 80, Protocol: protoTCP,
	}
	if got := Decide(rules, exception.SrcIP, exception.DstIP, exception.SourcePort, exception.DestPort, protoTCP); got != ActionAllow {
		t.Fatalf("exception point decided %s, want allow", got)
	}
	if got := Decide(rules, neighbor.SrcIP, neighbor.DstIP, neighbor.SourcePort, neighbor.DestPort, protoTCP); got != ActionDeny {
		t.Fatalf("neighbor point decided %s, want deny", got)
	}

	plan, report, err := Publish(rules, Options{IncludeDefaultDeny: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !report.OK() || report.PointsChecked == 0 {
		t.Fatalf("verification not clean: %+v", report)
	}

	allowShard := findShard(t, plan, exception)
	if allowShard.Action != ActionAllow || allowShard.SourceIndex != 0 {
		t.Fatalf("exception shard lost provenance: %+v", allowShard)
	}
	denyShard := findShard(t, plan, neighbor)
	if denyShard.Action != ActionDeny || denyShard.SourceIndex != 1 {
		t.Fatalf("broad deny shard lost provenance: %+v", denyShard)
	}

	// Swapping rule order must flip the exception decision, proving the
	// compiler preserved ordering rather than just set membership.
	swapped := []Rule{rules[1], rules[0]}
	if got := Decide(swapped, exception.SrcIP, exception.DstIP, exception.SourcePort, exception.DestPort, protoTCP); got != ActionDeny {
		t.Fatalf("swapped order decided %s, want deny", got)
	}
}

func TestNumericUpperBounds(t *testing.T) {
	rules := []Rule{{
		ID:        "bounds",
		SrcIP:     IPRange{From: 4294967295, To: 4294967295},
		DstIP:     IPRange{From: 0, To: 4294967295},
		SrcPort:   PortRange{From: 65535, To: 65535},
		DstPort:   PortRange{From: 0, To: 65535},
		Protocols: []uint8{255},
		Action:    ActionAllow,
	}}

	plan, report, err := Publish(rules, Options{IncludeDefaultDeny: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !report.OK() {
		t.Fatalf("verification mismatch at bounds: %+v", report.Mismatches)
	}

	shard := findShard(t, plan, Point{SrcIP: 4294967295, DstIP: 4294967295, SourcePort: 65535, DestPort: 65535, Protocol: 255})
	if shard.SrcIP.To != 4294967295 || shard.DstIP.To != 4294967295 ||
		shard.SrcPort.To != 65535 || shard.DstPort.To != 65535 {
		t.Fatalf("upper bounds not represented: %+v", shard)
	}

	// Endpoint one below the rule interval must remain default deny.
	below := Point{SrcIP: 4294967294, DstIP: 0, SourcePort: 65534, DestPort: 0, Protocol: 255}
	denyShard := findShard(t, plan, below)
	if denyShard.Action != ActionDeny || denyShard.SourceIndex != -1 || denyShard.SourceID != "default-deny" {
		t.Fatalf("default deny region wrong: %+v", denyShard)
	}
}

func TestBudgetExceededRejectsPublish(t *testing.T) {
	rules := []Rule{
		{
			SrcIP:     IPRange{From: 1, To: 1},
			DstIP:     IPRange{From: 1, To: 1},
			SrcPort:   PortRange{From: 1, To: 1},
			DstPort:   PortRange{From: 1, To: 1},
			Protocols: []uint8{protoTCP},
			Action:    ActionAllow,
		},
		{
			SrcIP:     IPRange{From: 2, To: 2},
			DstIP:     IPRange{From: 2, To: 2},
			SrcPort:   PortRange{From: 2, To: 2},
			DstPort:   PortRange{From: 2, To: 2},
			Protocols: []uint8{protoUDP},
			Action:    ActionAllow,
		},
	}

	if _, err := Compress(rules, Options{MaxEntries: 1}); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	plan, err := Compress(rules, Options{MaxEntries: 2})
	if err != nil {
		t.Fatalf("budget 2 should fit: %v", err)
	}
	if len(plan.Shards) != 2 {
		t.Fatalf("expected 2 shards, got %d", len(plan.Shards))
	}
}

func TestInvalidRules(t *testing.T) {
	base := func() Rule {
		return Rule{
			SrcIP:     IPRange{From: 1, To: 2},
			DstIP:     IPRange{From: 1, To: 2},
			SrcPort:   PortRange{From: 1, To: 2},
			DstPort:   PortRange{From: 1, To: 2},
			Protocols: []uint8{protoTCP},
			Action:    ActionAllow,
		}
	}

	cases := map[string]func(Rule) Rule{
		"unknown action": func(r Rule) Rule {
			r.Action = Action(99)
			return r
		},
		"inverted source IP": func(r Rule) Rule {
			r.SrcIP = IPRange{From: 5, To: 1}
			return r
		},
		"inverted destination port": func(r Rule) Rule {
			r.DstPort = PortRange{From: 9, To: 1}
			return r
		},
		"empty protocols": func(r Rule) Rule {
			r.Protocols = nil
			return r
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rule := mutate(base())
			if _, err := Compress([]Rule{rule}, Options{}); !errors.Is(err, ErrInvalidRule) {
				t.Fatalf("expected ErrInvalidRule, got %v", err)
			}
		})
	}
}

func TestAdjacentIntervalsMerge(t *testing.T) {
	t.Run("same source merges", func(t *testing.T) {
		// A single rule spanning two induced cells collapses back to one entry.
		rules := []Rule{{
			ID:        "one-rule",
			SrcIP:     IPRange{From: 0, To: 4294967295},
			DstIP:     IPRange{From: 0, To: 4294967295},
			SrcPort:   PortRange{From: 0, To: 200},
			DstPort:   PortRange{From: 0, To: 65535},
			Protocols: []uint8{protoICMP},
			Action:    ActionAllow,
		}}

		plan, err := Compress(rules, Options{})
		if err != nil {
			t.Fatalf("Compress: %v", err)
		}
		if len(plan.Shards) != 1 {
			t.Fatalf("same-source adjacent cells should merge, got %d shards", len(plan.Shards))
		}
		if plan.Shards[0].SrcPort.From != 0 || plan.Shards[0].SrcPort.To != 200 {
			t.Fatalf("merged port interval wrong: %+v", plan.Shards[0].SrcPort)
		}
	})

	t.Run("different sources stay separate", func(t *testing.T) {
		// Adjacent regions produced by distinct ordered rules must keep their
		// individual provenance even when both actions are identical.
		rules := []Rule{{
			ID:        "first",
			SrcIP:     IPRange{From: 0, To: 4294967295},
			DstIP:     IPRange{From: 0, To: 4294967295},
			SrcPort:   PortRange{From: 0, To: 100},
			DstPort:   PortRange{From: 0, To: 65535},
			Protocols: []uint8{protoICMP},
			Action:    ActionAllow,
		}, {
			ID:        "second",
			SrcIP:     IPRange{From: 0, To: 4294967295},
			DstIP:     IPRange{From: 0, To: 4294967295},
			SrcPort:   PortRange{From: 101, To: 200},
			DstPort:   PortRange{From: 0, To: 65535},
			Protocols: []uint8{protoICMP},
			Action:    ActionAllow,
		}}

		plan, report, err := Publish(rules, Options{})
		if err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if !report.OK() {
			t.Fatalf("verification failed: %+v", report.Mismatches)
		}
		if len(plan.Shards) != 2 {
			t.Fatalf("distinct sources must not merge, got %d shards", len(plan.Shards))
		}
		ids := map[string]bool{plan.Shards[0].SourceID: true, plan.Shards[1].SourceID: true}
		if !ids["first"] || !ids["second"] {
			t.Fatalf("provenance lost: %+v", plan.Shards)
		}
	})
}
