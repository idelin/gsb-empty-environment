package main

import (
	"fmt"
	"io"
	"strings"
)

func dirName(d int) string {
	if d == DirFwd {
		return "fwd"
	}
	return "rev"
}

func writeScenarioReport(w io.Writer, n *Network, rep ScenarioReport) {
	title := "baseline (no failures)"
	if rep.ScenarioID != "" {
		title = fmt.Sprintf("scenario %s: failed links [%s]", rep.ScenarioID, strings.Join(rep.FailedLinks, ", "))
	}
	fmt.Fprintf(w, "\n== %s ==\n", title)

	if len(rep.Unreachable) > 0 {
		fmt.Fprintf(w, "  UNREACHABLE flows (route broken, counted as FAILED, not 0-load OK): %s\n",
			strings.Join(rep.Unreachable, ", "))
	}
	for _, v := range rep.Verdicts {
		if v.Mode == ModeShared {
			status := "ok"
			if v.Overloaded {
				status = "OVERLOAD"
			}
			fmt.Fprintf(w, "  link %-6s shared   cap=%6.1f  load=%6.1f (fwd=%.1f rev=%.1f)  util=%5.1f%%  %s\n",
				v.LinkID, v.Capacity, v.CombinedLoad, v.Dirs[0].Load, v.Dirs[1].Load,
				100*v.CombinedLoad/v.Capacity, status)
		} else {
			for d := 0; d < 2; d++ {
				l := v.Dirs[d]
				if len(l.FlowIDs) == 0 {
					continue
				}
				status := "ok"
				if l.Load > v.Capacity {
					status = "OVERLOAD"
				}
				fmt.Fprintf(w, "  link %-6s %-3s     cap=%6.1f  load=%6.1f  util=%5.1f%%  %s  flows=%v\n",
					v.LinkID, dirName(d), v.Capacity, l.Load, 100*l.Load/v.Capacity, status, l.FlowIDs)
			}
		}
		if v.Overloaded {
			fmt.Fprintf(w, "    minimal overload flow sets (removing any one flow restores capacity):\n")
			for _, s := range v.MinimalSets {
				fmt.Fprintf(w, "      %s\n", formatSet(s))
			}
			if v.Truncated {
				fmt.Fprintf(w, "      (enumeration truncated: too many flows/sets; showing greedy subset)\n")
			}
		}
	}
	if !rep.AnyOverload && len(rep.Unreachable) == 0 {
		fmt.Fprintf(w, "  all link loads within capacity\n")
	} else if !rep.AnyOverload {
		fmt.Fprintf(w, "  remaining link loads within capacity\n")
	}
}

func writeRedundancy(w io.Writer, reds []FlowRedundancy) {
	fmt.Fprintf(w, "\n== redundancy check (link-disjoint backup path exists) ==\n")
	for _, r := range reds {
		if r.Redundant {
			fmt.Fprintf(w, "  flow %-6s REDUNDANT   alt path: %s\n", r.FlowID, strings.Join(r.AltPath, " -> "))
		} else {
			fmt.Fprintf(w, "  flow %-6s NOT redundant (no link-disjoint alternate route)\n", r.FlowID)
		}
	}
}

func writeSimResult(w io.Writer, res SimResult) {
	title := "baseline"
	if res.ScenarioID != "" {
		title = "scenario " + res.ScenarioID
	}
	fmt.Fprintf(w, "\n== discrete-event simulation: %s (%d ticks) ==\n", title, res.Ticks)
	if len(res.Unreachable) > 0 {
		fmt.Fprintf(w, "  UNREACHABLE flows (excluded, counted as FAILED): %s\n", strings.Join(res.Unreachable, ", "))
	}
	for _, f := range res.Flows {
		if f.Unreachable {
			fmt.Fprintf(w, "  flow %-6s UNREACHABLE\n", f.FlowID)
			continue
		}
		status := "ok"
		if !f.OK {
			status = "STARVED (backlog grows)"
		}
		fmt.Fprintf(w, "  flow %-6s generated=%-6d delivered=%-6d backlog=%-6d %s\n",
			f.FlowID, f.Generated, f.Delivered, f.Backlog, status)
	}
	for _, l := range res.Links {
		note := ""
		if l.Congested {
			note = "  CONGESTED (queue still growing at end)"
		}
		fmt.Fprintf(w, "  link %-6s %-3s  maxQ=%-6d endQ=%-6d served=%-6d%s\n",
			l.LinkID, dirName(l.Dir), l.MaxQueue, l.EndQueue, l.Served, note)
	}
}
