package fxacct

import (
	"fmt"
	"strings"
)

// Report renders a human-auditable trace: every intermediate value
// as an exact fraction, each rounding node's gain/loss, and the
// per-item vs aggregate difference.
func (c *Comparison) Report() string {
	var sb strings.Builder
	fmt.Fprintln(&sb, "== Per-item conversion ==")
	writeEvents(&sb, c.ItemEvents)
	fmt.Fprintf(&sb, "Per-item total: %s %s\n", FormatValue(c.PerItemTotal), c.Currency)
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "== Aggregate conversion (exact sum, rounded once) ==")
	writeEvents(&sb, c.AggregateEvents)
	fmt.Fprintf(&sb, "Aggregate total: %s %s\n", FormatValue(c.AggregateTotal), c.Currency)
	fmt.Fprintln(&sb)
	fmt.Fprintln(&sb, "== Rounding gain/loss per node ==")
	for _, pl := range c.StepPLs {
		fmt.Fprintf(&sb, "step %d %s [%s]: per-item sum of deltas = %s, aggregate delta = %s\n",
			pl.StepIdx, pl.StepName, pl.Currency,
			FormatValue(pl.PerItemDelta), FormatValue(pl.AggregateDelta))
	}
	fmt.Fprintf(&sb, "Difference (per-item - aggregate): %s %s\n",
		FormatValue(c.Difference), c.Currency)
	return sb.String()
}

func writeEvents(sb *strings.Builder, events []Event) {
	lastItem := ""
	for _, ev := range events {
		if ev.ItemID != lastItem {
			fmt.Fprintf(sb, "item %s:\n", ev.ItemID)
			lastItem = ev.ItemID
		}
		fmt.Fprintf(sb, "  step %d %s [%s]: %s -> %s %s",
			ev.StepIdx, ev.StepName, ev.Kind,
			FormatValue(ev.In), FormatValue(ev.Out), ev.Currency)
		if ev.Delta != nil {
			fmt.Fprintf(sb, "  (rounding delta %s)", FormatValue(ev.Delta))
		}
		fmt.Fprintln(sb)
	}
}
