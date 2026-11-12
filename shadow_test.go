package netsec

import (
	"reflect"
	"testing"
)

func TestShadowedDenyByLaterBroadAllow(t *testing.T) {
	rules := []Rule{
		NewRule(Deny, "file-read*", Subpath("/Users/alice/private")),
		NewRule(Allow, "file-read*", Subpath("/Users/alice")),
	}
	reports := FindShadowedDenies(rules)
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1: %#v", len(reports), reports)
	}
	if reports[0].Index != 0 {
		t.Fatalf("shadowed deny index: got %d, want 0", reports[0].Index)
	}
	if !reflect.DeepEqual(reports[0].CoveredBy, []int{1}) {
		t.Fatalf("covering rules: got %v, want [1]", reports[0].CoveredBy)
	}
	if got := ShadowedDenies(rules); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("indices: got %v, want [0]", got)
	}
}

func TestShadowingDependsOnRuleOrder(t *testing.T) {
	flipped := []Rule{
		NewRule(Allow, "file-read*", Subpath("/Users/alice")),
		NewRule(Deny, "file-read*", Subpath("/Users/alice/private")),
	}
	if got := FindShadowedDenies(flipped); len(got) != 0 {
		t.Fatalf("allow before deny must not shadow: %#v", got)
	}
}

func TestSubpathBoundaryPreventsShadowing(t *testing.T) {
	rules := []Rule{
		NewRule(Deny, "file-read*", Subpath("/a/b")),
		NewRule(Allow, "file-read*", Subpath("/a/bc")),
	}
	if got := FindShadowedDenies(rules); len(got) != 0 {
		t.Fatalf("subpath /a/bc must not cover /a/b: %#v", got)
	}

	reversed := []Rule{
		NewRule(Deny, "file-read*", Subpath("/a/bc")),
		NewRule(Allow, "file-read*", Subpath("/a/b")),
	}
	if got := FindShadowedDenies(reversed); len(got) != 0 {
		t.Fatalf("subpath /a/b must not cover /a/bc: %#v", got)
	}
}

func TestOperationWildcardControlsShadowing(t *testing.T) {
	notCovered := []Rule{
		NewRule(Deny, "file-read*", Literal("/secret")),
		NewRule(Allow, "file-write*", Literal("/secret")),
	}
	if got := FindShadowedDenies(notCovered); len(got) != 0 {
		t.Fatalf("different operation prefix must not shadow: %#v", got)
	}

	covered := []Rule{
		NewRule(Deny, "file-read-data", Literal("/secret")),
		NewRule(Allow, "file-read*", Literal("/secret")),
	}
	if got := ShadowedDenies(covered); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("wildcard operation should cover exact operation: got %v", got)
	}

	partial := []Rule{
		NewRule(Deny, "file-read*", Literal("/secret")),
		NewRule(Allow, "file-read-data", Literal("/secret")),
	}
	if got := FindShadowedDenies(partial); len(got) != 0 {
		t.Fatalf("exact allow must not cover a wildcard deny: %#v", got)
	}
}

func TestPartialCoverageIsNotShadowing(t *testing.T) {
	partial := []Rule{
		NewRule(Deny, "file-read*", Subpath("/a/b")),
		NewRule(Allow, "file-read*", Subpath("/a/b/c")),
	}
	if got := FindShadowedDenies(partial); len(got) != 0 {
		t.Fatalf("narrower allow must not shadow broader deny: %#v", got)
	}
}

func TestLiteralDenyShadowedBySubpathAndRegex(t *testing.T) {
	bySubpath := []Rule{
		NewRule(Deny, "file-read*", Literal("/Users/alice/private/key")),
		NewRule(Allow, "file-read*", Subpath("/Users/alice/private")),
	}
	if got := ShadowedDenies(bySubpath); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("literal deny should be shadowed by subpath: got %v", got)
	}

	byRegex := []Rule{
		NewRule(Deny, "file-read*", Literal("/Users/alice/private/key")),
		NewRule(Allow, "file-read*", Regex(`^/Users/alice/.*$`)),
	}
	if got := ShadowedDenies(byRegex); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("literal deny should be shadowed by regex: got %v", got)
	}
}

func TestSubpathDenyShadowedByPrefixRegex(t *testing.T) {
	rules := []Rule{
		NewRule(Deny, "file-read*", Subpath("/secret/private")),
		NewRule(Allow, "file-read*", Regex(`^/secret/.*$`)),
	}
	if got := ShadowedDenies(rules); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("prefix regex should shadow subpath deny: got %v", got)
	}
}

func TestRegexDenyShadowedBySubpath(t *testing.T) {
	rules := []Rule{
		NewRule(Deny, "file-read*", Regex(`^/secret/.*$`)),
		NewRule(Allow, "file-read*", Subpath("/secret")),
	}
	if got := ShadowedDenies(rules); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("subpath should shadow contained regex deny: got %v", got)
	}
}

func TestUniversalRegexShadowsAnySubpathDeny(t *testing.T) {
	rules := []Rule{
		NewRule(Deny, "file-read*", Subpath("/secret")),
		NewRule(Allow, "file-read*", Regex(`.*`)),
	}
	if got := ShadowedDenies(rules); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("universal regex should shadow subpath deny: got %v", got)
	}
}

func TestRegexExcludingBareRootDoesNotShadowIt(t *testing.T) {
	rules := []Rule{
		NewRule(Deny, "file-read*", Subpath("/secret")),
		NewRule(Allow, "file-read*", Regex(`^/secret/.*$`)),
	}
	if got := FindShadowedDenies(rules); len(got) != 0 {
		t.Fatalf("regex does not match the bare subpath root: %#v", got)
	}
}

func TestLaterNarrowDenyDoesNotRestoreShadowedDeny(t *testing.T) {
	rules := []Rule{
		NewRule(Deny, "file-read*", Subpath("/a")),
		NewRule(Allow, "file-read*", Subpath("/a")),
		NewRule(Deny, "file-read*", Subpath("/a/b")),
	}
	if got := ShadowedDenies(rules); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("the broad deny is still fully overridden by the later allow: got %v", got)
	}
}

func TestUnrelatedLaterDenyDoesNotCancelShadowing(t *testing.T) {
	rules := []Rule{
		NewRule(Deny, "file-read*", Subpath("/a")),
		NewRule(Allow, "file-read*", Subpath("/a")),
		NewRule(Deny, "file-read*", Subpath("/x")),
	}
	if got := ShadowedDenies(rules); !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("unrelated deny should not cancel shadowing: got %v", got)
	}
}
