// Package checker verifies backup cut consistency across partitions.
//
// Every record carries a commit version. Cross-partition transactions carry
// a participant list. A backup cut is a per-partition version frontier; it
// is consistent only if every cross-partition transaction is either fully
// included (commit marker within the cut at every participant) or fully
// excluded. Taking the latest version of each partition independently is
// not allowed.
package checker

import (
	"fmt"
	"sort"
	"strings"
)

type Version int64

type Outcome int

const (
	OutcomePending Outcome = iota
	OutcomeCommitted
	OutcomeAborted
)

func (o Outcome) String() string {
	switch o {
	case OutcomeCommitted:
		return "committed"
	case OutcomeAborted:
		return "aborted"
	default:
		return "pending"
	}
}

type EntryKind int

const (
	EntryData EntryKind = iota
	EntryCommit
	EntryAbort
)

type Entry struct {
	Kind    EntryKind
	TxnID   string
	Key     string
	Value   string
	Version Version
}

type Partition struct {
	Name    string
	Entries []Entry
}

func (p *Partition) sort() {
	sort.SliceStable(p.Entries, func(i, j int) bool {
		return p.Entries[i].Version < p.Entries[j].Version
	})
}

func (p *Partition) LatestVersion() Version {
	var v Version
	for _, e := range p.Entries {
		if e.Version > v {
			v = e.Version
		}
	}
	return v
}

// Log is the full multi-partition backup source: partition entry logs plus
// the participant list of every transaction.
type Log struct {
	Partitions map[string]*Partition
	Order      []string
	Txns       map[string][]string
	TxnOrder   []string
}

func NewLog() *Log {
	return &Log{
		Partitions: make(map[string]*Partition),
		Txns:       make(map[string][]string),
	}
}

func (l *Log) AddPartition(name string) {
	if _, ok := l.Partitions[name]; ok {
		return
	}
	l.Partitions[name] = &Partition{Name: name}
	l.Order = append(l.Order, name)
}

func (l *Log) AddTxn(id string, participants ...string) {
	if _, ok := l.Txns[id]; ok {
		return
	}
	cp := append([]string(nil), participants...)
	l.Txns[id] = cp
	l.TxnOrder = append(l.TxnOrder, id)
}

func (l *Log) AddEntry(partition string, e Entry) {
	p := l.Partitions[partition]
	p.Entries = append(p.Entries, e)
}

// Sort orders every partition log by version. Call once after loading.
func (l *Log) Sort() {
	for _, p := range l.Partitions {
		p.sort()
	}
}

// Validate checks structural sanity of the log.
func (l *Log) Validate() error {
	for txnID, parts := range l.Txns {
		seen := make(map[string]bool)
		for _, pt := range parts {
			if _, ok := l.Partitions[pt]; !ok {
				return fmt.Errorf("txn %s: unknown participant partition %q", txnID, pt)
			}
			if seen[pt] {
				return fmt.Errorf("txn %s: duplicate participant %q", txnID, pt)
			}
			seen[pt] = true
		}
	}
	for _, pname := range l.Order {
		p := l.Partitions[pname]
		decided := make(map[string]bool)
		for _, e := range p.Entries {
			parts, known := l.Txns[e.TxnID]
			if !known {
				return fmt.Errorf("partition %s: entry for unknown txn %q", pname, e.TxnID)
			}
			if !contains(parts, pname) {
				return fmt.Errorf("partition %s: entry for txn %s which does not list it as participant", pname, e.TxnID)
			}
			if e.Kind == EntryCommit || e.Kind == EntryAbort {
				if decided[e.TxnID] {
					return fmt.Errorf("partition %s: txn %s has conflicting commit/abort markers", pname, e.TxnID)
				}
				decided[e.TxnID] = true
			}
		}
	}
	return nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// Cut is a per-partition version frontier: entries with version <= cut[p]
// at partition p are inside the backup.
type Cut map[string]Version

func (c Cut) Copy() Cut {
	out := make(Cut, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

func (c Cut) String() string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s<=v%d", k, c[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// ParticipantEvidence describes one participant's state relative to a cut.
type ParticipantEvidence struct {
	Partition     string
	Outcome       Outcome
	CommitVersion Version
	DataVersions  []Version
	Included      bool
	Reason        string
}

// Violation is a cross-partition transaction that is neither fully included
// nor fully excluded at a cut.
type Violation struct {
	TxnID        string
	Participants []ParticipantEvidence
}

func (l *Log) participantStates(txnID string, cut Cut) []ParticipantEvidence {
	parts := l.Txns[txnID]
	states := make([]ParticipantEvidence, 0, len(parts))
	for _, pname := range parts {
		st := ParticipantEvidence{Partition: pname, Outcome: OutcomePending}
		for _, e := range l.Partitions[pname].Entries {
			if e.TxnID != txnID {
				continue
			}
			switch e.Kind {
			case EntryData:
				st.DataVersions = append(st.DataVersions, e.Version)
			case EntryCommit:
				st.Outcome = OutcomeCommitted
				st.CommitVersion = e.Version
			case EntryAbort:
				st.Outcome = OutcomeAborted
				st.CommitVersion = e.Version
			}
		}
		st.Included = st.Outcome == OutcomeCommitted && st.CommitVersion <= cut[pname]
		st.Reason = reason(st, cut[pname])
		states = append(states, st)
	}
	return states
}

func reason(st ParticipantEvidence, frontier Version) string {
	switch st.Outcome {
	case OutcomeCommitted:
		if st.CommitVersion <= frontier {
			return fmt.Sprintf("commit marker v%d within cut", st.CommitVersion)
		}
		return fmt.Sprintf("late commit marker v%d beyond cut v%d", st.CommitVersion, frontier)
	case OutcomeAborted:
		return fmt.Sprintf("aborted at v%d", st.CommitVersion)
	default:
		if len(st.DataVersions) > 0 {
			return fmt.Sprintf("pending: data received (v%v), commit undecided", st.DataVersions)
		}
		return "pending: no commit marker"
	}
}

// ValidateCut returns one Violation per cross-partition transaction that is
// partially included at the cut. An empty result means the cut is a
// recoverable point.
func (l *Log) ValidateCut(cut Cut) []Violation {
	var out []Violation
	for _, txnID := range l.TxnOrder {
		parts := l.Txns[txnID]
		if len(parts) < 2 {
			continue
		}
		states := l.participantStates(txnID, cut)
		included, excluded := 0, 0
		for _, st := range states {
			if st.Included {
				included++
			} else {
				excluded++
			}
		}
		if included > 0 && excluded > 0 {
			out = append(out, Violation{TxnID: txnID, Participants: states})
		}
	}
	return out
}

// Lowering records one cut-lowering step: the transaction that could not be
// fully included, the participants responsible ("missing"), and the
// participants whose frontier had to retreat below their commit version.
type Lowering struct {
	TxnID   string
	Missing []ParticipantEvidence
	Lowered []ParticipantEvidence
}

// FindRecoverableCut computes the greatest consistent cut not exceeding
// target, by repeatedly excluding cross-partition transactions that are
// only partially included. Each exclusion is returned as evidence.
func (l *Log) FindRecoverableCut(target Cut) (Cut, []Lowering) {
	cut := target.Copy()
	for _, name := range l.Order {
		if _, ok := cut[name]; !ok {
			cut[name] = l.Partitions[name].LatestVersion()
		}
	}
	var lowerings []Lowering
	for {
		changed := false
		for _, txnID := range l.TxnOrder {
			parts := l.Txns[txnID]
			if len(parts) < 2 {
				continue
			}
			states := l.participantStates(txnID, cut)
			allIncluded := true
			for _, st := range states {
				if !st.Included {
					allIncluded = false
				}
			}
			if allIncluded {
				continue
			}
			var missing, lowered []ParticipantEvidence
			for _, st := range states {
				if st.Outcome == OutcomeCommitted && st.CommitVersion <= cut[st.Partition] {
					cut[st.Partition] = st.CommitVersion - 1
					st.Included = false
					st.Reason = fmt.Sprintf("frontier lowered below commit v%d to exclude txn %s", st.CommitVersion, txnID)
					lowered = append(lowered, st)
					changed = true
				} else {
					missing = append(missing, st)
				}
			}
			if len(lowered) > 0 {
				lowerings = append(lowerings, Lowering{TxnID: txnID, Missing: missing, Lowered: lowered})
			}
		}
		if !changed {
			return cut, lowerings
		}
	}
}
