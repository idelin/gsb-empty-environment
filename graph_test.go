package enumkit

import (
	"errors"
	"strings"
	"testing"
)

func TestMergeAllowsConsistentAliases(t *testing.T) {
	r := newColorEnum(t)
	g, err := r.Merge()
	must(t, err, "merge v1..v3")
	if len(g.versions) != 3 {
		t.Fatalf("merged versions = %v", g.versions)
	}
}

func TestMergeRejectsTwoIDsSharingActiveName(t *testing.T) {
	r := New("color")
	must(t, r.Define(1, "RED"), "def1")
	must(t, r.Define(2, "GREEN"), "def2")
	must(t, r.AddVersion(Version{Name: "v1", Active: map[int64]string{1: "RED", 2: "GREEN"}}), "v1")
	must(t, r.AddVersion(Version{
		Name:    "v2",
		Active:  map[int64]string{1: "RED", 2: "EMERALD"},
		Aliases: []Alias{{Name: "GREEN", ID: 1}}, // old active name of 2 now points at 1
	}), "v2")
	_, err := r.Merge()
	var mce *MergeConflictError
	if !errors.As(err, &mce) {
		t.Fatalf("err = %v, want MergeConflictError", err)
	}
	if mce.Name != "GREEN" || len(mce.IDs) != 2 {
		t.Fatalf("conflict = %+v", mce)
	}
	if len(mce.Chain) == 0 {
		t.Fatal("missing shortest conflict chain")
	}
	if !strings.Contains(mce.Error(), "GREEN") || !strings.Contains(mce.Error(), "#1") || !strings.Contains(mce.Error(), "#2") {
		t.Fatalf("chain not rendered: %s", mce.Error())
	}
}

func TestMigrateRenameToCanonical(t *testing.T) {
	r := newColorEnum(t)
	g, err := r.Merge()
	must(t, err, "merge")
	v, err := g.Migrate("v1", "v3", "RED")
	must(t, err, "migrate RED v1->v3")
	if v.ID != 1 {
		t.Fatalf("RED migrated to #%d", v.ID)
	}
	if got := g.CanonicalAt("v3", v); got != "CRIMSON" {
		t.Fatalf("canonical at v3 = %q, want CRIMSON", got)
	}
	// Old name GREEN is known in the merged graph (alias at v2).
	v2, err := g.Migrate("v1", "v3", "GREEN")
	must(t, err, "migrate GREEN")
	if got := g.CanonicalAt("v3", v2); got != "EMERALD" {
		t.Fatalf("canonical at v3 = %q, want EMERALD", got)
	}
}

func TestMigrateUnknownPassthrough(t *testing.T) {
	r := newColorEnum(t)
	g, err := r.Merge()
	must(t, err, "merge")
	v, err := g.Migrate("v1", "v3", "MAGENTA")
	must(t, err, "migrate unknown name")
	if v.Known() || v.Raw != "MAGENTA" {
		t.Fatalf("unknown name = %+v", v)
	}
	if got := g.CanonicalAt("v3", v); got != "MAGENTA" {
		t.Fatalf("passthrough = %q, want MAGENTA", got)
	}
	v2, err := g.Migrate("v1", "v3", "77")
	must(t, err, "migrate unknown number")
	if v2.Known() || v2.Kind != UnknownNumber {
		t.Fatalf("unknown number = %+v", v2)
	}
}

func TestMigrateAmbiguityReturnsError(t *testing.T) {
	r := New("lamp")
	must(t, r.Define(4, "AMBER"), "def4")
	must(t, r.Define(5, "VIOLET"), "def5")
	must(t, r.AddVersion(Version{
		Name:    "v1",
		Active:  map[int64]string{4: "AMBER", 5: "VIOLET"},
		Aliases: []Alias{{Name: "LIGHT", ID: 4}},
	}), "v1")
	must(t, r.AddVersion(Version{
		Name:    "v2",
		Active:  map[int64]string{4: "AMBER", 5: "VIOLET"},
		Aliases: []Alias{{Name: "LIGHT", ID: 5}},
	}), "v2")
	g, err := r.Merge() // alias-only collision is mergeable
	must(t, err, "merge")
	_, err = g.Migrate("v1", "v2", "LIGHT")
	var ae *AmbiguityError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want AmbiguityError", err)
	}
	if len(ae.IDs) != 2 || ae.IDs[0] != 4 || ae.IDs[1] != 5 {
		t.Fatalf("candidates = %v, want [4 5]", ae.IDs)
	}
	if len(ae.Chain) == 0 {
		t.Fatal("ambiguity without shortest chain")
	}
	msg := ae.Error()
	if !strings.Contains(msg, "LIGHT") || !strings.Contains(msg, "#4") || !strings.Contains(msg, "#5") {
		t.Fatalf("chain not rendered: %s", msg)
	}
}

func TestVerifyConsistentGraph(t *testing.T) {
	r := newColorEnum(t)
	g, err := r.Merge()
	must(t, err, "merge")
	if errs := g.Verify(); len(errs) != 0 {
		for _, e := range errs {
			t.Logf("verify: %v", e)
		}
		t.Fatalf("verify found %d problems", len(errs))
	}
}

func TestVerifyDetectsAmbiguousPath(t *testing.T) {
	r := New("lamp")
	must(t, r.Define(4, "AMBER"), "def4")
	must(t, r.Define(5, "VIOLET"), "def5")
	must(t, r.AddVersion(Version{
		Name:    "v1",
		Active:  map[int64]string{4: "AMBER", 5: "VIOLET"},
		Aliases: []Alias{{Name: "LIGHT", ID: 4}},
	}), "v1")
	must(t, r.AddVersion(Version{
		Name:    "v2",
		Active:  map[int64]string{4: "AMBER", 5: "VIOLET"},
		Aliases: []Alias{{Name: "LIGHT", ID: 5}},
	}), "v2")
	g, err := r.Merge()
	must(t, err, "merge")
	errs := g.Verify()
	if len(errs) == 0 {
		t.Fatal("verify missed the ambiguous alias")
	}
	var ve *VerifyError
	found := false
	for _, e := range errs {
		if errors.As(e, &ve) {
			var ae *AmbiguityError
			if errors.As(ve.Err, &ae) && ve.Token == "LIGHT" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no VerifyError wrapping AmbiguityError for LIGHT: %v", errs)
	}
}

func TestShortestChainAcrossHops(t *testing.T) {
	// Chain: #1 -[A@v1]- #2 -[B@v2]- #3. Conflict between 1 and 3
	// via name C would be direct; instead force a 2-hop chain by
	// merging graphs where 1 and 3 only connect through 2.
	r := New("chain")
	for _, d := range []struct {
		id   int64
		name string
	}{{1, "ONE"}, {2, "TWO"}, {3, "THREE"}} {
		must(t, r.Define(d.id, d.name), "define")
	}
	// Cross-version alias reuse builds the hops:
	// v1: A->1, v2: A->2 and B->2, v3: B->3.
	must(t, r.AddVersion(Version{
		Name:    "v1",
		Active:  map[int64]string{1: "ONE", 2: "TWO", 3: "THREE"},
		Aliases: []Alias{{Name: "A", ID: 1}},
	}), "v1")
	must(t, r.AddVersion(Version{
		Name:    "v2",
		Active:  map[int64]string{1: "ONE", 2: "TWO", 3: "THREE"},
		Aliases: []Alias{{Name: "A", ID: 2}, {Name: "B", ID: 2}},
	}), "v2")
	must(t, r.AddVersion(Version{
		Name:    "v3",
		Active:  map[int64]string{1: "ONE", 2: "TWO", 3: "THREE"},
		Aliases: []Alias{{Name: "B", ID: 3}},
	}), "v3")
	g, err := r.Merge()
	must(t, err, "merge")
	chain := g.chain(1, 3)
	if len(chain) != 2 {
		t.Fatalf("chain length = %d, want 2 hops: %s", len(chain), RenderChain(chain))
	}
	if chain[0].Name != "A" || chain[1].Name != "B" {
		t.Fatalf("chain = %s", RenderChain(chain))
	}
}
