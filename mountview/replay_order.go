package mountview

import (
	"fmt"
	"sort"
)

type itemKind int

const (
	itemCheckpoint itemKind = iota
	itemEntry
	itemGap
)

type replayItem struct {
	kind  itemKind
	seq   uint64
	entry *LogEntry
	ckpt  *Checkpoint
	gap   *GapSpec
}

// orderReplay merges concrete entries, checkpoints and the gap windows
// declared by checkpoints into one strictly ordered replay stream. A gap is
// emitted immediately before the checkpoint that closes it.
func orderReplay(in *Input) ([]replayItem, error) {
	ckpts := make([]*Checkpoint, len(in.Checkpoints))
	copy(ckpts, in.Checkpoints)
	sort.Slice(ckpts, func(i, j int) bool { return ckpts[i].Seq < ckpts[j].Seq })

	entries := make([]*LogEntry, len(in.Entries))
	copy(entries, in.Entries)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Seq < entries[j].Seq })

	var gaps []*GapSpec
	for i, c := range ckpts {
		for gi := range c.Gaps {
			g := c.Gaps[gi]
			if g.BeforeSeq != c.Seq {
				return nil, fmt.Errorf("mountview: gap before seq %d not closed by checkpoint seq %d", g.BeforeSeq, c.Seq)
			}
			if g.AfterSeq >= g.BeforeSeq {
				return nil, fmt.Errorf("mountview: malformed gap [%d,%d)", g.AfterSeq, g.BeforeSeq)
			}
			if len(g.Allowed) == 0 {
				return nil, fmt.Errorf("mountview: gap [%d,%d) declares no allowed primitives", g.AfterSeq, g.BeforeSeq)
			}
			if i > 0 && g.AfterSeq < ckpts[i-1].Seq {
				return nil, fmt.Errorf("mountview: gap [%d,%d) overlaps earlier checkpoint", g.AfterSeq, g.BeforeSeq)
			}
			gaps = append(gaps, &g)
		}
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].BeforeSeq < gaps[j].BeforeSeq })
	for i := 1; i < len(gaps); i++ {
		if gaps[i].AfterSeq < gaps[i-1].BeforeSeq {
			return nil, fmt.Errorf("mountview: missing windows [%d,%d) and [%d,%d) overlap",
				gaps[i-1].AfterSeq, gaps[i-1].BeforeSeq, gaps[i].AfterSeq, gaps[i].BeforeSeq)
		}
	}

	for _, en := range entries {
		for _, g := range gaps {
			if en.Seq > g.AfterSeq && en.Seq < g.BeforeSeq {
				return nil, fmt.Errorf("mountview: concrete entry seq %d falls inside missing window [%d,%d)",
					en.Seq, g.AfterSeq, g.BeforeSeq)
			}
		}
	}

	var items []replayItem
	for _, c := range ckpts {
		items = append(items, replayItem{kind: itemCheckpoint, seq: c.Seq, ckpt: c})
	}
	for _, en := range entries {
		items = append(items, replayItem{kind: itemEntry, seq: en.Seq, entry: en})
	}
	for _, g := range gaps {
		items = append(items, replayItem{kind: itemGap, seq: g.BeforeSeq, gap: g, ckpt: closingCheckpoint(ckpts, g.BeforeSeq)})
	}
	for i := range in.OpenGaps {
		g := in.OpenGaps[i]
		if g.AfterSeq >= g.BeforeSeq || len(g.Allowed) == 0 {
			return nil, fmt.Errorf("mountview: malformed open gap [%d,%d)", g.AfterSeq, g.BeforeSeq)
		}
		items = append(items, replayItem{kind: itemGap, seq: g.BeforeSeq, gap: &g})
	}
	// At a shared sequence the gap's may-effects must be applied before the
	// checkpoint that reconciles them; entries never share a checkpoint seq.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].seq != items[j].seq {
			return items[i].seq < items[j].seq
		}
		rank := func(k itemKind) int {
			switch k {
			case itemEntry:
				return 0
			case itemGap:
				return 1
			default:
				return 2
			}
		}
		return rank(items[i].kind) < rank(items[j].kind)
	})
	if err := validateCheckpointChain(ckpts); err != nil {
		return nil, err
	}
	return items, nil
}

func closingCheckpoint(ckpts []*Checkpoint, seq uint64) *Checkpoint {
	for _, c := range ckpts {
		if c.Seq == seq {
			return c
		}
	}
	return nil
}

func validateCheckpointChain(ckpts []*Checkpoint) error {
	for i := 1; i < len(ckpts); i++ {
		if !equalBytes(ckpts[i-1].Digest, ckpts[i].ParentDigest) {
			return fmt.Errorf("mountview: checkpoint hash chain broken between seq %d and %d",
				ckpts[i-1].Seq, ckpts[i].Seq)
		}
	}
	return nil
}
