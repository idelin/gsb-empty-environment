package main

import (
	"fmt"
	"strings"
)

func stateInline(m *model, sm map[string]string) string {
	parts := make([]string, 0, len(m.fieldNames))
	for _, f := range m.fieldNames {
		parts = append(parts, f+"="+sm[f])
	}
	return strings.Join(parts, ", ")
}

func renderText(m *model, rep *report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Analyzed %d states and %d rules (exhaustive state-space exploration).\n\n", rep.States, rep.Rules)

	if rep.Terminates {
		b.WriteString("Termination: OK - every input reaches a state where no rule matches.\n")
	} else {
		b.WriteString("Termination: VIOLATED - a reachable execution loop exists.\n")
		w := rep.Loop
		fmt.Fprintf(&b, "  Minimal input: %s\n", stateInline(m, w.Input))
		fmt.Fprintf(&b, "  Loop of %d rule application(s):\n", len(w.Steps))
		fmt.Fprintf(&b, "    start: %s\n", stateInline(m, w.Input))
		for i, st := range w.Steps {
			fmt.Fprintf(&b, "    %d. [%s] -> %s\n", i+1, st.Rule, stateInline(m, st.State))
			if st.Note != "" {
				fmt.Fprintf(&b, "       (%s)\n", st.Note)
			}
		}
	}

	if rep.Unique {
		b.WriteString("Final queue: UNIQUE - every input ends in exactly one possible final queue.\n")
	} else {
		b.WriteString("Final queue: NOT UNIQUE - different legal rule choices lead to different final queues.\n")
		w := rep.Divergence
		fmt.Fprintf(&b, "  Minimal input: %s\n", stateInline(m, w.Input))
		renderChoice := func(label, queue string, path []pathStep) {
			fmt.Fprintf(&b, "  Choice %s (%d step(s), final queue %q):\n", label, len(path), queue)
			for i, st := range path {
				fmt.Fprintf(&b, "    %d. [%s] -> %s\n", i+1, st.Rule, stateInline(m, st.State))
			}
		}
		renderChoice("A", w.QueueA, w.PathA)
		renderChoice("B", w.QueueB, w.PathB)
	}

	b.WriteString("\n")
	if rep.Terminates && rep.Unique {
		b.WriteString("Verdict: SAFE - all inputs terminate and the final queue is unique.\n")
	} else {
		b.WriteString("Verdict: UNSAFE\n")
	}
	return b.String()
}
