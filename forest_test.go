package netsec_test

import (
	"errors"
	"reflect"
	"testing"

	netsec "example.org/netsec/q0285"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertErrIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("got error %v, want %v", err, target)
	}
}

func values(tags []netsec.EffectiveTag) map[string]string {
	result := make(map[string]string, len(tags))
	for _, tag := range tags {
		result[tag.Key] = tag.Value
	}
	return result
}

func inheritedByKey(entries []netsec.Provenance) map[string]netsec.Provenance {
	result := make(map[string]netsec.Provenance, len(entries))
	for _, entry := range entries {
		result[entry.Key] = entry
	}
	return result
}

func mustInherited(t *testing.T, f *netsec.Forest, id netsec.NodeID) []netsec.Provenance {
	t.Helper()
	entries, err := f.InheritedTags(id)
	must(t, err)
	return entries
}

func buildDeepTree(t *testing.T) *netsec.Forest {
	t.Helper()
	f := netsec.NewForest()
	// root -> a -> b -> c, plus root -> d.
	must(t, f.AddNode("root", ""))
	must(t, f.AddNode("a", "root"))
	must(t, f.AddNode("b", "a"))
	must(t, f.AddNode("c", "b"))
	must(t, f.AddNode("d", "root"))
	return f
}

func TestExplicitAndInheritedResolution(t *testing.T) {
	f := buildDeepTree(t)
	must(t, f.SetTag("root", "env", "prod"))
	must(t, f.SetTag("b", "env", "staging"))

	rootTags, err := f.EffectiveTags("root")
	must(t, err)
	if got := values(rootTags); got["env"] != "prod" || !rootTags[0].Explicit {
		t.Fatalf("root effective tags = %+v", rootTags)
	}

	aTags, err := f.EffectiveTags("a")
	must(t, err)
	if got := values(aTags); got["env"] != "prod" || aTags[0].Explicit {
		t.Fatalf("a effective tags = %+v", aTags)
	}

	bTags, err := f.EffectiveTags("b")
	must(t, err)
	if got := values(bTags); got["env"] != "staging" || !bTags[0].Explicit {
		t.Fatalf("b effective tags = %+v", bTags)
	}

	cTags, err := f.EffectiveTags("c")
	must(t, err)
	if got := values(cTags); got["env"] != "staging" {
		t.Fatalf("c should inherit b's explicit value, got %+v", cTags)
	}
	if cTags[0].Source != "b" || cTags[0].Explicit {
		t.Fatalf("c provenance = %+v", cTags[0])
	}

	entry := inheritedByKey(mustInherited(t, f, "c"))["env"]
	wantPath := []netsec.NodeID{"c", "b"}
	if entry.Source != "b" || entry.Value != "staging" || entry.Blocked {
		t.Fatalf("c inherited provenance = %+v", entry)
	}
	if len(entry.Path) != len(wantPath) {
		t.Fatalf("provenance path = %v, want %v", entry.Path, wantPath)
	}
	for i := range wantPath {
		if entry.Path[i] != wantPath[i] {
			t.Fatalf("provenance path = %v, want %v", entry.Path, wantPath)
		}
	}

	if ok, err := f.VerifyProvenance("c"); err != nil || !ok {
		t.Fatalf("VerifyProvenance c = %v, %v", ok, err)
	}
}

func TestBlockStopsPenetrationAndExplicitWins(t *testing.T) {
	f := buildDeepTree(t)
	must(t, f.SetTag("root", "env", "prod"))
	must(t, f.BlockInheritance("b", "env"))

	if blocked, err := f.IsBlocked("b", "env"); err != nil || !blocked {
		t.Fatalf("b blocked = %v, %v", blocked, err)
	}

	if tags, err := f.EffectiveTags("b"); err != nil || len(tags) != 0 {
		t.Fatalf("b must not inherit blocked env, got %+v", tags)
	}
	if tags, err := f.EffectiveTags("c"); err != nil || len(tags) != 0 {
		t.Fatalf("blocked marker must stop penetration to c, got %+v", tags)
	}

	entry := inheritedByKey(mustInherited(t, f, "c"))["env"]
	if !entry.Blocked || entry.Source != "b" || entry.Value != "" {
		t.Fatalf("blocked provenance = %+v", entry)
	}

	// An explicit tag on the blocked node still applies and overrides marker.
	must(t, f.SetTag("b", "env", "local"))
	if tags, err := f.EffectiveTags("b"); err != nil || values(tags)["env"] != "local" {
		t.Fatalf("explicit tag must win over block, got %+v", tags)
	}
	if tags, err := f.EffectiveTags("c"); err != nil || values(tags)["env"] != "local" {
		t.Fatalf("c must inherit b's explicit local value, got %+v", tags)
	}

	// Deleting the explicit value keeps the cut-off marker in effect.
	must(t, f.DeleteTag("b", "env"))
	if tags, err := f.EffectiveTags("b"); err != nil || len(tags) != 0 {
		t.Fatalf("after delete, block still stops root value, got %+v", tags)
	}

	// Unblock restores inheritance from the nearest ancestor.
	must(t, f.UnblockInheritance("b", "env"))
	if tags, err := f.EffectiveTags("c"); err != nil || values(tags)["env"] != "prod" {
		t.Fatalf("unblock must restore root inheritance, got %+v", tags)
	}

	// Deleting an explicit value restores inheritance rather than pinning it.
	must(t, f.SetTag("a", "env", "canary"))
	if tags, err := f.EffectiveTags("c"); err != nil || values(tags)["env"] != "canary" {
		t.Fatalf("c should inherit a canary, got %+v", tags)
	}
	must(t, f.DeleteTag("a", "env"))
	if tags, err := f.EffectiveTags("c"); err != nil || values(tags)["env"] != "prod" {
		t.Fatalf("delete explicit must restore root inheritance, got %+v", tags)
	}

	if ok, err := f.VerifyProvenance("b"); err != nil || !ok {
		t.Fatalf("verify b = %v, %v", ok, err)
	}
}

func TestClosureRecomputeIsScoped(t *testing.T) {
	f := buildDeepTree(t)
	must(t, f.SetTag("root", "k1", "v1"))
	must(t, f.SetTag("root", "k2", "v2"))

	// A second root subtree must keep its own provenance untouched.
	must(t, f.AddNode("other", ""))
	must(t, f.AddNode("leaf", "other"))
	must(t, f.SetTag("other", "k1", "foreign"))

	before, err := f.InheritedTags("leaf")
	must(t, err)
	genBefore := f.Generation()

	must(t, f.SetTag("a", "k1", "changed"))

	after, err := f.InheritedTags("leaf")
	must(t, err)
	if len(before) != len(after) {
		t.Fatalf("unrelated subtree cache length changed: %+v vs %+v", before, after)
	}
	for i := range before {
		if !reflect.DeepEqual(before[i], after[i]) {
			t.Fatalf("unrelated subtree provenance mutated: %+v vs %+v", before[i], after[i])
		}
	}

	// b and c pick up a's new value; d still resolves against root.
	cTags, err := f.EffectiveTags("c")
	must(t, err)
	if values(cTags)["k1"] != "changed" {
		t.Fatalf("c k1 = %+v", cTags)
	}
	dTags, err := f.EffectiveTags("d")
	must(t, err)
	if values(dTags)["k1"] != "v1" || values(dTags)["k2"] != "v2" {
		t.Fatalf("d tags = %+v", dTags)
	}

	// A block on a only re-resolves a's closure.
	must(t, f.BlockInheritance("a", "k2"))
	cTags, err = f.EffectiveTags("c")
	must(t, err)
	if len(cTags) != 1 || values(cTags)["k1"] != "changed" {
		t.Fatalf("c should lose only k2, got %+v", cTags)
	}
	dTags, err = f.EffectiveTags("d")
	must(t, err)
	if values(dTags)["k2"] != "v2" {
		t.Fatalf("d k2 must remain inherited, got %+v", dTags)
	}

	if f.Generation() <= genBefore {
		t.Fatalf("generation did not advance: %d", f.Generation())
	}

	for _, id := range []netsec.NodeID{"root", "a", "b", "c", "d", "other", "leaf"} {
		if ok, err := f.VerifyProvenance(id); err != nil || !ok {
			t.Fatalf("provenance mismatch on %s: %v, %v", id, ok, err)
		}
	}
}

func TestMoveRebuildsInheritanceWithoutPromotingCache(t *testing.T) {
	f := buildDeepTree(t)
	must(t, f.SetTag("a", "tier", "gold"))
	must(t, f.SetTag("d", "tier", "silver"))

	// c inherits gold from a before the move.
	tags, err := f.EffectiveTags("c")
	must(t, err)
	if values(tags)["tier"] != "gold" {
		t.Fatalf("pre-move c = %+v", tags)
	}

	txn, err := f.BeginMove("c", "d")
	must(t, err)

	// Before commit the forest is unchanged.
	parent, err := f.Parent("c")
	must(t, err)
	if parent != "b" {
		t.Fatalf("forest changed before commit: parent = %q", parent)
	}

	must(t, txn.Commit())

	parent, err = f.Parent("c")
	must(t, err)
	if parent != "d" {
		t.Fatalf("post-commit parent = %q, want d", parent)
	}
	path, err := f.Path("c")
	must(t, err)
	wantPath := []netsec.NodeID{"root", "d", "c"}
	if len(path) != len(wantPath) {
		t.Fatalf("path = %v, want %v", path, wantPath)
	}
	for i := range wantPath {
		if path[i] != wantPath[i] {
			t.Fatalf("path = %v, want %v", path, wantPath)
		}
	}

	// Old inherited gold must not become an explicit tag; new ancestry wins.
	tags, err = f.EffectiveTags("c")
	must(t, err)
	if values(tags)["tier"] != "silver" || tags[0].Explicit {
		t.Fatalf("post-move c = %+v", tags)
	}
	explicit, err := f.ExplicitTags("c")
	must(t, err)
	if len(explicit) != 0 {
		t.Fatalf("moved node gained explicit tags: %+v", explicit)
	}
	entry := inheritedByKey(mustInherited(t, f, "c"))["tier"]
	if entry.Source != "d" || entry.Value != "silver" {
		t.Fatalf("post-move provenance = %+v", entry)
	}

	if ok, err := f.VerifyProvenance("c"); err != nil || !ok {
		t.Fatalf("post-move verify = %v, %v", ok, err)
	}

	// A cut-off marker on the new ancestor keeps blocking the moved subtree.
	// Move c away first, block d, then move it back under the blocked ancestor.
	away, err := f.BeginMove("c", "root")
	must(t, err)
	must(t, away.Commit())
	must(t, f.DeleteTag("d", "tier"))
	must(t, f.BlockInheritance("d", "tier"))
	back, err := f.BeginMove("c", "d")
	must(t, err)
	must(t, back.Commit())
	if tags, err := f.EffectiveTags("c"); err != nil || len(tags) != 0 {
		t.Fatalf("blocked new ancestor must stop inheritance, got %+v", tags)
	}
	blockedEntry := inheritedByKey(mustInherited(t, f, "c"))["tier"]
	if !blockedEntry.Blocked || blockedEntry.Source != "d" {
		t.Fatalf("expected blocked provenance from d, got %+v", blockedEntry)
	}
	if ok, err := f.VerifyProvenance("c"); err != nil || !ok {
		t.Fatalf("blocked post-move verify = %v, %v", ok, err)
	}
}

func TestMoveToRootAndRollback(t *testing.T) {
	f := buildDeepTree(t)
	must(t, f.SetTag("root", "zone", "cn"))

	// A rolled-back transaction leaves topology and provenance untouched.
	txn, err := f.BeginMove("b", "")
	must(t, err)
	must(t, txn.Rollback())
	assertErrIs(t, txn.Rollback(), netsec.ErrTxnClosed)
	assertErrIs(t, txn.Commit(), netsec.ErrTxnClosed)

	parent, err := f.Parent("b")
	must(t, err)
	if parent != "a" {
		t.Fatalf("rollback parent = %q, want a", parent)
	}
	if tags, err := f.EffectiveTags("b"); err != nil || values(tags)["zone"] != "cn" {
		t.Fatalf("rollback must keep inheritance, got %+v", tags)
	}
	entry := inheritedByKey(mustInherited(t, f, "b"))["zone"]
	wantPath := []netsec.NodeID{"b", "a", "root"}
	if len(entry.Path) != len(wantPath) {
		t.Fatalf("rollback provenance path = %v, want %v", entry.Path, wantPath)
	}
	for i := range wantPath {
		if entry.Path[i] != wantPath[i] {
			t.Fatalf("rollback provenance path = %v, want %v", entry.Path, wantPath)
		}
	}

	// Cycle prevention: cannot move a node into its own subtree.
	_, err = f.BeginMove("a", "c")
	assertErrIs(t, err, netsec.ErrCycle)
	_, err = f.BeginMove("a", "a")
	assertErrIs(t, err, netsec.ErrCycle)

	// A committed detach turns the subtree root into a forest root and drops
	// inheritance from the old ancestry.
	commit, err := f.BeginMove("b", "")
	must(t, err)
	must(t, commit.Commit())
	roots, err := f.Children("")
	must(t, err)
	found := false
	for _, root := range roots {
		if root == "b" {
			found = true
		}
	}
	if !found {
		t.Fatalf("b should be a root after commit, roots = %v", roots)
	}
	if tags, err := f.EffectiveTags("b"); err != nil || len(tags) != 0 {
		t.Fatalf("detached root must lose inheritance, got %+v", tags)
	}
}

func TestSnapshotKeepsOldPathInterpretation(t *testing.T) {
	f := buildDeepTree(t)
	must(t, f.SetTag("a", "team", "sec"))

	snap := f.Snapshot()
	snapGeneration := snap.Generation()

	oldPath, err := snap.Path("c")
	must(t, err)
	wantOld := []netsec.NodeID{"root", "a", "b", "c"}
	if len(oldPath) != len(wantOld) {
		t.Fatalf("snapshot path = %v, want %v", oldPath, wantOld)
	}
	for i := range wantOld {
		if oldPath[i] != wantOld[i] {
			t.Fatalf("snapshot path = %v, want %v", oldPath, wantOld)
		}
	}

	snapTags, err := snap.EffectiveTags("c")
	must(t, err)
	if values(snapTags)["team"] != "sec" {
		t.Fatalf("snapshot c tags = %+v", snapTags)
	}

	// Mutate and move the live forest; the snapshot stays frozen.
	txn, err := f.BeginMove("c", "d")
	must(t, err)
	must(t, txn.Commit())
	must(t, f.DeleteTag("a", "team"))

	if snap.Generation() != snapGeneration {
		t.Fatalf("snapshot generation changed: %d", snap.Generation())
	}
	snapPath, err := snap.Path("c")
	must(t, err)
	for i := range wantOld {
		if snapPath[i] != wantOld[i] {
			t.Fatalf("snapshot path drifted after move: %v", snapPath)
		}
	}
	snapTags, err = snap.EffectiveTags("c")
	must(t, err)
	if values(snapTags)["team"] != "sec" {
		t.Fatalf("snapshot lost old resolution: %+v", snapTags)
	}
	if ok, err := snap.VerifyProvenance("c"); err != nil || !ok {
		t.Fatalf("snapshot verify = %v, %v", ok, err)
	}
	snapInherited, err := snap.InheritedTags("c")
	must(t, err)
	snapEntry := inheritedByKey(snapInherited)["team"]
	if snapEntry.Blocked || snapEntry.Source != "a" {
		t.Fatalf("snapshot provenance drifted: %+v", snapEntry)
	}

	// A second snapshot taken after the move proves provenance is evaluated
	// against each snapshot's own frozen topology.
	liveSnap := f.Snapshot()
	if ok, err := liveSnap.VerifyProvenance("c"); err != nil || !ok {
		t.Fatalf("live snapshot verify = %v, %v", ok, err)
	}
	wantLive := []netsec.NodeID{"root", "d", "c"}
	liveSnapPath, err := liveSnap.Path("c")
	must(t, err)
	if len(liveSnapPath) != len(wantLive) {
		t.Fatalf("live snapshot path = %v, want %v", liveSnapPath, wantLive)
	}

	// Live forest reflects the move and the deleted explicit tag.
	if tags, err := f.EffectiveTags("c"); err != nil || len(tags) != 0 {
		t.Fatalf("live c should have no team tag, got %+v", tags)
	}
	livePath, err := f.Path("c")
	must(t, err)
	if len(livePath) != len(wantLive) {
		t.Fatalf("live path = %v, want %v", livePath, wantLive)
	}
}

func TestMissingNodesAndDefensiveCopies(t *testing.T) {
	f := netsec.NewForest()
	must(t, f.AddNode("root", ""))
	must(t, f.SetTag("root", "k", "v"))

	if _, err := f.Parent("ghost"); !errors.Is(err, netsec.ErrNodeNotFound) {
		t.Fatalf("Parent ghost err = %v", err)
	}
	if _, err := f.EffectiveTags("ghost"); !errors.Is(err, netsec.ErrNodeNotFound) {
		t.Fatalf("EffectiveTags ghost err = %v", err)
	}
	if err := f.AddNode("root", ""); !errors.Is(err, netsec.ErrNodeExists) {
		t.Fatalf("duplicate AddNode err = %v", err)
	}
	if err := f.AddNode("child", "ghost"); !errors.Is(err, netsec.ErrNodeNotFound) {
		t.Fatalf("AddNode missing parent err = %v", err)
	}
	if _, err := f.BeginMove("ghost", ""); !errors.Is(err, netsec.ErrNodeNotFound) {
		t.Fatalf("BeginMove ghost err = %v", err)
	}

	snap := f.Snapshot()
	if _, err := snap.Path("ghost"); !errors.Is(err, netsec.ErrNodeNotFound) {
		t.Fatalf("snapshot Path ghost err = %v", err)
	}

	// Mutating returned slices must not corrupt internal state.
	path, err := f.Path("root")
	must(t, err)
	path[0] = "tampered"
	entries, err := f.InheritedTags("root")
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("root should have no inherited entries, got %+v", entries)
	}

	roots, err := f.Children("")
	must(t, err)
	if len(roots) != 1 || roots[0] != "root" {
		t.Fatalf("forest roots = %v", roots)
	}
}

func TestMultiRootIndependentInheritance(t *testing.T) {
	f := netsec.NewForest()
	must(t, f.AddNode("r1", ""))
	must(t, f.AddNode("r2", ""))
	must(t, f.AddNode("x", "r1"))
	must(t, f.AddNode("y", "r2"))
	must(t, f.SetTag("r1", "color", "red"))

	if tags, err := f.EffectiveTags("x"); err != nil || values(tags)["color"] != "red" {
		t.Fatalf("x tags = %+v", tags)
	}
	if tags, err := f.EffectiveTags("r2"); err != nil || len(tags) != 0 {
		t.Fatalf("r2 must not inherit across roots, got %+v", tags)
	}
	if tags, err := f.EffectiveTags("y"); err != nil || len(tags) != 0 {
		t.Fatalf("y must not inherit across roots, got %+v", tags)
	}

	must(t, f.BlockInheritance("r2", "color"))
	txn, err := f.BeginMove("x", "r2")
	must(t, err)
	must(t, txn.Commit())
	if tags, err := f.EffectiveTags("x"); err != nil || len(tags) != 0 {
		t.Fatalf("x moved under blocked root must lose color, got %+v", tags)
	}
	entry := inheritedByKey(mustInherited(t, f, "x"))["color"]
	if !entry.Blocked {
		t.Fatalf("expected blocked provenance, got %+v", entry)
	}
	if ok, err := f.VerifyProvenance("x"); err != nil || !ok {
		t.Fatalf("verify x = %v, %v", ok, err)
	}
}

func TestSiblingBranchesDoNotShareCache(t *testing.T) {
	f := netsec.NewForest()
	must(t, f.AddNode("root", ""))
	must(t, f.AddNode("left", "root"))
	must(t, f.AddNode("right", "root"))
	must(t, f.AddNode("l1", "left"))
	must(t, f.AddNode("r1", "right"))
	must(t, f.SetTag("root", "k", "v"))

	// Blocking one sibling subtree must not mutate the other branch.
	must(t, f.BlockInheritance("left", "k"))

	if tags, err := f.EffectiveTags("l1"); err != nil || len(tags) != 0 {
		t.Fatalf("l1 must be blocked, got %+v", tags)
	}
	if tags, err := f.EffectiveTags("r1"); err != nil || values(tags)["k"] != "v" {
		t.Fatalf("r1 must still inherit root value, got %+v", tags)
	}

	leftEntry := inheritedByKey(mustInherited(t, f, "l1"))["k"]
	rightEntry := inheritedByKey(mustInherited(t, f, "r1"))["k"]
	if !leftEntry.Blocked || leftEntry.Source != "left" {
		t.Fatalf("l1 provenance = %+v", leftEntry)
	}
	if rightEntry.Blocked || rightEntry.Source != "root" {
		t.Fatalf("r1 provenance = %+v", rightEntry)
	}

	// Explicit override on the blocked node reopens only its own branch.
	must(t, f.SetTag("left", "k", "branch"))
	if tags, err := f.EffectiveTags("l1"); err != nil || values(tags)["k"] != "branch" {
		t.Fatalf("l1 should inherit branch override, got %+v", tags)
	}
	if tags, err := f.EffectiveTags("r1"); err != nil || values(tags)["k"] != "v" {
		t.Fatalf("r1 must remain on root value, got %+v", tags)
	}

	for _, id := range []netsec.NodeID{"root", "left", "right", "l1", "r1"} {
		if ok, err := f.VerifyProvenance(id); err != nil || !ok {
			t.Fatalf("verify %s = %v, %v", id, ok, err)
		}
	}
}
