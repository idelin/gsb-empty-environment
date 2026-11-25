package handoff

import (
	"net/netip"
	"sort"
)

// RecoveryNeeds explains which additional egress-bound exact-digest receipts
// are absent from each counterexample trace and are necessary to restore an
// authorized forward decision.
func RecoveryNeeds(protocol *Protocol, violations []Violation, policy Policy) []AckRequirement {
	if protocol == nil || len(violations) == 0 {
		return nil
	}
	set := make(map[string]AckRequirement)
	for _, v := range violations {
		for _, need := range recoveryForViolation(protocol, v, policy) {
			mergeAckNeed(set, need, v.Class)
		}
	}
	return sortedAckNeeds(set)
}

func mergeAckNeed(set map[string]AckRequirement, need AckRequirement, class string) {
	key := string(need.Egress) + "\x00" + need.StatementID + "\x00" + need.Digest.String() + "\x00" + need.Reason
	existing, ok := set[key]
	if !ok {
		existing = need
	}
	if class != "" {
		existing.Classes = appendUniqueString(existing.Classes, class)
		sort.Strings(existing.Classes)
	}
	set[key] = existing
}

func sortedAckNeeds(set map[string]AckRequirement) []AckRequirement {
	out := make([]AckRequirement, 0, len(set))
	for _, req := range set {
		out = append(out, req)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Egress != out[j].Egress {
			return out[i].Egress < out[j].Egress
		}
		if out[i].StatementID != out[j].StatementID {
			return out[i].StatementID < out[j].StatementID
		}
		if out[i].Digest != out[j].Digest {
			return out[i].Digest.String() < out[j].Digest.String()
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}

func recoveryForViolation(protocol *Protocol, v Violation, policy Policy) []AckRequirement {
	class, err := netip.ParsePrefix(v.Class)
	if err != nil {
		return nil
	}
	st := traceState(protocol, v.Trace)
	if st == nil {
		return nil
	}
	addr := class.Addr()
	decision := Select(st.views[v.Egress], addr, st.authz, policy)
	return recoveryNeedsForDecision(protocol, v.Trace, v.Egress, class, st, decision)
}

func recoveryNeedsForDecision(protocol *Protocol, trace []EventID, egress EgressID, class netip.Prefix, st *simState, decision Decision) []AckRequirement {
	addr := class.Addr()

	withdrawByDigest := make(map[Digest]Statement)
	statementIDs := make([]string, 0, len(protocol.Statements))
	for id, stmt := range protocol.Statements {
		statementIDs = append(statementIDs, id)
		for _, d := range stmt.Withdraws {
			withdrawByDigest[d] = stmt
		}
	}
	sort.Strings(statementIDs)

	var needs []AckRequirement
	seen := make(map[string]bool)
	add := func(stmt Statement, reason string) {
		ackID := EventID("ack:" + stmt.ID + ":" + string(egress))
		if traceContains(trace, ackID) {
			return
		}
		key := string(egress) + "/" + stmt.Digest().String() + "/" + reason
		if seen[key] {
			return
		}
		seen[key] = true
		needs = append(needs, AckRequirement{
			Egress:      egress,
			StatementID: stmt.ID,
			Digest:      stmt.Digest(),
			Reason:      reason,
		})
	}

	problemDigests := make(map[Digest]bool)
	for _, c := range decision.LevelClaims {
		problemDigests[c.Digest()] = true
	}
	if decision.ClaimDigest != (Digest{}) {
		problemDigests[decision.ClaimDigest] = true
	}
	for d := range problemDigests {
		if stmt, ok := withdrawByDigest[d]; ok {
			add(stmt, "ack the exact withdrawal to remove the conflicting/unauthorized longer route at this egress")
		}
	}

	for _, id := range statementIDs {
		stmt := protocol.Statements[id]
		for _, claim := range stmt.Announces {
			if !claim.Covers(addr) {
				continue
			}
			if !st.authz.Authorized(claim.Tenant, addr) {
				continue
			}
			add(stmt, "ack the exact authorized covering announcement from this egress")
			break
		}
	}

	if len(needs) == 0 {
		// Before a cut there may be no currently authorized new statement. The
		// pre-position ACK is still the extra receipt needed before cut can be
		// safely executed.
		for _, id := range statementIDs {
			stmt := protocol.Statements[id]
			for _, claim := range stmt.Announces {
				if claim.Covers(addr) && claim.Tenant == protocol.Spec.NewTenant {
					add(stmt, "pre-cut receipt required before arming authorization cut")
					break
				}
			}
		}
	}
	return needs
}

func traceState(protocol *Protocol, trace []EventID) *simState {
	if protocol == nil {
		return nil
	}
	st := protocol.initialState()
	for _, id := range trace {
		ev, ok := protocol.Events[id]
		if !ok {
			return nil
		}
		next := st.cloneFor(id)
		protocol.apply(next, ev)
		st = next
	}
	return st
}

func traceContains(trace []EventID, target EventID) bool {
	for _, id := range trace {
		if id == target {
			return true
		}
	}
	return false
}

func appendUniqueString(in []string, s string) []string {
	for _, x := range in {
		if x == s {
			return in
		}
	}
	return append(in, s)
}
