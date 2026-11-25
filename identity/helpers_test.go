package identity

import "testing"

func tok(tenant string, gen int64) Token  { return Token{Tenant: tenant, Generation: gen} }
func pol(tenant string, gen int64) Policy { return Policy{Tenant: tenant, Generation: gen} }

func hasIllegal(r *AnalysisResult, eventID int, reason string) bool {
	for _, a := range r.IllegalAttempts {
		if a.EventID == eventID && a.Reason == reason {
			return true
		}
	}
	return false
}

func assertOracleAgrees(t *testing.T, in Input, res *AnalysisResult) {
	t.Helper()
	oracle, err := EnumeratePermutations(in)
	if err != nil {
		t.Fatalf("oracle failed: %v", err)
	}
	if len(oracle.LegalHistories) == 0 {
		t.Fatal("oracle found no legal history while analyzer did")
	}
	for id, rr := range res.Reads {
		stat := oracle.Reads[id]
		if stat == nil {
			t.Fatalf("oracle missing read %d", id)
		}
		if rr.ReachableConform != stat.ReachableConform || rr.ReachableViolate != stat.ReachableViolate {
			t.Fatalf("read %d reachability mismatch: analyzer=(%v,%v) oracle=(%v,%v)",
				id, rr.ReachableConform, rr.ReachableViolate, stat.ReachableConform, stat.ReachableViolate)
		}
		if rr.ReachableConform && len(rr.ConformPrefix) != shortestLen(stat.ConformPrefixes) {
			t.Fatalf("read %d conform shortest prefix mismatch", id)
		}
		if rr.ReachableViolate && len(rr.ViolatePrefix) != shortestLen(stat.ViolatePrefixes) {
			t.Fatalf("read %d violate shortest prefix mismatch", id)
		}
		oracleClass := classifyFromOracle(stat)
		if rr.Class != oracleClass {
			t.Fatalf("read %d class mismatch: analyzer=%v oracle=%v", id, rr.Class, oracleClass)
		}
	}
}

func shortestLen(prefixes [][]int) int {
	best := -1
	for _, p := range prefixes {
		if best < 0 || len(p) < best {
			best = len(p)
		}
	}
	return best
}

func classifyFromOracle(stat *OracleReadStat) Class {
	switch {
	case stat.ReachableConform && stat.ReachableViolate:
		return Mixed
	case stat.ReachableConform:
		return AllConform
	case stat.ReachableViolate:
		return AllViolate
	default:
		return Unreachable
	}
}
