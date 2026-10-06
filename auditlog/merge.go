package auditlog

import (
	"fmt"
	"sort"
)

// Source is a named replica of the audit log. Entries may arrive in any
// order, may contain duplicates, and may be missing segments.
type Source struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

// EntryVariant is one distinct version of an entry at a sequence number,
// together with the sources that vouch for it.
type EntryVariant struct {
	Entry   Entry    `json:"entry"`
	Sources []string `json:"sources"`
}

// ForkEvidence records a divergence: two or more self-consistent but
// mutually incompatible entries claim the same sequence number. Both sides
// are preserved; the merge never picks one automatically.
type ForkEvidence struct {
	Seq      uint64         `json:"seq"`
	Variants []EntryVariant `json:"variants"`
}

// TamperEvidence records an entry whose stored hash does not match its
// recomputed hash. Such entries are excluded from the chain.
type TamperEvidence struct {
	Source string `json:"source"`
	Entry  Entry  `json:"entry"`
}

// StopReason explains why the trusted prefix ends where it does.
type StopReason string

const (
	// StopExhausted means every known entry is part of the trusted prefix.
	StopExhausted StopReason = "exhausted: all known entries verified"
	// StopGap means no replica supplied the next sequence number.
	StopGap StopReason = "gap: missing segment in all replicas"
	// StopFork means replicas diverge at the next sequence number.
	StopFork StopReason = "fork: conflicting entries, manual resolution required"
	// StopBrokenLink means the next entry does not chain to the prefix.
	StopBrokenLink StopReason = "broken-link: prev_hash does not match prefix head"
	// StopNoGenesis means no entry with a zero prev_hash was found.
	StopNoGenesis StopReason = "no-genesis: chain start not present in any replica"
)

// MergeResult is the outcome of merging several replicas.
type MergeResult struct {
	// TrustedPrefix is the longest common prefix that is hash-verified,
	// gap-free from genesis, and agreed upon by every replica covering it.
	TrustedPrefix []Entry `json:"trusted_prefix"`
	// Forks holds both sides of every detected divergence as evidence.
	Forks []ForkEvidence `json:"forks,omitempty"`
	// Tampered holds entries that failed self-verification.
	Tampered []TamperEvidence `json:"tampered,omitempty"`
	// StopReason explains where and why the prefix ends.
	StopReason StopReason `json:"stop_reason"`
}

// Merge combines replicas into the longest common trusted prefix.
//
// The algorithm never selects a log by length or highest sequence number:
// an entry is trusted only when its hash chain is intact from genesis and
// every replica that covers its sequence number agrees on its content.
// On divergence both branches are kept as evidence and the prefix stops
// before the fork.
func Merge(sources []Source) MergeResult {
	res := MergeResult{StopReason: StopExhausted}

	// Index self-consistent entries by sequence number. Identical entries
	// from several sources (or duplicated within one) collapse into a
	// single variant.
	variants := map[uint64][]EntryVariant{}
	seen := map[uint64]map[Hash]int{} // seq -> entry hash -> variant index
	for _, src := range sources {
		inSource := map[uint64]map[Hash]bool{}
		for _, e := range src.Entries {
			if !e.VerifySelf() {
				res.Tampered = append(res.Tampered, TamperEvidence{Source: src.Name, Entry: e})
				continue
			}
			if inSource[e.Seq] == nil {
				inSource[e.Seq] = map[Hash]bool{}
			}
			if inSource[e.Seq][e.Hash] {
				continue // exact duplicate within one replica
			}
			inSource[e.Seq][e.Hash] = true

			if seen[e.Seq] == nil {
				seen[e.Seq] = map[Hash]int{}
			}
			if idx, ok := seen[e.Seq][e.Hash]; ok {
				variants[e.Seq][idx].Sources = append(variants[e.Seq][idx].Sources, src.Name)
				continue
			}
			seen[e.Seq][e.Hash] = len(variants[e.Seq])
			variants[e.Seq] = append(variants[e.Seq], EntryVariant{
				Entry:   e,
				Sources: []string{src.Name},
			})
		}
	}

	// Locate genesis: the lowest-seq entry chaining to ZeroHash.
	seqs := make([]uint64, 0, len(variants))
	for s := range variants {
		seqs = append(seqs, s)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	genesisIdx := -1
	for i, s := range seqs {
		for _, v := range variants[s] {
			if v.Entry.PrevHash == ZeroHash {
				genesisIdx = i
				break
			}
		}
		if genesisIdx >= 0 {
			break
		}
	}
	if genesisIdx < 0 {
		res.StopReason = StopNoGenesis
		return res
	}

	// Walk the chain from genesis, one sequence number at a time.
	expectedPrev := ZeroHash
	for i := genesisIdx; ; i++ {
		if i >= len(seqs) {
			res.StopReason = StopExhausted
			return res
		}
		if seqs[i] != firstSeq(seqs, genesisIdx)+uint64(i-genesisIdx) {
			res.StopReason = StopGap
			return res
		}
		vs := variants[seqs[i]]
		if len(vs) > 1 {
			sortVariants(vs)
			res.Forks = append(res.Forks, ForkEvidence{Seq: seqs[i], Variants: vs})
			res.StopReason = StopFork
			return res
		}
		e := vs[0].Entry
		if e.PrevHash != expectedPrev {
			res.StopReason = StopBrokenLink
			return res
		}
		res.TrustedPrefix = append(res.TrustedPrefix, e)
		expectedPrev = e.Hash
	}
}

func firstSeq(seqs []uint64, i int) uint64 { return seqs[i] }

// sortVariants orders fork variants deterministically by hash so reports
// are stable; it does not imply any preference between branches.
func sortVariants(vs []EntryVariant) {
	sort.Slice(vs, func(i, j int) bool {
		return vs[i].Entry.Hash.Hex() < vs[j].Entry.Hash.Hex()
	})
}

// Summary renders a short human-readable merge report.
func (r MergeResult) Summary() string {
	s := fmt.Sprintf("trusted prefix: %d entries\nstop reason: %s", len(r.TrustedPrefix), r.StopReason)
	for _, f := range r.Forks {
		s += fmt.Sprintf("\nfork at seq %d with %d variants (evidence kept, no side selected)", f.Seq, len(f.Variants))
	}
	for _, t := range r.Tampered {
		s += fmt.Sprintf("\ntampered entry from %q at seq %d excluded", t.Source, t.Entry.Seq)
	}
	return s
}
