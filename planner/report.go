package planner

import (
	"fmt"
	"sort"
	"strings"
)

// Report renders a human-readable plan report.
func Report(inst *Instance, plan *Plan) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Forensics plan as of %s (evidence limit: %d)\n",
		inst.AsOf.Format(dateLayout), inst.MaxEvidence)

	if len(plan.ExpiredSkip) > 0 {
		fmt.Fprintf(&b, "Evidence excluded as not valid on the plan date: %s\n", strings.Join(plan.ExpiredSkip, ", "))
	}

	if !plan.Feasible {
		b.WriteString("\nRESULT: no feasible plan satisfies ALL acceptance criteria\n")
		for _, cp := range plan.Criteria {
			fmt.Fprintf(&b, "\n[%s] UNSATISFIED\n  expression: %s\n  reason: %s\n",
				cp.CriterionID, exprOf(inst, cp.CriterionID), cp.Reason)
		}
		return b.String()
	}

	fmt.Fprintf(&b, "\nRESULT: feasible, total cost %.2f, evidence items %d/%d\n",
		plan.TotalCost, len(plan.Evidence), inst.MaxEvidence)
	fmt.Fprintf(&b, "Evidence to collect: %s\n", strings.Join(plan.Evidence, ", "))
	for _, id := range plan.Evidence {
		info := inst.Evidence[id]
		validity := "no expiry"
		if info.HasUntil {
			validity = "valid until " + info.ValidUntil.Format(dateLayout)
		}
		fmt.Fprintf(&b, "  - %s: cost %.2f, %s\n", id, info.Ev.Cost, validity)
	}

	collected := make(map[string]bool)
	for _, id := range plan.Evidence {
		collected[id] = true
	}
	for _, cp := range plan.Criteria {
		fmt.Fprintf(&b, "\n[%s] SATISFIED\n", cp.CriterionID)
		fmt.Fprintf(&b, "  expression: %s\n", exprOf(inst, cp.CriterionID))
		if cp.Term != nil {
			fmt.Fprintf(&b, "  chosen path: %s\n", termString(*cp.Term))
		}
		root := rootOf(inst, cp.CriterionID)
		if root != nil {
			trace := Evaluate(root, collected, inst)
			b.WriteString(indent(trace.Render(), "  "))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func exprOf(inst *Instance, id string) string {
	for _, c := range inst.Criteria {
		if c.ID == id {
			return c.Expr
		}
	}
	return ""
}

func rootOf(inst *Instance, id string) *Node {
	for _, c := range inst.Criteria {
		if c.ID == id {
			return c.Root
		}
	}
	return nil
}

func termString(t Term) string {
	var parts []string
	req := make([]string, 0, len(t.Require))
	for id := range t.Require {
		req = append(req, id)
	}
	sort.Strings(req)
	parts = append(parts, req...)
	fbd := make([]string, 0, len(t.Forbid))
	for id := range t.Forbid {
		fbd = append(fbd, "NOT "+id)
	}
	sort.Strings(fbd)
	parts = append(parts, fbd...)
	if len(parts) == 0 {
		return "(true — no evidence needed)"
	}
	return strings.Join(parts, " AND ")
}

func indent(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}
