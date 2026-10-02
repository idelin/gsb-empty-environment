package leaseaudit

import (
	"fmt"
	"sort"
	"strings"
)

// StateView is the reconstructed system state after an event during
// replay: the lease holder/term plus the arbiter decision-lock state.
type StateView struct {
	Holder    string
	Term      uint64
	Locked    bool  // arbiter decision lock currently held
	LockOwner int64 // arrival Seq holding the lock, -1 when free
}

// TimelineEntry pairs an event with the state it produced.
type TimelineEntry struct {
	Event Event
	State StateView
}

// Replay sorts events into a total causal order (Lamport, then log
// sequence as tie-breaker) and folds them into a timeline of states.
func Replay(events []Event) []TimelineEntry {
	sorted := append([]Event(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Lamport != sorted[j].Lamport {
			return sorted[i].Lamport < sorted[j].Lamport
		}
		return sorted[i].Seq < sorted[j].Seq
	})

	state := StateView{LockOwner: -1}
	timeline := make([]TimelineEntry, 0, len(sorted))
	for _, ev := range sorted {
		switch ev.Kind {
		case EventLockAcquired:
			state.Locked = true
			if len(ev.Causes) > 0 {
				state.LockOwner = ev.Causes[0]
			}
		case EventLockReleased:
			state.Locked = false
			state.LockOwner = -1
		case EventLeaseGranted, EventLeaseRenewed, EventLeaseHandover:
			state.Term = ev.Term
			state.Holder = extractQuoted(ev.Detail)
		case EventLeaseExpired:
			state.Holder = ""
		}
		timeline = append(timeline, TimelineEntry{Event: ev, State: state})
	}
	return timeline
}

// extractQuoted pulls the first "quoted" token out of a detail string;
// for grant/renew/handover events that is the (new) holder.
func extractQuoted(detail string) string {
	start := strings.IndexByte(detail, '"')
	if start < 0 {
		return ""
	}
	rest := detail[start+1:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// CausalChain walks happens-before edges backwards from the event with
// the given sequence number and returns the full evidence chain in
// chronological order (root causes first).
func CausalChain(log *AuditLog, seq int64) []Event {
	var chain []Event
	seen := map[int64]bool{}
	var visit func(s int64)
	visit = func(s int64) {
		if seen[s] {
			return
		}
		seen[s] = true
		ev, ok := log.Get(s)
		if !ok {
			return
		}
		for _, c := range ev.Causes {
			visit(c)
		}
		chain = append(chain, ev)
	}
	visit(seq)
	sort.SliceStable(chain, func(i, j int) bool { return chain[i].Seq < chain[j].Seq })
	return chain
}

// FormatTimeline renders a replayed timeline as a human-readable
// table, suitable for incident reports.
func FormatTimeline(entries []TimelineEntry) string {
	var b strings.Builder
	b.WriteString("SEQ  LAM  EVENT                  NODE      LOCK   HOLDER    TERM  DETAIL\n")
	b.WriteString(strings.Repeat("-", 100) + "\n")
	for _, e := range entries {
		lock := "free"
		if e.State.Locked {
			lock = fmt.Sprintf("held(#%d)", e.State.LockOwner)
		}
		holder := e.State.Holder
		if holder == "" {
			holder = "-"
		}
		fmt.Fprintf(&b, "#%-4d %-4d %-22s %-9s %-8s %-9s %-5d %s\n",
			e.Event.Seq, e.Event.Lamport, e.Event.Kind, e.Event.NodeID,
			lock, holder, e.State.Term, e.Event.Detail)
	}
	return b.String()
}

// FormatCausalChain renders an evidence chain with its edges.
func FormatCausalChain(events []Event) string {
	var b strings.Builder
	for _, ev := range events {
		fmt.Fprintf(&b, "  %s\n", ev)
		if len(ev.Causes) > 0 {
			fmt.Fprintf(&b, "      ^-- caused by: %v\n", ev.Causes)
		}
	}
	return b.String()
}
