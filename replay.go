package leaseaudit

import (
	"fmt"
	"sort"
	"strings"
)

// Replay 按时间轴（Lamport 时钟，其次 Seq）回放指定租约的完整事件流水。
func Replay(t *Tracker, leaseID string) []Event {
	events := t.EventsFor(leaseID)
	sort.Slice(events, func(i, j int) bool {
		if events[i].Lamport != events[j].Lamport {
			return events[i].Lamport < events[j].Lamport
		}
		return events[i].Seq < events[j].Seq
	})
	return events
}

// Render 将事件流水渲染为对齐的审计表格，用于回放与排查。
func Render(events []Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-4s %-7s %-26s %-8s %-16s %-19s %-10s %s\n",
		"SEQ", "LAMPORT", "VECTOR", "NODE", "TYPE", "TRANSITION", "CAUSED-BY", "DETAIL")
	for _, e := range events {
		transition := ""
		if e.From != "" || e.To != "" {
			transition = fmt.Sprintf("%s->%s", e.From, e.To)
		}
		causes := make([]string, 0, len(e.CausedBy))
		for _, c := range e.CausedBy {
			causes = append(causes, fmt.Sprintf("#%d", c))
		}
		fmt.Fprintf(&b, "%-4d %-7d %-26s %-8s %-16s %-19s %-10s %s\n",
			e.Seq, e.Lamport, e.Vector.String(), e.NodeID, e.Type,
			transition, strings.Join(causes, ","), e.Detail)
	}
	return b.String()
}
