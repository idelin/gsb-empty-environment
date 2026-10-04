// Package snapshot computes the greatest transaction-consistent
// cross-partition read snapshot not exceeding a given watermark.
//
// Each partition owns a totally ordered list of versions. Every version is
// produced by exactly one transaction, and a transaction may produce
// versions on several partitions. A snapshot (a per-partition prefix of the
// version lists) is consistent iff it never exposes half of a transaction:
// for every transaction, either all of its versions are in the snapshot or
// none are.
//
// Solve rolls the watermark frontier back until the snapshot becomes
// consistent, recording a reason for every rollback so the full cause chain
// of any excluded version can be reconstructed afterwards. Transaction
// associations may form cycles; the rollback process still terminates
// because the sum of all partition frontiers is a non-negative integer that
// strictly decreases with every rollback event.
package snapshot

import "fmt"

const (
	notExcluded = -2
	// WatermarkCause marks versions excluded directly by the watermark
	// rather than by a rollback event. It is the root of every reason chain.
	WatermarkCause = -1
)

// VersionID identifies a version by partition and commit index.
type VersionID struct {
	Partition int
	Index     int
}

func (v VersionID) String() string {
	return fmt.Sprintf("p%d#%d", v.Partition, v.Index)
}

// Problem is the solver input.
type Problem struct {
	// Partitions[p] lists transaction IDs in commit order; position i is
	// the i-th version of partition p. A transaction that writes to
	// several partitions appears once per version it produced, which is
	// exactly the transaction association.
	Partitions [][]string
	// Watermark[p] bounds the snapshot of partition p to its first
	// Watermark[p] versions (indexes < Watermark[p]).
	Watermark []int
}

// RollbackEvent records one frontier decrease of one partition.
type RollbackEvent struct {
	ID           int
	Txn          string    // split transaction that forced the rollback
	Partition    int       // partition whose frontier decreased
	FromFrontier int       // frontier before the rollback
	ToFrontier   int       // frontier after the rollback
	Witness      VersionID // an excluded version of Txn that proved the split
	WitnessCause int       // event ID that excluded Witness, or WatermarkCause
}

// ChainStep is one link of a rollback reason chain.
type ChainStep struct {
	Version VersionID
	Txn     string
	Cause   int // rollback event ID, or WatermarkCause
}

// Solution is the greatest consistent snapshot not exceeding the watermark.
type Solution struct {
	// Frontier[p] is the number of leading versions of partition p
	// included in the snapshot.
	Frontier []int
	// Events lists every rollback in the order it happened.
	Events []RollbackEvent

	versions [][]string
	excluded [][]int
}

type txnInfo struct {
	parts []int       // partitions touched, in first-seen order
	first map[int]int // partition -> first version index
	last  map[int]int // partition -> last version index
	inc   int         // partitions with at least one included version
	exc   int         // partitions with at least one excluded version
}

// Solve computes the greatest transaction-consistent snapshot whose
// per-partition frontier does not exceed the watermark.
func Solve(p Problem) (*Solution, error) {
	if len(p.Partitions) != len(p.Watermark) {
		return nil, fmt.Errorf("snapshot: %d partitions but %d watermarks",
			len(p.Partitions), len(p.Watermark))
	}
	for pt, wm := range p.Watermark {
		if wm < 0 || wm > len(p.Partitions[pt]) {
			return nil, fmt.Errorf("snapshot: watermark %d out of range for partition %d with %d versions",
				wm, pt, len(p.Partitions[pt]))
		}
	}

	n := len(p.Partitions)
	frontier := make([]int, n)
	excluded := make([][]int, n)
	for pt := range p.Partitions {
		frontier[pt] = p.Watermark[pt]
		excluded[pt] = make([]int, len(p.Partitions[pt]))
		for i := range excluded[pt] {
			if i < p.Watermark[pt] {
				excluded[pt][i] = notExcluded
			} else {
				excluded[pt][i] = WatermarkCause
			}
		}
	}

	order := []string{}
	txns := map[string]*txnInfo{}
	for pt, vers := range p.Partitions {
		for i, id := range vers {
			info, ok := txns[id]
			if !ok {
				info = &txnInfo{first: map[int]int{}, last: map[int]int{}}
				txns[id] = info
				order = append(order, id)
			}
			if _, ok := info.first[pt]; !ok {
				info.first[pt] = i
				info.parts = append(info.parts, pt)
			}
			info.last[pt] = i
		}
	}

	for _, id := range order {
		info := txns[id]
		for _, pt := range info.parts {
			if info.first[pt] < frontier[pt] {
				info.inc++
			}
			if info.last[pt] >= frontier[pt] {
				info.exc++
			}
		}
	}

	// A transaction is split iff inc > 0 && exc > 0. Both counters move
	// monotonically (inc only decreases, exc only increases), so a
	// transaction becomes split at most once and is enqueued at most once.
	queue := []string{}
	enqueued := map[string]bool{}
	push := func(id string) {
		info := txns[id]
		if info.inc > 0 && info.exc > 0 && !enqueued[id] {
			enqueued[id] = true
			queue = append(queue, id)
		}
	}
	for _, id := range order {
		push(id)
	}

	var events []RollbackEvent
	for head := 0; head < len(queue); head++ {
		id := queue[head]
		info := txns[id]
		if info.inc == 0 || info.exc == 0 {
			continue
		}
		var witness VersionID
		found := false
		for _, pt := range info.parts {
			if info.last[pt] >= frontier[pt] {
				witness = VersionID{Partition: pt, Index: info.last[pt]}
				found = true
				break
			}
		}
		if !found {
			continue
		}
		witnessCause := excluded[witness.Partition][witness.Index]
		for _, pt := range info.parts {
			target := info.first[pt]
			from := frontier[pt]
			if target >= from {
				continue
			}
			ev := RollbackEvent{
				ID:           len(events),
				Txn:          id,
				Partition:    pt,
				FromFrontier: from,
				ToFrontier:   target,
				Witness:      witness,
				WitnessCause: witnessCause,
			}
			events = append(events, ev)
			for i := from - 1; i >= target; i-- {
				excluded[pt][i] = ev.ID
				ui := txns[p.Partitions[pt][i]]
				if i == ui.first[pt] {
					ui.inc--
				}
				if i == ui.last[pt] {
					ui.exc++
				}
				push(p.Partitions[pt][i])
			}
			frontier[pt] = target
		}
	}

	return &Solution{
		Frontier: frontier,
		Events:   events,
		versions: p.Partitions,
		excluded: excluded,
	}, nil
}

// Included reports whether version v is part of the snapshot.
func (s *Solution) Included(v VersionID) bool {
	return v.Partition >= 0 && v.Partition < len(s.Frontier) &&
		v.Index >= 0 && v.Index < s.Frontier[v.Partition]
}

// IsConsistent reports whether the snapshot exposes every transaction
// either fully or not at all.
func (s *Solution) IsConsistent() bool {
	total := map[string]int{}
	included := map[string]int{}
	for pt, vers := range s.versions {
		for i, id := range vers {
			total[id]++
			if i < s.Frontier[pt] {
				included[id]++
			}
		}
	}
	for id, c := range included {
		if c != total[id] {
			return false
		}
	}
	return true
}

// ReasonChain returns the cause chain explaining why version v was rolled
// back, from v itself up to a version excluded directly by the watermark.
// The second return value is false if v is included in the snapshot.
//
// The chain is always finite: the witness of event e was excluded before e
// happened, so event IDs strictly decrease along the chain even when the
// transaction association graph contains cycles.
func (s *Solution) ReasonChain(v VersionID) ([]ChainStep, bool) {
	if v.Partition < 0 || v.Partition >= len(s.excluded) ||
		v.Index < 0 || v.Index >= len(s.excluded[v.Partition]) {
		return nil, false
	}
	if s.excluded[v.Partition][v.Index] == notExcluded {
		return nil, false
	}
	var steps []ChainStep
	cur := v
	for {
		cause := s.excluded[cur.Partition][cur.Index]
		steps = append(steps, ChainStep{
			Version: cur,
			Txn:     s.versions[cur.Partition][cur.Index],
			Cause:   cause,
		})
		if cause == WatermarkCause {
			break
		}
		cur = s.Events[cause].Witness
	}
	return steps, true
}

// Explain renders the reason chain of version v as human-readable text.
func (s *Solution) Explain(v VersionID) string {
	steps, ok := s.ReasonChain(v)
	if !ok {
		return fmt.Sprintf("%s is included in the snapshot", v)
	}
	out := ""
	for i, st := range steps {
		if i > 0 {
			out += "\n"
		}
		if st.Cause == WatermarkCause {
			out += fmt.Sprintf("%s (txn %s) excluded directly by the watermark",
				st.Version, st.Txn)
		} else {
			ev := s.Events[st.Cause]
			out += fmt.Sprintf("%s (txn %s) excluded by rollback #%d: txn %s split, partition %d frontier %d -> %d (witness %s)",
				st.Version, st.Txn, ev.ID, ev.Txn, ev.Partition,
				ev.FromFrontier, ev.ToFrontier, ev.Witness)
		}
	}
	return out
}
