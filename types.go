// Package decision implements a conflict-free replicated data type (CRDT)
// for weighted multi-party voting. Nodes cast YES/NO/ABSTAIN ballots
// weighted by stake, weights may be adjusted causally over time, and
// replicas merged after network partitions converge to a deterministic
// decision.
package decision

import "fmt"

// Choice is a ballot cast by a node.
type Choice int

const (
	Abstain Choice = iota
	Yes
	No
)

func (c Choice) String() string {
	switch c {
	case Yes:
		return "YES"
	case No:
		return "NO"
	default:
		return "ABSTAIN"
	}
}

// Decision is the deterministic outcome of a tally.
type Decision int

const (
	// Undecided means neither side holds a strict majority of the total
	// effective weight.
	Undecided Decision = iota
	// Approved means YES weight is a strict majority of total weight.
	Approved
	// Rejected means NO weight is a strict majority of total weight.
	Rejected
)

func (d Decision) String() string {
	switch d {
	case Approved:
		return "APPROVED"
	case Rejected:
		return "REJECTED"
	default:
		return "UNDECIDED"
	}
}

// Vote is a last-writer-wins register entry holding one node's ballot.
// Time is the voter's Lamport timestamp; Voter breaks ties.
type Vote struct {
	Choice Choice
	Time   uint64
	Voter  string
}

// Weight is a last-writer-wins register entry holding one node's stake.
// Time is the Lamport timestamp of the update; Source breaks ties.
type Weight struct {
	Value  uint64
	Time   uint64
	Source string
}

// lessVote orders votes causally: newer wins, ties break deterministically.
func lessVote(a, b Vote) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	if a.Voter != b.Voter {
		return a.Voter < b.Voter
	}
	return a.Choice < b.Choice
}

// lessWeight orders weight updates causally: newer wins, ties break
// deterministically by source then value.
func lessWeight(a, b Weight) bool {
	if a.Time != b.Time {
		return a.Time < b.Time
	}
	if a.Source != b.Source {
		return a.Source < b.Source
	}
	return a.Value < b.Value
}

// Tally is the aggregated, deterministic view of a merged state.
type Tally struct {
	TotalWeight   uint64   // effective weight of every known node
	VotedWeight   uint64   // weight of nodes that cast a ballot
	YesWeight     uint64   // weight voting YES
	NoWeight      uint64   // weight voting NO
	AbstainWeight uint64   // weight explicitly abstaining
	YesCount      int      // number of YES ballots
	NoCount       int      // number of NO ballots
	AbstainCount  int      // number of ABSTAIN ballots
	MissingVoters []string // weighted nodes that have not voted (sorted)
}

// Decision maps a tally to an outcome by strict majority of the total
// effective weight. Abstentions raise the bar for both sides because they
// count toward TotalWeight but toward neither side.
func (t Tally) Decision() Decision {
	switch {
	case t.TotalWeight == 0:
		return Undecided
	case t.YesWeight*2 > t.TotalWeight:
		return Approved
	case t.NoWeight*2 > t.TotalWeight:
		return Rejected
	default:
		return Undecided
	}
}

func (t Tally) String() string {
	return fmt.Sprintf("total=%d voted=%d yes=%d(%d) no=%d(%d) abstain=%d(%d) missing=%v => %s",
		t.TotalWeight, t.VotedWeight,
		t.YesWeight, t.YesCount,
		t.NoWeight, t.NoCount,
		t.AbstainWeight, t.AbstainCount,
		t.MissingVoters, t.Decision())
}
