package main

import (
	"fmt"
	"io"
)

func printReport(w io.Writer, r *Report) {
	if !r.Feasible {
		fmt.Fprintln(w, "status: INFEASIBLE - contradictory time constraints located")
		fmt.Fprintf(w, "  %s\n", r.Conflict.Summary)
		if len(r.Conflict.Chain) > 0 {
			fmt.Fprintln(w, "  constraint chain:")
			for _, s := range r.Conflict.Chain {
				fmt.Fprintf(w, "    %s\n", s)
			}
		}
		fmt.Fprintln(w, "  hint: reconcile the listed observations; durations are never")
		fmt.Fprintln(w, "        derived by naive subtraction (no negative spans emitted)")
		return
	}
	fmt.Fprintln(w, "status: feasible")
	fmt.Fprintf(w, "makespan range: [%.6g, %.6g]  (uncertainty span %.6g)\n",
		r.Min.Makespan, r.Max.Makespan, r.Max.Makespan-r.Min.Makespan)
	printScenario(w, "shortest realizable explanation", r.Min)
	printScenario(w, "longest realizable explanation", r.Max)
}

func printScenario(w io.Writer, title string, s *Scenario) {
	fmt.Fprintf(w, "\n-- %s: makespan %.6g --\n", title, s.Makespan)
	total := s.Compute + s.Queue
	if total > eps {
		fmt.Fprintf(w, "  attribution: compute %.6g (%.0f%%), queue %.6g (%.0f%%)\n",
			s.Compute, 100*s.Compute/total, s.Queue, 100*s.Queue/total)
	}
	fmt.Fprintf(w, "  verdict: %s\n", s.Verdict)
	fmt.Fprintln(w, "  critical chain:")
	for _, st := range s.Chain {
		fmt.Fprintf(w, "    %s -> %s  [%s] +%.6g  (%s)\n",
			st.From, st.To, st.Kind, st.Delta, st.Note)
	}
	fmt.Fprintln(w, "  witness timeline:")
	for _, et := range s.Timeline {
		fmt.Fprintf(w, "    %-18s @ %.6g\n", et.Event, et.Time)
	}
}
