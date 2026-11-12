package netsec

import (
	"reflect"
	"testing"
)

func TestReplaceSignedAncestorBlocksAllDescendants(t *testing.T) {
	graph := map[string][]string{
		"tip":        {"main1"},
		"main1":      {"bad-target"},
		"branch":     {"bad-target"},
		"bad-target": nil,
		"other":      nil,
	}
	signed := map[string]bool{
		"tip":        true,
		"main1":      true,
		"branch":     true,
		"bad-target": true,
		"other":      true,
	}
	refs := []Ref{{Name: "refs/replace/bad-target", Target: "unsigned-rewrite"}}

	got := Analyze(refs, graph, signed, false)
	wantAffected := StringSet{
		"bad-target": true,
		"main1":      true,
		"tip":        true,
		"branch":     true,
	}
	if !reflect.DeepEqual(got.Affected, wantAffected) {
		t.Fatalf("Affected = %v, want %v", got.Affected, wantAffected)
	}
	if !reflect.DeepEqual(got.Blocked, wantAffected) {
		t.Fatalf("Blocked = %v, want %v", got.Blocked, wantAffected)
	}
	if got.Passed["tip"] || got.Passed["main1"] || got.Passed["branch"] || got.Passed["bad-target"] {
		t.Fatalf("signed descendants passed through replacement: %v", got.Passed)
	}
	if !got.Passed["other"] {
		t.Fatalf("unrelated signed commit was blocked: %v", got.Passed)
	}
	if got.Allowed {
		t.Fatal("replacement was accepted without allowReplace")
	}
}

func TestAllowReplaceRequiresVerifiedTarget(t *testing.T) {
	graph := map[string][]string{
		"child":           {"signed-ancestor"},
		"signed-ancestor": nil,
	}
	refs := []Ref{{Name: "refs/replace/signed-ancestor", Target: "rewrite"}}
	signed := map[string]bool{
		"child":    true,
		"ancestor": true,
	}

	got := Analyze(refs, graph, signed, true)
	if !got.Blocked["signed-ancestor"] || !got.Blocked["child"] {
		t.Fatalf("unsigned replacement target was allowed: %v", got.Blocked)
	}
	if got.Allowed {
		t.Fatal("check passed with an unsigned replacement target")
	}

	signed["rewrite"] = true
	got = Analyze(refs, graph, signed, true)
	if !got.Allowed || len(got.Blocked) != 0 {
		t.Fatalf("verified replacement target was not allowed: %+v", got)
	}
	if !got.Affected["signed-ancestor"] || !got.Affected["child"] {
		t.Fatalf("allowed replacement disappeared from affected set: %v", got.Affected)
	}
	if !got.Passed["child"] {
		t.Fatalf("signed child did not pass after an allowed replacement: %v", got.Passed)
	}
}

func TestVerifiedTargetDoesNotBypassWithoutAllowReplace(t *testing.T) {
	graph := map[string][]string{
		"child":    {"ancestor"},
		"ancestor": nil,
	}
	signed := map[string]bool{
		"child":    true,
		"ancestor": true,
		"rewrite":  true,
	}
	refs := []Ref{{Name: "refs/replace/ancestor", Target: "rewrite"}}

	got := Analyze(refs, graph, signed, false)
	if got.Allowed {
		t.Fatal("verified replacement target bypassed without explicit allowReplace")
	}
	if !got.Blocked["child"] {
		t.Fatalf("descendant was not blocked: %v", got.Blocked)
	}
}

func TestGraftsAlwaysBlockIncludingDescendants(t *testing.T) {
	graph := map[string][]string{
		"tip":         {"mid"},
		"mid":         {"grafted"},
		"grafted":     {"real-parent"},
		"real-parent": nil,
	}
	signed := map[string]bool{
		"tip":         true,
		"mid":         true,
		"grafted":     true,
		"real-parent": true,
	}

	got := Analyze(Input{
		Graph:         graph,
		Signed:        signed,
		GraftContents: "grafted forged-parent\n",
		AllowReplace:  true,
	})
	want := StringSet{"grafted": true, "mid": true, "tip": true}
	if !reflect.DeepEqual(got.Affected, want) {
		t.Fatalf("Affected = %v, want %v", got.Affected, want)
	}
	if !reflect.DeepEqual(got.Blocked, want) {
		t.Fatalf("graft was allowed by allowReplace: %v", got.Blocked)
	}
	if got.Allowed {
		t.Fatal("graft passed verification")
	}
	if len(got.Replacements) != 1 || got.Replacements[0].Kind != KindGraft {
		t.Fatalf("graft was not reported: %+v", got.Replacements)
	}
}

func TestGraftPseudoRefIsDetected(t *testing.T) {
	refs := []Ref{{Name: "info/grafts", Target: "grafted forged-parent\n"}}
	graph := map[string][]string{
		"tip":     {"grafted"},
		"grafted": {"real-parent"},
	}

	got := Analyze(refs, graph, map[string]bool{"tip": true}, false)
	if !got.Blocked["grafted"] || !got.Blocked["tip"] {
		t.Fatalf("info/grafts contents were ignored: %v", got.Blocked)
	}
}

func TestReplaceRefNamedInfoGraftsIsNotGraftContent(t *testing.T) {
	graph := map[string][]string{
		"original": {"parent"},
		"parent":   nil,
		"target":   nil,
	}
	refs := []Ref{{Name: "refs/replace/info/grafts", Target: "target"}}

	got := Analyze(refs, graph, map[string]bool{}, true)
	if !got.Blocked["info/grafts"] || got.Affected["target"] {
		t.Fatalf("replace ref was misread as grafts content: %+v", got)
	}
}

func TestReplacementTargetIsNotFollowed(t *testing.T) {
	graph := map[string][]string{
		"original-child": {"original"},
		"original":       nil,
		"target-only":    {"target-parent"},
		"target-parent":  nil,
	}
	refs := []Ref{{Name: "refs/replace/original", Target: "target-only"}}

	got := Analyze(refs, graph, map[string]bool{"original-child": true}, false)
	want := StringSet{"original": true, "original-child": true}
	if !reflect.DeepEqual(got.Affected, want) {
		t.Fatalf("replacement target was traversed: affected = %v, want %v", got.Affected, want)
	}
	if got.Affected["target-only"] || got.Affected["target-parent"] {
		t.Fatalf("replacement target leaked into affected set: %v", got.Affected)
	}
}

func TestUnrelatedRefsAreIgnored(t *testing.T) {
	graph := map[string][]string{"tip": {"parent"}, "parent": nil}
	signed := map[string]bool{"tip": true, "parent": true}
	refs := []Ref{
		{Name: "refs/heads/main", Target: "tip"},
		{Name: "refs/replacements/fake", Target: "parent"},
	}

	got := Analyze(refs, graph, signed, false)
	if !got.OK() || len(got.Affected) != 0 {
		t.Fatalf("non-replace refs changed the result: %+v", got)
	}
}

func TestMultipleReplacementsForSameOriginalFailClosed(t *testing.T) {
	graph := map[string][]string{"tip": {"ancestor"}, "ancestor": nil}
	signed := map[string]bool{"ancestor": true, "unsigned": false, "signed": true}
	refs := []Ref{
		{Name: "refs/replace/ancestor", Target: "signed"},
		{Name: "refs/replace/ancestor", Target: "unsigned"},
	}

	got := Analyze(refs, graph, signed, true)
	if got.Allowed || !got.Blocked["ancestor"] || !got.Blocked["tip"] {
		t.Fatalf("one unsigned replacement was enough to allow history: %+v", got)
	}
}

func TestMalformedReplaceRefFailsClosed(t *testing.T) {
	graph := map[string][]string{"tip": {"parent"}, "parent": nil}
	signed := map[string]bool{"tip": true, "parent": true}
	refs := []Ref{{Name: "refs/replace/", Target: "somewhere"}}

	got := Analyze(refs, graph, signed, true)
	if got.Err == nil || got.Allowed {
		t.Fatalf("malformed replace ref did not fail closed: %+v", got)
	}
	if len(got.Blocked) != 2 || len(got.Passed) != 0 {
		t.Fatalf("malformed replace ref was not treated as a global blocker: %+v", got)
	}
}

func TestMalformedGraftsFailsClosed(t *testing.T) {
	graph := map[string][]string{"tip": {"parent"}, "parent": nil}
	got := Analyze(Input{
		Graph:         graph,
		Signed:        map[string]bool{"tip": true},
		GraftContents: "grafted\x00parent",
		AllowReplace:  true,
	})
	if got.Err == nil || got.Allowed || !got.Blocked["tip"] || !got.Blocked["parent"] {
		t.Fatalf("malformed grafts input did not fail closed: %+v", got)
	}
}

func TestAnalyzeAcceptsCommonSetAndGraftForms(t *testing.T) {
	graph := map[string][]string{"tip": {"grafted"}, "grafted": nil}

	got := Analyze(
		map[string]string{"info/grafts": "grafted parent"},
		graph,
		[]string{"tip", "grafted"},
		"grafted parent\n",
		true,
	)
	if got.Err != nil {
		t.Fatalf("Analyze rejected supported argument forms: %v", got.Err)
	}
	if !got.Blocked["grafted"] || !got.Blocked["tip"] {
		t.Fatalf("graft was not blocked: %+v", got)
	}
	if !Verify([]Ref{{Name: "refs/heads/main", Target: "tip"}}, graph, []string{"tip", "grafted"}, false) {
		t.Fatal("unrelated repository did not verify")
	}
}

func TestParseGrafts(t *testing.T) {
	got, err := ParseGrafts("\n# comment\r\nA\nB P1 P2\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Graft{
		{Commit: "A"},
		{Commit: "B", Parents: []string{"P1", "P2"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseGrafts = %+v, want %+v", got, want)
	}
}

func TestDescendantClosureHandlesCyclesAndMerges(t *testing.T) {
	graph := map[string][]string{
		"root":  {"left", "right"},
		"left":  {"root"},
		"right": {"root"},
		"tip":   {"left", "right"},
	}
	got := Analyze(
		[]Ref{{Name: "refs/replace/root", Target: "rewrite"}},
		graph,
		map[string]bool{"tip": true},
		false,
	)
	for _, id := range []string{"root", "left", "right", "tip"} {
		if !got.Blocked[id] {
			t.Fatalf("%s was not blocked: %v", id, got.Blocked)
		}
	}
}
