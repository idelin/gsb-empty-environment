package model

import (
	"fmt"
	"strings"
)

// RenderOp renders one event in a stable, compact form for witnesses.
func RenderOp(op Op) string {
	label := ""
	if op.Label != "" {
		label = " # " + op.Label
	}
	base := "T" + itoa(op.Thread) + " " + op.Kind.String()
	switch op.Kind {
	case OpSetRoot:
		return fmt.Sprintf("%s(root %s <= %s)%s", base, op.Root, dash(op.Addr), label)
	case OpFlushRoot:
		return base + label
	case OpLoadRoot:
		return fmt.Sprintf("%s(root %s -> reg %s)%s", base, op.Root, op.Reg, label)
	case OpProtect:
		return fmt.Sprintf("%s(slot %d -> %s)%s", base, op.Slot, op.Addr, label)
	case OpFlushHP:
		return base + label
	case OpClearHP:
		return fmt.Sprintf("%s(slot %d)%s", base, op.Slot, label)
	case OpVerifyRoot:
		return fmt.Sprintf("%s(root %s == reg %s)%s", base, op.Root, op.Reg, label)
	case OpDeref:
		return fmt.Sprintf("%s(reg %s)%s", base, op.Reg, label)
	case OpRetire:
		return fmt.Sprintf("%s(%s)%s", base, op.Addr, label)
	case OpScan:
		if op.IncludeThreads == nil {
			return base + "(all threads)" + label
		}
		return base + "(threads " + joinIDs(op.IncludeThreads) + ")" + label
	case OpAlloc:
		if op.ReuseAddr != "" {
			return fmt.Sprintf("%s(reg %s <= reuse %s)%s", base, op.Reg, op.ReuseAddr, label)
		}
		return fmt.Sprintf("%s(reg %s <= fresh)%s", base, op.Reg, label)
	default:
		return base + label
	}
}

func dash(s string) string {
	if s == "" {
		return "<nil>"
	}
	return s
}

// RenderWitness pretty-prints a violation and the schedule that produced it.
func RenderWitness(w Witness) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VIOLATION %s\n", w.Kind)
	fmt.Fprintf(&b, "  reason: %s\n", w.Violation.Detail)
	if w.Violation.Addr != "" {
		fmt.Fprintf(&b, "  object: addr=%s generation=%d\n",
			w.Violation.Addr, w.Violation.Identity)
	}
	b.WriteString("  interleaving witness:\n")
	for _, e := range w.Trace {
		fmt.Fprintf(&b, "    %2d. %s\n", e.Step, RenderOp(e.Op))
	}
	return b.String()
}

// RenderReport summarizes an exhaustive run.
func RenderReport(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "states=%d transitions=%d completed=%d infeasible-pruned=%d\n",
		r.StatesExplored, r.Transitions, r.Completed, r.InfeasibleBranches)
	if r.Safe() {
		b.WriteString("result: SAFE across every explored interleaving\n")
		return b.String()
	}
	keys := make([]ViolationKind, 0, len(r.ViolationCounts))
	for k := range r.ViolationCounts {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	b.WriteString("result: VIOLATIONS WITNESSED\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "  %s: %d interleaving(s)\n", k, r.ViolationCounts[k])
	}
	return b.String()
}
