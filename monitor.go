package snaprepl

import "fmt"

// Rollback records one discarded staged snapshot.
type Rollback struct {
	Node      int
	SnapIndex int
}

// Monitor observes every apply/publish/rollback in the cluster and
// checks the safety invariants:
//
//  1. each replica applies log entries in strictly increasing index
//     order (no duplicate or out-of-order apply, even when log entries
//     and snapshot chunks interleave);
//  2. no replica applies a log entry already covered by a snapshot it
//     installed;
//  3. state machine safety: any two replicas that apply the same log
//     index apply the identical command.
type Monitor struct {
	applies   map[int][]Entry
	published map[int][]int
	Rollbacks []Rollback
	safetyErr error
}

func NewMonitor() *Monitor {
	return &Monitor{applies: map[int][]Entry{}, published: map[int][]int{}}
}

func (m *Monitor) RecordApply(node int, e Entry) {
	if m.safetyErr == nil {
		if apps := m.applies[node]; len(apps) > 0 && e.Index <= apps[len(apps)-1].Index {
			m.safetyErr = fmt.Errorf("node %d applied index %d after %d: duplicate or out-of-order apply",
				node, e.Index, apps[len(apps)-1].Index)
		}
		for _, p := range m.published[node] {
			if e.Index <= p {
				m.safetyErr = fmt.Errorf("node %d applied log entry %d already covered by installed snapshot %d",
					node, e.Index, p)
			}
		}
	}
	m.applies[node] = append(m.applies[node], e)
}

func (m *Monitor) RecordPublish(node, index int) {
	m.published[node] = append(m.published[node], index)
}

func (m *Monitor) RecordRollback(node, index int) {
	m.Rollbacks = append(m.Rollbacks, Rollback{Node: node, SnapIndex: index})
}

// CheckSafety returns nil iff every observed run satisfied the invariants.
func (m *Monitor) CheckSafety() error {
	if m.safetyErr != nil {
		return m.safetyErr
	}
	byIndex := map[int]Entry{}
	owner := map[int]int{}
	for node, apps := range m.applies {
		for _, e := range apps {
			if prev, ok := byIndex[e.Index]; ok {
				if prev.Cmd != e.Cmd || prev.Term != e.Term {
					return fmt.Errorf("state machine safety violated at index %d: node %d applied %q (term %d), node %d applied %q (term %d)",
						e.Index, owner[e.Index], prev.Cmd, prev.Term, node, e.Cmd, e.Term)
				}
			} else {
				byIndex[e.Index] = e
				owner[e.Index] = node
			}
		}
	}
	return nil
}

// Applies reports how many log entries each node applied.
func (m *Monitor) Applies() map[int]int {
	out := map[int]int{}
	for node, apps := range m.applies {
		out[node] = len(apps)
	}
	return out
}
