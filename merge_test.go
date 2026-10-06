package layermerge

import (
	"strings"
	"testing"
)

func child(t *testing.T, dir *Node, name string) *Node {
	t.Helper()
	n, ok := dir.Children[name]
	if !ok {
		t.Fatalf("expected child %q under %q", name, dir.Name)
	}
	return n
}

func TestWhiteoutRemovesLowerNode(t *testing.T) {
	lower := MustLayer("lower",
		File("keep", "k"),
		File("drop", "d"),
		Dir("gone", File("inner", "i")),
	)
	upper := MustLayer("upper",
		Whiteout("drop"),
		Whiteout("gone"),
	)
	root, err := Merge(lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := root.Children["drop"]; ok {
		t.Error("whiteout did not remove lower file")
	}
	if _, ok := root.Children["gone"]; ok {
		t.Error("whiteout did not remove lower dir subtree")
	}
	if _, ok := root.Children[WhiteoutPrefix+"drop"]; ok {
		t.Error("whiteout entry leaked into merged tree")
	}
	if got := child(t, root, "keep"); got.Layer != 0 {
		t.Errorf("keep: layer = %d, want 0", got.Layer)
	}
}

func TestOpaqueDirHidesLowerChildren(t *testing.T) {
	lower := MustLayer("lower",
		Dir("d", File("x", "1"), File("y", "2")),
	)
	upper := MustLayer("upper",
		Dir("d", Opaque(), File("z", "3")),
	)
	root, err := Merge(lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	d := child(t, root, "d")
	if len(d.Children) != 1 {
		t.Fatalf("opaque dir has %d children, want 1: %v", len(d.Children), d.Children)
	}
	z := child(t, d, "z")
	if z.Layer != 1 {
		t.Errorf("z: layer = %d, want 1", z.Layer)
	}
	if _, ok := d.Children[OpaqueMarker]; ok {
		t.Error("opaque marker leaked into merged tree")
	}
}

func TestNonOpaqueDirMergesWithLower(t *testing.T) {
	lower := MustLayer("lower", Dir("d", File("x", "1")))
	upper := MustLayer("upper", Dir("d", File("y", "2")))
	root, err := Merge(lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	d := child(t, root, "d")
	if len(d.Children) != 2 {
		t.Fatalf("merged dir has %d children, want 2", len(d.Children))
	}
	if d.Children["x"].Layer != 0 || d.Children["y"].Layer != 1 {
		t.Errorf("provenance wrong: x=%d y=%d", d.Children["x"].Layer, d.Children["y"].Layer)
	}
}

func TestTypeReplacement(t *testing.T) {
	lower := MustLayer("lower",
		Dir("a", File("inner", "i")),
		File("b", "file"),
	)
	upper := MustLayer("upper",
		File("a", "now-a-file"),
		Dir("b", File("inner", "i")),
	)
	root, err := Merge(lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	a := child(t, root, "a")
	if a.Kind != KindFile || a.Content != "now-a-file" || a.Layer != 1 {
		t.Errorf("dir->file replacement failed: %+v", a)
	}
	b := child(t, root, "b")
	if b.Kind != KindDir || b.Layer != 1 {
		t.Errorf("file->dir replacement failed: %+v", b)
	}
	if _, ok := b.Children["inner"]; !ok {
		t.Error("upper dir children lost in file->dir replacement")
	}
}

func TestHardlinkResolvesWithinLayerOnly(t *testing.T) {
	lower := MustLayer("lower", File("cfg", "old"))
	upper := MustLayer("upper",
		File("cfg", "new"),
		Hardlink("alias", "/cfg"),
	)
	root, err := Merge(lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	alias := child(t, root, "alias")
	if alias.Resolved == nil {
		t.Fatal("hardlink not resolved")
	}
	if alias.Resolved.Content != "new" {
		t.Errorf("hardlink resolved to %q, want same-layer content %q", alias.Resolved.Content, "new")
	}
}

func TestHardlinkDoesNotEscapeToLowerLayer(t *testing.T) {
	_ = MustLayer("lower", File("secret", "s"))
	if _, err := NewLayer("upper", Hardlink("h", "/secret")); err == nil {
		t.Fatal("expected dangling hardlink error: target only exists in lower layer")
	}
}

func TestExpandShowsLayerByLayerStates(t *testing.T) {
	l0 := MustLayer("l0", File("a", "0"), Dir("d", File("x", "x")))
	l1 := MustLayer("l1", Whiteout("a"), Dir("d", File("y", "y")))
	l2 := MustLayer("l2", Dir("d", Opaque(), File("z", "z")))

	snaps, err := Expand(l0, l1, l2)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 3 {
		t.Fatalf("got %d snapshots, want 3", len(snaps))
	}
	if _, ok := snaps[0].Root.Children["a"]; !ok {
		t.Error("step 0: a missing")
	}
	if _, ok := snaps[1].Root.Children["a"]; ok {
		t.Error("step 1: whiteout should have removed a")
	}
	d1 := child(t, snaps[1].Root, "d")
	if len(d1.Children) != 2 {
		t.Errorf("step 1: d has %d children, want 2 (merged)", len(d1.Children))
	}
	d2 := child(t, snaps[2].Root, "d")
	if len(d2.Children) != 1 || d2.Children["z"] == nil {
		t.Errorf("step 2: opaque d should contain only z, got %v", d2.Children)
	}
}

func TestDumpAnnotatesProvenance(t *testing.T) {
	lower := MustLayer("lower", File("a", "0"))
	upper := MustLayer("upper", File("b", "1"), Hardlink("c", "/b"))
	root, err := Merge(lower, upper)
	if err != nil {
		t.Fatal(err)
	}
	out := root.Dump()
	for _, want := range []string{
		"a (file) [layer 0]",
		"b (file) [layer 1]",
		"c (hardlink -> /b => file) [layer 1]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %q:\n%s", want, out)
		}
	}
}
