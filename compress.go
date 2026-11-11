package netsec

import (
	"fmt"
	"sort"
)

// Numeric limits of the four packet dimensions. Cuts are stored as uint64 so
// that the exclusive upper bounds 2^32 and 2^16 never overflow.
const (
	ipMaxCut   = uint64(1) << 32
	portMaxCut = uint64(1) << 16
)

// dimension identifies one of the four numeric packet dimensions.
type dimension int

const (
	dimSrcIP dimension = iota
	dimDstIP
	dimSrcPort
	dimDstPort
)

// box is an elementary hyper-rectangle over the four numeric dimensions plus
// the protocol group, carrying a constant first-match decision.
type box struct {
	srcLo, srcHi uint32 // inclusive
	dstLo, dstHi uint32
	spLo, spHi   uint16
	dpLo, dpHI   uint16
	protos       []uint8
	action       Action
	sourceIndex  int
}

// Compress builds a set of disjoint shards that is decision-equivalent to the
// ordered input rules under first-match semantics.
//
// The input interval endpoints induce a finite grid of elementary cells; each
// cell has a constant decision because no rule boundary crosses its interior.
// Cells sharing a decision are swept and merged along every dimension to cut
// the hardware entry count. Minimality is not attempted.
//
// Every emitted shard records the index and ID of the rule responsible for its
// decision. When Options.IncludeDefaultDeny is set, unmatched space is emitted
// as explicit deny shards marked with source index -1.
func Compress(rules []Rule, opts Options) (*Plan, error) {
	if opts.MaxEntries < 0 {
		return nil, fmt.Errorf("%w: negative entry budget", ErrBudgetExceeded)
	}

	normalized := make([]Rule, len(rules))
	for i, rule := range rules {
		validated, err := validateRule(i, rule)
		if err != nil {
			return nil, err
		}
		normalized[i] = validated
	}

	srcIPCuts := buildCuts(normalized, dimSrcIP)
	dstIPCuts := buildCuts(normalized, dimDstIP)
	srcPortCuts := buildCuts(normalized, dimSrcPort)
	dstPortCuts := buildCuts(normalized, dimDstPort)

	protoGroups := buildProtoGroups(normalized)

	boxes := enumerateCells(normalized, srcIPCuts, dstIPCuts, srcPortCuts, dstPortCuts, protoGroups)
	boxes = mergeAll(boxes)

	shards := make([]Shard, 0, len(boxes))
	for _, b := range boxes {
		if b.sourceIndex < 0 && !opts.IncludeDefaultDeny {
			continue
		}
		shards = append(shards, boxToShard(b, normalized))
	}

	if opts.MaxEntries > 0 && len(shards) > opts.MaxEntries {
		return nil, fmt.Errorf("%w: need %d entries, budget %d", ErrBudgetExceeded, len(shards), opts.MaxEntries)
	}

	return &Plan{Shards: shards, RuleCount: len(normalized)}, nil
}

// buildCuts collects sorted, unique half-open cut points for one dimension.
// Every rule interval [from, to] contributes cuts at from and to+1, bounded by
// the dimension minimum and its exclusive numeric upper bound.
func buildCuts(rules []Rule, dim dimension) []uint64 {
	cuts := map[uint64]struct{}{}
	switch dim {
	case dimSrcIP, dimDstIP:
		cuts[0] = struct{}{}
		cuts[ipMaxCut] = struct{}{}
	default:
		cuts[0] = struct{}{}
		cuts[portMaxCut] = struct{}{}
	}

	for _, rule := range rules {
		switch dim {
		case dimSrcIP:
			cuts[uint64(rule.SrcIP.From)] = struct{}{}
			cuts[uint64(rule.SrcIP.To)+1] = struct{}{}
		case dimDstIP:
			cuts[uint64(rule.DstIP.From)] = struct{}{}
			cuts[uint64(rule.DstIP.To)+1] = struct{}{}
		case dimSrcPort:
			cuts[uint64(rule.SrcPort.From)] = struct{}{}
			cuts[uint64(rule.SrcPort.To)+1] = struct{}{}
		case dimDstPort:
			cuts[uint64(rule.DstPort.From)] = struct{}{}
			cuts[uint64(rule.DstPort.To)+1] = struct{}{}
		}
	}

	out := make([]uint64, 0, len(cuts))
	for cut := range cuts {
		out = append(out, cut)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// protoGroup is a maximal set of protocols with identical rule membership.
type protoGroup struct {
	members []uint8
	// signature identifies which rules match these protocols, independent of
	// the numeric dimensions.
	signature string
}

// buildProtoGroups partitions the 256 protocol numbers by membership vector.
// Protocols no rule mentions form one shared default-deny group.
func buildProtoGroups(rules []Rule) []protoGroup {
	type indexedGroup struct {
		sig     string
		members []uint8
	}
	groups := map[string]*indexedGroup{}
	order := []string{}

	for proto := 0; proto <= 255; proto++ {
		sig := protoSignature(rules, uint8(proto))
		group, ok := groups[sig]
		if !ok {
			group = &indexedGroup{sig: sig}
			groups[sig] = group
			order = append(order, sig)
		}
		group.members = append(group.members, uint8(proto))
	}

	out := make([]protoGroup, 0, len(order))
	for _, sig := range order {
		group := groups[sig]
		out = append(out, protoGroup{members: group.members, signature: sig})
	}
	return out
}

// protoSignature builds a compact membership key for one protocol number.
func protoSignature(rules []Rule, proto uint8) string {
	buf := make([]byte, 0, len(rules))
	for _, rule := range rules {
		bit := byte('0')
		for _, p := range rule.Protocols {
			if p == proto {
				bit = '1'
				break
			}
		}
		buf = append(buf, bit)
	}
	return string(buf)
}

// enumerateCells walks every elementary grid cell. Each cell is disjoint from
// all others and carries the first matching rule's decision.
func enumerateCells(
	rules []Rule,
	srcIPCuts, dstIPCuts, srcPortCuts, dstPortCuts []uint64,
	protoGroups []protoGroup,
) []box {
	boxes := make([]box, 0)

	for si := 0; si+1 < len(srcIPCuts); si++ {
		srcLo, srcHi := uint32(srcIPCuts[si]), uint32(srcIPCuts[si+1]-1)
		for di := 0; di+1 < len(dstIPCuts); di++ {
			dstLo, dstHi := uint32(dstIPCuts[di]), uint32(dstIPCuts[di+1]-1)
			for spi := 0; spi+1 < len(srcPortCuts); spi++ {
				spLo, spHi := uint16(srcPortCuts[spi]), uint16(srcPortCuts[spi+1]-1)
				for dpi := 0; dpi+1 < len(dstPortCuts); dpi++ {
					dpLo, dpHI := uint16(dstPortCuts[dpi]), uint16(dstPortCuts[dpi+1]-1)
					for _, group := range protoGroups {
						proto := group.members[0]
						sourceIndex := -1
						action := ActionDeny
						for idx, rule := range rules {
							if ruleMatches(rule, srcLo, dstLo, spLo, dpLo, proto) {
								sourceIndex = idx
								action = rule.Action
								break
							}
						}
						boxes = append(boxes, box{
							srcLo:       srcLo,
							srcHi:       srcHi,
							dstLo:       dstLo,
							dstHi:       dstHi,
							spLo:        spLo,
							spHi:        spHi,
							dpLo:        dpLo,
							dpHI:        dpHI,
							protos:      append([]uint8(nil), group.members...),
							action:      action,
							sourceIndex: sourceIndex,
						})
					}
				}
			}
		}
	}

	return boxes
}

// mergeKey identifies boxes that may be merged along one dimension: they must
// agree on the decision, provenance and every other dimension.
type mergeKey struct {
	sourceIndex int
	action      Action
	srcLo       uint32
	srcHi       uint32
	dstLo       uint32
	dstHi       uint32
	spLo        uint16
	spHi        uint16
	dpLo        uint16
	dpHI        uint16
	protos      string
}

// interval is a generic inclusive interval over any numeric dimension;
// uint64 storage keeps 32-bit upper bounds safe.
type interval struct {
	lo uint64
	hi uint64
}

// mergeAll sweeps every dimension. Four passes always suffice for these four
// dimensions, but running them as fixed rounds keeps the result deterministic.
func mergeAll(boxes []box) []box {
	current := boxes
	for round := 0; round < 4; round++ {
		current = mergeDimension(current, dimSrcIP)
		current = mergeDimension(current, dimDstIP)
		current = mergeDimension(current, dimSrcPort)
		current = mergeDimension(current, dimDstPort)
	}
	current = mergeProtos(current)
	return current
}

// mergeDimension coalesces adjacent intervals in one dimension while keeping
// all other dimensions, the protocol set and the decision identical.
func mergeDimension(boxes []box, dim dimension) []box {
	groups := map[mergeKey][]interval{}
	order := []mergeKey{}

	for _, b := range boxes {
		key := mergeKey{
			sourceIndex: b.sourceIndex,
			action:      b.action,
			srcLo:       b.srcLo,
			srcHi:       b.srcHi,
			dstLo:       b.dstLo,
			dstHi:       b.dstHi,
			spLo:        b.spLo,
			spHi:        b.spHi,
			dpLo:        b.dpLo,
			dpHI:        b.dpHI,
			protos:      string(b.protos),
		}
		var span interval
		switch dim {
		case dimSrcIP:
			key.srcLo, key.srcHi = 0, 0
			span = interval{lo: uint64(b.srcLo), hi: uint64(b.srcHi)}
		case dimDstIP:
			key.dstLo, key.dstHi = 0, 0
			span = interval{lo: uint64(b.dstLo), hi: uint64(b.dstHi)}
		case dimSrcPort:
			key.spLo, key.spHi = 0, 0
			span = interval{lo: uint64(b.spLo), hi: uint64(b.spHi)}
		case dimDstPort:
			key.dpLo, key.dpHI = 0, 0
			span = interval{lo: uint64(b.dpLo), hi: uint64(b.dpHI)}
		}
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], span)
	}

	merged := make([]box, 0, len(boxes))
	for _, key := range order {
		for _, span := range mergeIntervals(groups[key]) {
			b := box{
				srcLo:       key.srcLo,
				srcHi:       key.srcHi,
				dstLo:       key.dstLo,
				dstHi:       key.dstHi,
				spLo:        key.spLo,
				spHi:        key.spHi,
				dpLo:        key.dpLo,
				dpHI:        key.dpHI,
				protos:      []uint8(key.protos),
				action:      key.action,
				sourceIndex: key.sourceIndex,
			}
			switch dim {
			case dimSrcIP:
				b.srcLo, b.srcHi = uint32(span.lo), uint32(span.hi)
			case dimDstIP:
				b.dstLo, b.dstHi = uint32(span.lo), uint32(span.hi)
			case dimSrcPort:
				b.spLo, b.spHi = uint16(span.lo), uint16(span.hi)
			case dimDstPort:
				b.dpLo, b.dpHI = uint16(span.lo), uint16(span.hi)
			}
			merged = append(merged, b)
		}
	}
	return merged
}

// mergeIntervals unions sorted adjacent or overlapping inclusive intervals.
func mergeIntervals(spans []interval) []interval {
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })

	merged := make([]interval, 0, len(spans))
	for _, span := range spans {
		if len(merged) == 0 || span.lo > merged[len(merged)-1].hi+1 {
			merged = append(merged, span)
			continue
		}
		if span.hi > merged[len(merged)-1].hi {
			merged[len(merged)-1].hi = span.hi
		}
	}
	return merged
}

// protoKey identifies boxes that differ only in their protocol sets.
type protoKey struct {
	sourceIndex int
	action      Action
	srcLo       uint32
	srcHi       uint32
	dstLo       uint32
	dstHi       uint32
	spLo        uint16
	spHi        uint16
	dpLo        uint16
	dpHI        uint16
}

// mergeProtos unions protocol sets for boxes that are otherwise identical.
// Two such boxes can only originate from the same rule, so the decision and
// provenance stay intact.
func mergeProtos(boxes []box) []box {
	groups := map[protoKey]map[uint8]struct{}{}
	order := []protoKey{}

	for _, b := range boxes {
		key := protoKey{
			sourceIndex: b.sourceIndex,
			action:      b.action,
			srcLo:       b.srcLo,
			srcHi:       b.srcHi,
			dstLo:       b.dstLo,
			dstHi:       b.dstHi,
			spLo:        b.spLo,
			spHi:        b.spHi,
			dpLo:        b.dpLo,
			dpHI:        b.dpHI,
		}
		set, ok := groups[key]
		if !ok {
			set = map[uint8]struct{}{}
			groups[key] = set
			order = append(order, key)
		}
		for _, p := range b.protos {
			set[p] = struct{}{}
		}
	}

	merged := make([]box, 0, len(order))
	for _, key := range order {
		protos := make([]uint8, 0, len(groups[key]))
		for p := range groups[key] {
			protos = append(protos, p)
		}
		sort.Slice(protos, func(i, j int) bool { return protos[i] < protos[j] })
		merged = append(merged, box{
			srcLo:       key.srcLo,
			srcHi:       key.srcHi,
			dstLo:       key.dstLo,
			dstHi:       key.dstHi,
			spLo:        key.spLo,
			spHi:        key.spHi,
			dpLo:        key.dpLo,
			dpHI:        key.dpHI,
			protos:      protos,
			action:      key.action,
			sourceIndex: key.sourceIndex,
		})
	}
	return merged
}

// boxToShard attaches source provenance and a human-readable explanation.
func boxToShard(b box, rules []Rule) Shard {
	shard := Shard{
		SrcIP:       IPRange{From: b.srcLo, To: b.srcHi},
		DstIP:       IPRange{From: b.dstLo, To: b.dstHi},
		SrcPort:     PortRange{From: b.spLo, To: b.spHi},
		DstPort:     PortRange{From: b.dpLo, To: b.dpHI},
		Protocols:   append([]uint8(nil), b.protos...),
		Action:      b.action,
		SourceIndex: b.sourceIndex,
	}

	if b.sourceIndex < 0 {
		shard.SourceID = "default-deny"
		shard.Explanation = "implicit default deny: no ordered rule matches this region"
		return shard
	}

	rule := rules[b.sourceIndex]
	shard.SourceID = rule.ID
	label := rule.ID
	if label == "" {
		label = fmt.Sprintf("rule[%d]", b.sourceIndex)
	}
	shard.Explanation = fmt.Sprintf(
		"first-match %s from %s (rule index %d)",
		b.action, label, b.sourceIndex,
	)
	if rule.Comment != "" {
		shard.Explanation += ": " + rule.Comment
	}
	return shard
}
