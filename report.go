package main

import (
	"fmt"
	"io"
	"strings"
)

// runTrace drives the state machine and renders a human-readable report.
func runTrace(out io.Writer, events []Event, strict bool) error {
	state := initialState()

	fmt.Fprintln(out, "# capabilities(7) exec transition replay (non-root thread)")
	fmt.Fprintf(out, "step  start: %s\n", snapshotLine(state))

	var rejected int
	for i := range events {
		ev := events[i]
		before := *state

		var err error
		label := describeEvent(ev)
		switch ev.Kind {
		case "init":
			err = state.applyInit(ev)
		case "exec":
			err = state.applyExec(ev)
		case "capset":
			err = state.applyCapset(ev)
		case "ambient":
			err = state.applyAmbient(ev)
		case "bset":
			err = state.applyBounding(ev)
		}

		fmt.Fprintln(out)
		fmt.Fprintf(out, "line %-3d %s\n", ev.Line, label)
		if err != nil {
			rejected++
			fmt.Fprintf(out, "       REJECTED: %s\n", strings.TrimPrefix(err.Error(), fmt.Sprintf("line %d ", ev.Line)))
			if strict {
				return err
			}
			continue
		}
		printSetChanges(out, &before, state)
		fmt.Fprintf(out, "       %s\n", snapshotLine(state))

		newlyEffective := before.Effective ^ state.Effective
		newlyEffective &= state.Effective
		for _, bit := range newlyEffective.bits() {
			if e, seen := state.firstEffective[bit]; seen && e.Step != ev.Line {
				continue
			}
			e, ok := state.firstEffective[bit]
			if !ok {
				continue
			}
			fmt.Fprintf(out, "       FIRST-EFFECTIVE %s: %s\n", capName(bit), e.Detail)
		}
	}

	printEvidence(out, state)
	if rejected != 0 {
		fmt.Fprintf(out, "\n%d operation(s) rejected by the kernel permission rules; state was left unchanged at each.\n", rejected)
	}
	return nil
}

func snapshotLine(s *State) string {
	return fmt.Sprintf("P=%s | I=%s | E=%s | A=%s | B=%s",
		renderSet(s.Permitted), renderSet(s.Inheritable), renderSet(s.Effective),
		renderSet(s.Ambient), renderSet(s.Bounding))
}

func describeEvent(ev Event) string {
	switch ev.Kind {
	case "init":
		return "init declared starting sets"
	case "exec":
		priv := "unprivileged file"
		switch {
		case ev.Nosuid:
			priv = "nosuid mount: file caps/suid ignored, treated as unprivileged"
		case ev.isPrivileged():
			priv = "PRIVILEGED file"
		}
		return fmt.Sprintf("exec %s [%s]", ev.Target, priv)
	case "capset":
		return "capset replace P,I,E"
	case "ambient":
		return fmt.Sprintf("prctl PR_CAP_AMBIENT %s %s", ev.Mode, renderSet(ev.Caps))
	case "bset":
		return fmt.Sprintf("prctl PR_CAPBSET_DROP %s", renderSet(ev.Caps))
	}
	return ev.Kind
}

func printSetChanges(out io.Writer, before, after *State) {
	rows := []struct {
		name string
		b, a CapMask
	}{
		{"P", before.Permitted, after.Permitted},
		{"I", before.Inheritable, after.Inheritable},
		{"E", before.Effective, after.Effective},
		{"A", before.Ambient, after.Ambient},
		{"B", before.Bounding, after.Bounding},
	}
	for _, r := range rows {
		added, removed := r.a&^r.b, r.b&^r.a
		if added == 0 && removed == 0 {
			continue
		}
		var parts []string
		if added != 0 {
			parts = append(parts, "+"+renderSet(added))
		}
		if removed != 0 {
			parts = append(parts, "-"+renderSet(removed))
		}
		fmt.Fprintf(out, "       %s change: %s\n", r.name, strings.Join(parts, " "))
	}
}

func printEvidence(out io.Writer, s *State) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "# first time each capability became effective")
	if len(s.firstEffective) == 0 {
		fmt.Fprintln(out, "(no capability ever entered the effective set)")
		return
	}
	for b := uint(0); b <= CapLastCap; b++ {
		e, ok := s.firstEffective[b]
		if !ok {
			continue
		}
		fmt.Fprintf(out, "%-26s line %-3d via %-32s %s\n", capName(b), e.Step, e.Op, e.Detail)
	}
}
