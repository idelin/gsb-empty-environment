package repairplan

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// RenderText writes a deterministic, human-readable summary of the plan:
// stripe verdicts first, then the round-by-round repair timeline.
func RenderText(w io.Writer, plan *Plan) error {
	var b strings.Builder

	fmt.Fprintf(&b, "Repair plan: %d round(s)\n", plan.TotalRounds)
	fmt.Fprintln(&b, "Stripe verdicts:")
	for _, r := range plan.StripeReports {
		switch r.Status {
		case StatusHealthy:
			fmt.Fprintf(&b, "  - %s HEALTHY (no repair needed)\n", r.StripeID)
		case StatusUnrecoverable:
			fmt.Fprintf(&b, "  - %s UNRECOVERABLE surviving=%d (< k), not scheduled\n",
				r.StripeID, r.Surviving)
		default:
			if !r.Scheduled {
				fmt.Fprintf(&b, "  - %s DEGRADED margin=%d surviving=%d BLOCKED:\n",
					r.StripeID, r.Margin, r.Surviving)
				for _, f := range r.Failures {
					fmt.Fprintf(&b, "      shard %d %s on %s: %s\n",
						f.ShardIndex, f.Reason, f.FaultDomain, f.Detail)
				}
			} else {
				fmt.Fprintf(&b, "  - %s DEGRADED margin=%d surviving=%d missing=%v completed=round%d at=%s\n",
					r.StripeID, r.Margin, r.Surviving, r.MissingShards,
					r.CompletedRound, r.CompletedAt)
			}
		}
	}
	if len(plan.Unrecoverable) > 0 {
		fmt.Fprintln(&b, "Unrecoverable stripes:")
		for _, c := range plan.Unrecoverable {
			fmt.Fprintf(&b, "  - %s surviving=%d k=%d missing=%v\n",
				c.StripeID, c.Surviving, c.K, c.Missing)
		}
	}

	for _, round := range plan.Rounds {
		fmt.Fprintf(&b, "Round %d [%s -> %s] tasks=%d:\n",
			round.Round, round.Start, round.End, len(round.Tasks))
		for _, task := range round.Tasks {
			fmt.Fprintf(&b, "  * stripe=%s shard=%d target=%s sources=%v crossRack=%v bytes=%d\n",
				task.StripeID, task.ShardIndex, task.TargetOSD,
				task.SourceOSDs, task.CrossRackSources, task.Bytes)
		}
		ids := make([]string, 0, len(round.NodeUsage))
		for id := range round.NodeUsage {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			u := round.NodeUsage[id]
			if u.IngressBytes == 0 && u.EgressBytes == 0 && u.ConcurrentTasks == 0 {
				continue
			}
			fmt.Fprintf(&b, "    node=%s in=%d out=%d tasks=%d\n",
				id, u.IngressBytes, u.EgressBytes, u.ConcurrentTasks)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}
