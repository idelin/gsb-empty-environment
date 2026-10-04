package main

import "fmt"

// Entry is a single log record tagged with its term and 1-based index.
type Entry struct {
	Term  int
	Index int
}

// AppendRequest mirrors Raft's AppendEntries RPC.
type AppendRequest struct {
	LeaderTerm   int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []Entry
	CommitIndex  int
}

// Replica is a follower log with a commit watermark.
type Replica struct {
	currentTerm int
	commitIndex int
	log         []Entry
	events      []string
}

func (r *Replica) lastIndex() int { return len(r.log) }

func (r *Replica) truncate(from int) {
	removed := len(r.log) - (from - 1)
	r.events = append(r.events, fmt.Sprintf(
		"TRUNCATE from index %d (removed %d entries: [%d..%d])",
		from, removed, from, len(r.log)))
	r.log = r.log[:from-1]
}

// HandleAppend applies one AppendRequest, returning whether it was accepted.
// Idempotent for duplicate retries, rejects stale (late) requests, rolls
// back a whole conflicting term segment in one step, and never truncates
// committed entries.
func (r *Replica) HandleAppend(req AppendRequest) bool {
	if req.LeaderTerm < r.currentTerm {
		r.events = append(r.events, fmt.Sprintf(
			"REJECT stale request: leader term %d < current term %d",
			req.LeaderTerm, r.currentTerm))
		return false
	}
	r.currentTerm = req.LeaderTerm

	if req.PrevLogIndex > 0 {
		if req.PrevLogIndex > r.lastIndex() {
			r.events = append(r.events, fmt.Sprintf(
				"REJECT prevLogIndex %d beyond log end %d",
				req.PrevLogIndex, r.lastIndex()))
			return false
		}
		if prevTerm := r.log[req.PrevLogIndex-1].Term; prevTerm != req.PrevLogTerm {
			// Fast rollback: jump to the first index of the conflicting
			// term and drop the whole segment in one step.
			first := req.PrevLogIndex
			for first > 1 && r.log[first-2].Term == prevTerm {
				first--
			}
			if first <= r.commitIndex {
				r.events = append(r.events, fmt.Sprintf(
					"REJECT conflict in term %d reaches committed prefix (commitIndex=%d)",
					prevTerm, r.commitIndex))
				return false
			}
			r.truncate(first)
			r.events = append(r.events, "REJECT after rollback: leader must retry with earlier prevLogIndex")
			return false
		}
	}

	// Locate the first entry not already present (duplicate retries skip ahead).
	i := 0
	for i < len(req.Entries) {
		e := req.Entries[i]
		pos := e.Index - 1
		if pos < len(r.log) {
			if r.log[pos].Term == e.Term {
				i++
				continue
			}
			if pos < r.commitIndex {
				r.events = append(r.events, fmt.Sprintf(
					"REJECT new entry at index %d conflicts with committed entry",
					e.Index))
				return false
			}
			r.truncate(e.Index)
		}
		if e.Index > len(r.log)+1 {
			r.events = append(r.events, fmt.Sprintf(
				"REJECT entry at index %d would leave a gap (log ends at %d)",
				e.Index, len(r.log)))
			return false
		}
		break
	}
	if i < len(req.Entries) {
		from := req.Entries[i].Index
		to := req.Entries[len(req.Entries)-1].Index
		r.log = append(r.log, req.Entries[i:]...)
		r.events = append(r.events, fmt.Sprintf(
			"APPEND [%d..%d] (%d entries, term %d)",
			from, to, to-from+1, req.Entries[i].Term))
	}

	if req.CommitIndex > r.commitIndex {
		r.commitIndex = min(req.CommitIndex, r.lastIndex())
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func formatLog(log []Entry) string {
	s := "["
	for i, e := range log {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d@t%d", e.Index, e.Term)
	}
	return s + "]"
}

// prefixEqual reports whether log starts with the expected entries.
func prefixEqual(log, expected []Entry) bool {
	if len(log) < len(expected) {
		return false
	}
	for i, e := range expected {
		if log[i] != e {
			return false
		}
	}
	return true
}
