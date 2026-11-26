package capmig

import (
	"fmt"
	"strings"
)

// RenderText returns a deterministic, human-auditable report.
func RenderText(res *Result) string {
	var b strings.Builder

	fmt.Fprintf(&b, "CAPABILITY MIGRATION REPORT\n")
	fmt.Fprintf(&b, "barrier epoch: %d\n", res.BarrierEpoch)
	fmt.Fprintf(&b, "histories: total=%d admissible=%d\n", res.TotalHistories, res.AdmissibleHistories)
	fmt.Fprintf(&b, "inconsistent: %t\n", res.Inconsistent)
	fmt.Fprintf(&b, "exclusivity invariant held: %t\n", res.ExclusivityHeld)
	fmt.Fprintf(&b, "joint consistency held: %t\n", res.JointConsistencyHeld)
	fmt.Fprintf(&b, "solve millis: %d\n\n", res.SolveMillis)

	if len(res.AliasTable) > 0 {
		b.WriteString("ALIAS RESOLUTION\n")
		for _, a := range res.AliasTable {
			fmt.Fprintf(&b, "  %s -> %s\n", a.Alias, a.PhysicalID)
		}
		b.WriteString("\n")
	}

	if len(res.RejectedReceipts) > 0 {
		b.WriteString("BARRIER-REJECTED RECEIPTS\n")
		for _, r := range res.RejectedReceipts {
			fmt.Fprintf(&b, "  handoff=%s token=%s receipt_epoch=%d < barrier=%d\n",
				r.HandoffID, r.Token, r.ReceiptEpoch, r.BarrierEpoch)
		}
		b.WriteString("\n")
	}

	if len(res.AmbiguousVariables) > 0 {
		b.WriteString("AMBIGUOUS HANDOFF VARIABLES\n")
		for _, v := range res.AmbiguousVariables {
			fmt.Fprintf(&b, "  x%d %s: token=%s %s -> %s effective=%t\n",
				v.Index, v.HandoffID, v.Token, v.From, v.To, v.Effective)
		}
		b.WriteString("\n")
	}

	b.WriteString("MIGRATION DECISIONS\n")
	for _, t := range res.TicketReports {
		fmt.Fprintf(&b, "  ticket=%s subject=%s decision=%s support=%d/%d target=%s actions=%s\n",
			t.ID, t.Subject, t.Decision, t.SupportingHistories, t.AdmissibleHistories,
			t.TargetPhysical, strings.Join(t.TargetActions, ","))
		for _, req := range t.Requirements {
			if req.Unsupported {
				fmt.Fprintf(&b, "    requirement %s/%s: UNSUPPORTED by any ancestor\n",
					req.PhysicalObject, req.Action)
				continue
			}
			for _, c := range req.CoveringTokens {
				fmt.Fprintf(&b, "    requirement %s/%s via token=%s object_path=%s action_path=%s holder_in_all=%t alive_in_all=%t\n",
					req.PhysicalObject, req.Action, c.Token,
					strings.Join(c.ObjectPath, "->"), strings.Join(c.ActionPath, "->"),
					c.HolderInAll, c.AliveInAll)
			}
		}
		for token, hops := range t.Chain {
			if len(hops) == 0 {
				continue
			}
			fmt.Fprintf(&b, "    chain token=%s\n", token)
			for _, h := range hops {
				flags := ""
				if h.RejectedByBarrier {
					flags = " BARRIER_REJECTED"
				}
				if h.LostConfirmation {
					flags += " LOST_CONFIRMATION"
				}
				fmt.Fprintf(&b, "      hop=%s %s->%s epoch=%d success_histories=%d%s\n",
					h.HandoffID, h.From, h.To, h.Epoch, h.SuccessHistories, flags)
			}
		}
		if len(t.EffectiveVariables) > 0 {
			fmt.Fprintf(&b, "    effective variables: %s\n", strings.Join(t.EffectiveVariables, ","))
		}
		if len(t.BarrierRejects) > 0 {
			fmt.Fprintf(&b, "    barrier rejects: %s\n", strings.Join(t.BarrierRejects, ","))
		}
		if len(t.TouchesRevocation) > 0 {
			fmt.Fprintf(&b, "    revocations: %s\n", strings.Join(t.TouchesRevocation, ","))
		}
		if len(t.CausalFailures) > 0 {
			fmt.Fprintf(&b, "    causal failures: %s\n", strings.Join(t.CausalFailures, ","))
		}
		if len(t.ReauthCandidates) > 0 {
			fmt.Fprintf(&b, "    reauth candidates: %s\n", strings.Join(t.ReauthCandidates, ","))
		}
		for _, cx := range t.Counterexamples {
			fmt.Fprintf(&b, "    counterexample mask=%d choices=%s reason=%q\n",
				cx.Mask, formatChoices(cx.VariableChoices), cx.FailedReason)
		}
	}

	b.WriteString("\nMINIMUM RE-AUTHENTICATION SUBJECTS\n")
	if len(res.MinimumReauthSubjects) == 0 {
		b.WriteString("  (none)\n")
	} else {
		for _, s := range res.MinimumReauthSubjects {
			fmt.Fprintf(&b, "  %s\n", s)
		}
	}
	return b.String()
}

func formatChoices(choices map[string]bool) string {
	keys := make([]string, 0, len(choices))
	for k := range choices {
		keys = append(keys, k)
	}
	sortStrings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%t", k, choices[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
