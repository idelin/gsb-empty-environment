package sm

import "sort"

// Snapshot is a consistent slice point of the state machine: all log
// events with LSN < ExpectLSN are reflected in it. Recovery restores the
// snapshot and replays the log suffix from ExpectLSN onward.
type Snapshot struct {
	ExpectLSN  uint64              `json:"expect_lsn"`
	Partitions []map[string]string `json:"partitions"`
	Pending    []PendingTxn        `json:"pending,omitempty"`
	Evidence   []Evidence          `json:"evidence,omitempty"`
}

// PendingTxn is the serializable form of an in-flight transaction, so a
// snapshot taken mid-two-phase-commit can be resumed exactly.
type PendingTxn struct {
	ID         string            `json:"id"`
	State      string            `json:"state"` // "prepared" | "committed"
	Writes     map[string]string `json:"writes,omitempty"`
	Reads      []string          `json:"reads,omitempty"`
	PrepareLSN uint64            `json:"prepare_lsn"`
	CommitLSN  uint64            `json:"commit_lsn,omitempty"`
}

// Snapshot captures a consistent slice point. Deep-copied and
// deterministically ordered, so snapshots of identical states are equal.
func (s *StateMachine) Snapshot() Snapshot {
	snap := Snapshot{
		ExpectLSN:  s.expectLSN,
		Partitions: make([]map[string]string, len(s.parts)),
		Evidence:   s.Evidence(),
	}
	for i, part := range s.parts {
		cp := make(map[string]string, len(part))
		for k, v := range part {
			cp[k] = v
		}
		snap.Partitions[i] = cp
	}
	ids := make([]string, 0, len(s.pending))
	for id := range s.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		t := s.pending[id]
		pt := PendingTxn{
			ID:         t.id,
			Writes:     t.writes,
			Reads:      t.reads,
			PrepareLSN: t.prepareLSN,
			CommitLSN:  t.commitLSN,
		}
		if t.state == txnCommitted {
			pt.State = "committed"
		} else {
			pt.State = "prepared"
		}
		snap.Pending = append(snap.Pending, pt)
	}
	return snap
}

// Restore rebuilds a state machine from a snapshot. The returned machine
// expects the log to continue at the snapshot's slice point; replay the
// log suffix with Replay/Deliver to recover the pre-crash state.
func Restore(snap Snapshot, cfg Config) *StateMachine {
	if cfg.NumPartitions <= 0 {
		cfg.NumPartitions = len(snap.Partitions)
	}
	s := New(cfg)
	s.expectLSN = snap.ExpectLSN
	s.parts = make([]map[string]string, len(snap.Partitions))
	for i, part := range snap.Partitions {
		cp := make(map[string]string, len(part))
		for k, v := range part {
			cp[k] = v
		}
		s.parts[i] = cp
	}
	s.evidence = append(s.evidence, snap.Evidence...)
	for _, pt := range snap.Pending {
		t := &txn{
			id:         pt.ID,
			writes:     pt.Writes,
			reads:      pt.Reads,
			prepareLSN: pt.PrepareLSN,
			commitLSN:  pt.CommitLSN,
			state:      txnPrepared,
		}
		if pt.State == "committed" {
			t.state = txnCommitted
		}
		s.pending[t.id] = t
	}
	return s
}
