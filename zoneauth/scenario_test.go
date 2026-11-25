package zoneauth_test

import (
	"testing"

	"prescreen.local/ddd-10561-v4/testsupport"
	"prescreen.local/ddd-10561-v4/zoneauth"
)

func verdictsByQuery(t *testing.T, fixture *testsupport.Fixture) map[string]*zoneauth.Verdict {
	t.Helper()
	entries := make([]zoneauth.CachedQuery, 0, len(fixture.Cached))
	for _, entry := range fixture.Cached {
		entries = append(entries, zoneauth.CachedQuery{
			Query: entry.Query, Answer: entry.Answer, Proof: entry.Proof,
		})
	}
	verdicts, _, err := zoneauth.Reevaluate(fixture.Patch, fixture.OldRoot, trustedKeys(fixture), entries)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]*zoneauth.Verdict, len(verdicts))
	for i := range verdicts {
		out[testsupport.QueryKey(verdicts[i].Query)] = &verdicts[i]
	}
	return out
}

// TestScenarioHandoff checks a real cross-zone key handoff: the new root is
// signed by the new key, the old key signs the handoff binding both roots,
// and cached answers are re-evaluated under the extended trust set.
func TestScenarioHandoff(t *testing.T) {
	fixture := loadGolden(t, "handoff")
	verdicts := verdictsByQuery(t, fixture)

	if fixture.Patch.Handoff == nil {
		t.Fatal("expected key handoff")
	}
	if got := verdicts["org/new"]; got.Kind != zoneauth.VerdictChanged ||
		got.NewAnswer.Kind != zoneauth.AnswerReferral {
		t.Fatalf("org/new should become a referral, got %+v", got)
	}
	if got := verdicts["org/new/sub"]; got.Kind != zoneauth.VerdictChanged ||
		got.NewAnswer.Kind != zoneauth.AnswerReferral {
		t.Fatalf("delegation must propagate below the cut, got %+v", got)
	}
	if got := verdicts["net/edge"]; got.Kind != zoneauth.VerdictReusable {
		t.Fatalf("unrelated side branch must stay reusable, got %s", got.Kind)
	}
}

// TestScenarioWildcardOnly checks that changing only a wildcard template
// moves the root but leaves the anchor's own NODATA answer untouched.
func TestScenarioWildcardOnly(t *testing.T) {
	fixture := loadGolden(t, "wildcard-only")
	verdicts := verdictsByQuery(t, fixture)

	for _, query := range []string{"svc/a", "svc/b"} {
		got := verdicts[query]
		if got.Kind != zoneauth.VerdictChanged || !got.NewAnswer.ViaWildcard {
			t.Fatalf("%s should change via wildcard, got %+v", query, got)
		}
	}
	got := verdicts["svc"]
	if got.Kind != zoneauth.VerdictReusable || got.NewAnswer.Kind != zoneauth.AnswerNoData {
		t.Fatalf("anchor nodata must be reusable, got %+v", got)
	}
}

// TestScenarioReexpose checks removal of a near delegation cut so that the
// far-ancestor apex wildcard re-emerges for the subtree beneath the cut.
func TestScenarioReexpose(t *testing.T) {
	fixture := loadGolden(t, "reexpose")
	verdicts := verdictsByQuery(t, fixture)

	got := verdicts["near/deep/leaf"]
	if got.Kind != zoneauth.VerdictChanged ||
		got.NewAnswer.Kind != zoneauth.AnswerNXDomain {
		t.Fatalf("deep name beyond the single-label wildcard should be nxdomain, got %+v", got)
	}
	if got := verdicts["near"]; got.Kind != zoneauth.VerdictChanged ||
		got.NewAnswer.Kind != zoneauth.AnswerNoData {
		t.Fatalf("cut node should become a regular nodata node, got %+v", got)
	}
	if got := verdicts["fresh"]; got.Kind != zoneauth.VerdictReusable ||
		got.NewAnswer.Kind != zoneauth.AnswerPositive || !got.NewAnswer.ViaWildcard {
		t.Fatalf("far-ancestor wildcard must stay visible for fresh names, got %+v", got)
	}
	if got := verdicts["other/thing"]; got.Kind != zoneauth.VerdictReusable {
		t.Fatalf("apex wildcard answer elsewhere must stay reusable, got %s", got.Kind)
	}
}

// TestScenarioSideBranch checks that an unrelated root change does not
// invalidate cached answers in untouched branches, including the
// "kee" vs "keep" string-prefix trap.
func TestScenarioSideBranch(t *testing.T) {
	fixture := loadGolden(t, "side-branch")
	verdicts := verdictsByQuery(t, fixture)

	for _, query := range []string{"keep", "keep/child", "edit", "kee/lookalike"} {
		if got := verdicts[query]; got.Kind != zoneauth.VerdictReusable {
			t.Fatalf("%s must be reusable after an unrelated edit, got %s (%s)",
				query, got.Kind, got.Reason)
		}
	}
}
