package netsec

import (
	"errors"
	"math/rand"
	"testing"
)

func TestVerifyDetectsTamperedAction(t *testing.T) {
	rules := []Rule{{
		ID:        "only-allow",
		SrcIP:     IPRange{From: 100, To: 200},
		DstIP:     IPRange{From: 100, To: 200},
		SrcPort:   PortRange{From: 10, To: 20},
		DstPort:   PortRange{From: 30, To: 40},
		Protocols: []uint8{protoTCP},
		Action:    ActionAllow,
	}}

	plan, err := Compress(rules, Options{IncludeDefaultDeny: true})
	if err != nil {
		t.Fatalf("Compress: %v", err)
	}

	// Tamper with the rule-derived shard; verification must reject it.
	for i := range plan.Shards {
		if plan.Shards[i].SourceIndex == 0 {
			plan.Shards[i].Action = ActionDeny
			break
		}
	}
	report, err := Verify(rules, plan)
	if err != nil {
		t.Fatalf("Verify returned error instead of report: %v", err)
	}
	if report.OK() || len(report.Mismatches) == 0 {
		t.Fatalf("expected mismatch for tampered shard, report=%+v", report)
	}
	if _, _, err := Publish(rules, Options{IncludeDefaultDeny: true}); err != nil {
		t.Fatalf("untampered Publish should pass: %v", err)
	}
}

func TestVerifyDetectsOverlappingShards(t *testing.T) {
	plan := &Plan{
		RuleCount: 1,
		Shards: []Shard{
			{
				SrcIP:       IPRange{From: 0, To: 100},
				DstIP:       IPRange{From: 0, To: 100},
				SrcPort:     PortRange{From: 0, To: 100},
				DstPort:     PortRange{From: 0, To: 100},
				Protocols:   []uint8{protoTCP},
				Action:      ActionAllow,
				SourceIndex: 0,
			},
			{
				SrcIP:       IPRange{From: 50, To: 150},
				DstIP:       IPRange{From: 0, To: 100},
				SrcPort:     PortRange{From: 0, To: 100},
				DstPort:     PortRange{From: 0, To: 100},
				Protocols:   []uint8{protoTCP},
				Action:      ActionAllow,
				SourceIndex: 0,
			},
		},
	}
	rules := []Rule{{
		SrcIP:     IPRange{From: 0, To: 150},
		DstIP:     IPRange{From: 0, To: 100},
		SrcPort:   PortRange{From: 0, To: 100},
		DstPort:   PortRange{From: 0, To: 100},
		Protocols: []uint8{protoTCP},
		Action:    ActionAllow,
	}}

	report, err := Verify(rules, plan)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if report.OK() {
		t.Fatal("expected overlap mismatch")
	}
	if len(report.Mismatches[0].OverlapShards) != 2 {
		t.Fatalf("expected 2 overlapping shards, got %v", report.Mismatches[0].OverlapShards)
	}
}

func TestProvenanceOutOfRange(t *testing.T) {
	rules := []Rule{{
		SrcIP:     IPRange{From: 1, To: 2},
		DstIP:     IPRange{From: 1, To: 2},
		SrcPort:   PortRange{From: 1, To: 2},
		DstPort:   PortRange{From: 1, To: 2},
		Protocols: []uint8{protoTCP},
		Action:    ActionAllow,
	}}
	plan := &Plan{Shards: []Shard{{
		SrcIP:       rules[0].SrcIP,
		DstIP:       rules[0].DstIP,
		SrcPort:     rules[0].SrcPort,
		DstPort:     rules[0].DstPort,
		Protocols:   []uint8{protoTCP},
		Action:      ActionAllow,
		SourceIndex: 7,
	}}}

	if _, err := Verify(rules, plan); !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("expected verification error, got %v", err)
	}
}

func TestEmptyRulesDefaultDeny(t *testing.T) {
	plan, report, err := Publish(nil, Options{IncludeDefaultDeny: true})
	if err != nil {
		t.Fatalf("Publish empty: %v", err)
	}
	if !report.OK() || report.PointsChecked == 0 {
		t.Fatalf("empty rule verification wrong: %+v", report)
	}
	if len(plan.Shards) != 1 {
		t.Fatalf("expected one default-deny shard, got %d", len(plan.Shards))
	}
	shard := plan.Shards[0]
	if shard.Action != ActionDeny || shard.SourceIndex != -1 {
		t.Fatalf("not default deny: %+v", shard)
	}

	implicit, err := Compress(nil, Options{})
	if err != nil {
		t.Fatalf("Compress empty: %v", err)
	}
	if len(implicit.Shards) != 0 {
		t.Fatalf("implicit default deny should emit no shards, got %d", len(implicit.Shards))
	}
	report2, err := Verify(nil, implicit)
	if err != nil || !report2.OK() {
		t.Fatalf("implicit deny verify failed: err=%v report=%+v", err, report2)
	}
}

// TestRandomizedEquivalence generates many small rule sets, compiles them, and
// checks every endpoint-induced lattice point plus random packets against the
// ordered first-match reference. Both explicit and implicit default deny are
// exercised.
func TestRandomizedEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(20261111))
	protoPool := []uint8{protoICMP, protoTCP, protoUDP, 47, 50, 255}

	for iter := 0; iter < 60; iter++ {
		ruleCount := 1 + rng.Intn(4)
		rules := make([]Rule, ruleCount)
		for i := range rules {
			srcLo, srcHi := randomSpan(rng, 0, 9)
			dstLo, dstHi := randomSpan(rng, 0, 9)
			spLo, spHi := randomSpan(rng, 0, 9)
			dpLo, dpHI := randomSpan(rng, 0, 9)

			protoSet := map[uint8]bool{}
			for j := 0; j < 1+rng.Intn(3); j++ {
				protoSet[protoPool[rng.Intn(len(protoPool))]] = true
			}
			protos := make([]uint8, 0, len(protoSet))
			for p := range protoSet {
				protos = append(protos, p)
			}

			action := ActionAllow
			if rng.Intn(2) == 0 {
				action = ActionDeny
			}
			rules[i] = Rule{
				ID:        ruleName(i),
				SrcIP:     IPRange{From: uint32(srcLo), To: uint32(srcHi)},
				DstIP:     IPRange{From: uint32(dstLo), To: uint32(dstHi)},
				SrcPort:   PortRange{From: uint16(spLo), To: uint16(spHi)},
				DstPort:   PortRange{From: uint16(dpLo), To: uint16(dpHI)},
				Protocols: protos,
				Action:    action,
			}
		}

		plan, err := Compress(rules, Options{IncludeDefaultDeny: iter%2 == 0})
		if err != nil {
			t.Fatalf("iter %d: Compress: %v", iter, err)
		}

		report, err := Verify(rules, plan)
		if err != nil {
			t.Fatalf("iter %d: Verify: %v", iter, err)
		}
		if !report.OK() {
			t.Fatalf("iter %d: lattice mismatch: %+v", iter, report.Mismatches[0])
		}

		// Random packets outside the lattice endpoints.
		for sample := 0; sample < 200; sample++ {
			point := Point{
				SrcIP:      uint32(rng.Intn(12)),
				DstIP:      uint32(rng.Intn(12)),
				SourcePort: uint16(rng.Intn(12)),
				DestPort:   uint16(rng.Intn(12)),
				Protocol:   protoPool[rng.Intn(len(protoPool))],
			}
			want := Decide(rules, point.SrcIP, point.DstIP, point.SourcePort, point.DestPort, point.Protocol)
			got := shardDecision(plan, point)
			if got != want {
				t.Fatalf("iter %d: random point %+v got %s want %s", iter, point, got, want)
			}
			assertAtMostOneMatch(t, plan, point)
		}

		// Published shards must never overlap one another.
		for i := 0; i < len(plan.Shards); i++ {
			for j := i + 1; j < len(plan.Shards); j++ {
				if shardsOverlap(plan.Shards[i], plan.Shards[j]) {
					t.Fatalf("iter %d: shards %d and %d overlap", iter, i, j)
				}
			}
		}
	}
}

func randomSpan(rng *rand.Rand, minV, maxV int) (int, int) {
	lo := minV + rng.Intn(maxV-minV+1)
	hi := lo + rng.Intn(maxV-lo+1)
	return lo, hi
}

func ruleName(i int) string {
	return "rule-" + string(rune('a'+i))
}

func shardDecision(plan *Plan, point Point) Action {
	for _, shard := range plan.Shards {
		if shardMatches(shard, point) {
			return shard.Action
		}
	}
	return ActionDeny
}

func assertAtMostOneMatch(t *testing.T, plan *Plan, point Point) {
	t.Helper()
	matches := 0
	for _, shard := range plan.Shards {
		if shardMatches(shard, point) {
			matches++
		}
	}
	if matches > 1 {
		t.Fatalf("point %+v matched %d shards", point, matches)
	}
}

func shardsOverlap(a, b Shard) bool {
	return rangesOverlapIP(a.SrcIP, b.SrcIP) &&
		rangesOverlapIP(a.DstIP, b.DstIP) &&
		rangesOverlapPort(a.SrcPort, b.SrcPort) &&
		rangesOverlapPort(a.DstPort, b.DstPort) &&
		protosOverlap(a.Protocols, b.Protocols)
}

func rangesOverlapIP(a, b IPRange) bool {
	return a.From <= b.To && b.From <= a.To
}

func rangesOverlapPort(a, b PortRange) bool {
	return a.From <= b.To && b.From <= a.To
}

func protosOverlap(a, b []uint8) bool {
	set := map[uint8]bool{}
	for _, p := range a {
		set[p] = true
	}
	for _, p := range b {
		if set[p] {
			return true
		}
	}
	return false
}
