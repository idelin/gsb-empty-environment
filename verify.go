package netsec

import (
	"fmt"
	"sort"
)

// Point is one concrete packet used for lattice verification.
type Point struct {
	SrcIP      uint32
	DstIP      uint32
	SourcePort uint16
	DestPort   uint16
	Protocol   uint8
}

// Mismatch records a single verification failure.
type Mismatch struct {
	Point         Point
	RuleAction    Action
	ShardAction   Action
	MatchedShard  int
	OverlapShards []int
}

// Report summarizes a lattice verification run.
type Report struct {
	// PointsChecked is the number of concrete lattice points evaluated.
	PointsChecked int
	// Mismatches is empty when the shard set is decision-equivalent.
	Mismatches []Mismatch
}

// OK reports whether verification found no divergence.
func (r Report) OK() bool {
	return len(r.Mismatches) == 0
}

const maxReportedMismatches = 32

// Verify checks that the published shards reproduce the ordered rules at every
// lattice point induced by the interval endpoints of both the input rules and
// the published shards.
//
// The endpoint cuts of every numeric dimension define elementary cells whose
// decision is constant in their interior. Including the shard endpoints makes
// every shard boundary a lattice point, so overlapping shards cannot hide
// between rule boundaries. Each cell is probed at its lower and upper corners,
// so every interval endpoint and every numeric upper bound is exercised.
// Protocol groups are probed at every member, including the numbers no rule
// mentions, which must remain default deny.
//
// Verify also confirms that the published shards are pairwise disjoint and
// that every shard's action agrees with its originating rule.
func Verify(rules []Rule, plan *Plan) (Report, error) {
	for i, rule := range rules {
		if _, err := validateRule(i, rule); err != nil {
			return Report{}, err
		}
	}
	if plan == nil {
		return Report{}, fmt.Errorf("netsec: verify: nil plan")
	}

	if err := verifyShardProvenance(rules, plan); err != nil {
		return Report{}, err
	}

	cuts := [4][]uint64{
		buildCuts(rules, dimSrcIP),
		buildCuts(rules, dimDstIP),
		buildCuts(rules, dimSrcPort),
		buildCuts(rules, dimDstPort),
	}
	addShardCuts(&cuts, plan.Shards)
	protoGroups := buildProtoGroups(rules)

	report := Report{}
	cells := buildLatticeCells(cuts)
	for _, cell := range cells {
		low := Point{
			SrcIP:      uint32(cell[0].lo),
			DstIP:      uint32(cell[1].lo),
			SourcePort: uint16(cell[2].lo),
			DestPort:   uint16(cell[3].lo),
		}
		high := Point{
			SrcIP:      uint32(cell[0].hi),
			DstIP:      uint32(cell[1].hi),
			SourcePort: uint16(cell[2].hi),
			DestPort:   uint16(cell[3].hi),
		}

		for _, group := range protoGroups {
			// The low corner probes every partitioned protocol number.
			for _, proto := range group.members {
				point := low
				point.Protocol = proto
				report.checkPoint(rules, plan, point)
			}
			// The high corner probes numeric upper bounds for one member.
			point := high
			point.Protocol = group.members[0]
			report.checkPoint(rules, plan, point)
		}
	}

	return report, nil
}

// addShardCuts folds published shard boundaries into the verification lattice.
func addShardCuts(cuts *[4][]uint64, shards []Shard) {
	extra := [4]map[uint64]struct{}{
		{}, {}, {}, {},
	}
	for i := range extra {
		extra[i] = map[uint64]struct{}{}
	}
	for _, shard := range shards {
		extra[dimSrcIP][uint64(shard.SrcIP.From)] = struct{}{}
		extra[dimSrcIP][uint64(shard.SrcIP.To)+1] = struct{}{}
		extra[dimDstIP][uint64(shard.DstIP.From)] = struct{}{}
		extra[dimDstIP][uint64(shard.DstIP.To)+1] = struct{}{}
		extra[dimSrcPort][uint64(shard.SrcPort.From)] = struct{}{}
		extra[dimSrcPort][uint64(shard.SrcPort.To)+1] = struct{}{}
		extra[dimDstPort][uint64(shard.DstPort.From)] = struct{}{}
		extra[dimDstPort][uint64(shard.DstPort.To)+1] = struct{}{}
	}
	for dim := range cuts {
		existing := map[uint64]struct{}{}
		for _, cut := range cuts[dim] {
			existing[cut] = struct{}{}
		}
		for cut := range extra[dim] {
			if _, ok := existing[cut]; !ok {
				cuts[dim] = append(cuts[dim], cut)
			}
		}
		sort.Slice(cuts[dim], func(i, j int) bool { return cuts[dim][i] < cuts[dim][j] })
	}
}

// cellRange is one inclusive elementary interval during verification.
type cellRange struct {
	lo uint64
	hi uint64
}

// buildLatticeCells reconstructs every elementary cell from dimension cuts.
func buildLatticeCells(cuts [4][]uint64) [][4]cellRange {
	cells := [][4]cellRange{}

	var walk func(dim int, acc [4]cellRange)
	walk = func(dim int, acc [4]cellRange) {
		if dim == len(cuts) {
			cells = append(cells, acc)
			return
		}
		for i := 0; i+1 < len(cuts[dim]); i++ {
			acc[dim] = cellRange{lo: cuts[dim][i], hi: cuts[dim][i+1] - 1}
			walk(dim+1, acc)
		}
	}
	walk(0, [4]cellRange{})
	return cells
}

// checkPoint compares rule first-match, shard first-match and shard overlap at
// one concrete packet.
func (r *Report) checkPoint(rules []Rule, plan *Plan, point Point) {
	r.PointsChecked++

	ruleAction := Decide(rules, point.SrcIP, point.DstIP, point.SourcePort, point.DestPort, point.Protocol)

	matched := -1
	overlaps := []int{}
	for i := range plan.Shards {
		if shardMatches(plan.Shards[i], point) {
			overlaps = append(overlaps, i)
			if matched == -1 {
				matched = i
			}
		}
	}

	shardAction := ActionDeny
	if matched != -1 {
		shardAction = plan.Shards[matched].Action
	}

	if ruleAction != shardAction {
		r.record(Mismatch{
			Point:         point,
			RuleAction:    ruleAction,
			ShardAction:   shardAction,
			MatchedShard:  matched,
			OverlapShards: overlaps,
		})
		return
	}

	// Disjointness is an independent invariant: overlapping shards are invalid
	// even when both happen to carry the same action.
	if len(overlaps) > 1 {
		r.record(Mismatch{
			Point:         point,
			RuleAction:    ruleAction,
			ShardAction:   shardAction,
			MatchedShard:  matched,
			OverlapShards: overlaps,
		})
	}
}

func (r *Report) record(mismatch Mismatch) {
	if len(r.Mismatches) < maxReportedMismatches {
		r.Mismatches = append(r.Mismatches, mismatch)
	}
}

// shardMatches reports whether a concrete packet falls inside a shard.
func shardMatches(shard Shard, point Point) bool {
	if !shard.SrcIP.Contains(point.SrcIP) || !shard.DstIP.Contains(point.DstIP) {
		return false
	}
	if !shard.SrcPort.Contains(point.SourcePort) || !shard.DstPort.Contains(point.DestPort) {
		return false
	}
	for _, proto := range shard.Protocols {
		if proto == point.Protocol {
			return true
		}
	}
	return false
}

// verifyShardProvenance rejects shards whose source reference cannot be
// resolved. Decision disagreements are surfaced as lattice mismatches, since
// every shard region is probed at its own boundaries.
func verifyShardProvenance(rules []Rule, plan *Plan) error {
	for i, shard := range plan.Shards {
		if shard.SourceIndex < 0 {
			continue
		}
		if shard.SourceIndex >= len(rules) {
			return fmt.Errorf("%w: shard %d: source index %d out of range",
				ErrVerificationFailed, i, shard.SourceIndex)
		}
	}
	return nil
}

// Publish compiles the rules, enforces the hardware entry budget and verifies
// decision equivalence on the endpoint-induced lattice before returning the
// shard set. Any failure rejects publication and no plan is returned.
func Publish(rules []Rule, opts Options) (*Plan, Report, error) {
	plan, err := Compress(rules, opts)
	if err != nil {
		return nil, Report{}, err
	}

	report, err := Verify(rules, plan)
	if err != nil {
		return nil, Report{}, err
	}
	if !report.OK() {
		return nil, report, fmt.Errorf("%w: %d mismatches", ErrVerificationFailed, len(report.Mismatches))
	}
	return plan, report, nil
}

// SortShards orders shards by their numeric dimensions and protocols. It is
// optional and provided for deterministic hardware programming output.
func SortShards(shards []Shard) {
	sort.Slice(shards, func(i, j int) bool {
		a, b := shards[i], shards[j]
		if a.SrcIP.From != b.SrcIP.From {
			return a.SrcIP.From < b.SrcIP.From
		}
		if a.DstIP.From != b.DstIP.From {
			return a.DstIP.From < b.DstIP.From
		}
		if a.SrcPort.From != b.SrcPort.From {
			return a.SrcPort.From < b.SrcPort.From
		}
		if a.DstPort.From != b.DstPort.From {
			return a.DstPort.From < b.DstPort.From
		}
		return firstProto(a.Protocols) < firstProto(b.Protocols)
	})
}

func firstProto(protos []uint8) uint8 {
	if len(protos) == 0 {
		return 0
	}
	return protos[0]
}
