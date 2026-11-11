package netsec

import (
	"errors"
	"testing"
)

func mustAssemble(t *testing.T, entries ...Entry) *View {
	t.Helper()
	v, err := Assemble(Manifest{Entries: entries})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return v
}

func mustDigest(t *testing.T, v *View, path, want string) {
	t.Helper()
	got, ok := v.Lookup(path)
	if !ok {
		t.Fatalf("view missing %q", path)
	}
	if got != want {
		t.Fatalf("view[%q] = %q, want %q", path, got, want)
	}
}

func mustAbsent(t *testing.T, v *View, path string) {
	t.Helper()
	if d, ok := v.Lookup(path); ok {
		t.Fatalf("view unexpectedly has %q -> %q", path, d)
	}
}

func mustFail(t *testing.T, want error, entries ...Entry) {
	t.Helper()
	_, err := Assemble(Manifest{Entries: entries})
	if !errors.Is(err, want) {
		t.Fatalf("Assemble error = %v, want %v", err, want)
	}
}

func TestAliasChain(t *testing.T) {
	v := mustAssemble(t,
		Entry{Path: "lib/real.txt", Kind: KindFile, Digest: "sha256:real"},
		Entry{Path: "one", Kind: KindLink, Target: "two"},
		Entry{Path: "two", Kind: KindLink, Target: "three"},
		Entry{Path: "three", Kind: KindLink, Target: "lib/real.txt"},
		Entry{Path: "sub/rel", Kind: KindLink, Target: "../lib/real.txt"},
	)
	for _, p := range []string{"one", "two", "three", "sub/rel"} {
		mustDigest(t, v, p, "sha256:real")
	}
	mustDigest(t, v, "lib/real.txt", "sha256:real")
	if v.Len() != 5 {
		t.Fatalf("view has %d files, want 5", v.Len())
	}
}

func TestSameNameDirReplacement(t *testing.T) {
	v := mustAssemble(t,
		Entry{Path: "vendor/lib/old.txt", Kind: KindFile, Digest: "sha256:old"},
		Entry{Path: "third_party/lib/new.txt", Kind: KindFile, Digest: "sha256:new"},
		Entry{Path: "vendor/lib", Kind: KindLink, Target: "../third_party/lib"},
	)
	mustDigest(t, v, "vendor/lib/new.txt", "sha256:new")
	mustAbsent(t, v, "vendor/lib/old.txt")
	mustDigest(t, v, "third_party/lib/new.txt", "sha256:new")
	if v.Len() != 2 {
		t.Fatalf("view has %d files, want 2", v.Len())
	}
}

func TestRootRelativeTarget(t *testing.T) {
	v := mustAssemble(t,
		Entry{Path: "bin/tools/x.sh", Kind: KindFile, Digest: "sha256:x"},
		Entry{Path: "tools", Kind: KindLink, Target: "/bin/tools"},
	)
	mustDigest(t, v, "tools/x.sh", "sha256:x")
	mustDigest(t, v, "bin/tools/x.sh", "sha256:x")
}

func TestMissingTarget(t *testing.T) {
	mustFail(t, ErrMissingTarget,
		Entry{Path: "broken", Kind: KindLink, Target: "nope"},
	)
	mustFail(t, ErrMissingTarget,
		Entry{Path: "a", Kind: KindLink, Target: "b"},
		Entry{Path: "b", Kind: KindLink, Target: "gone"},
	)
	// Absolute targets resolve inside the input root, never the host
	// filesystem, so this is a missing manifest object.
	mustFail(t, ErrMissingTarget,
		Entry{Path: "abs", Kind: KindLink, Target: "/etc/passwd"},
	)
}

func TestCrossRoot(t *testing.T) {
	mustFail(t, ErrCrossRoot,
		Entry{Path: "esc", Kind: KindLink, Target: "../outside"},
	)
	mustFail(t, ErrCrossRoot,
		Entry{Path: "d/esc", Kind: KindLink, Target: "../../outside"},
	)
}

func TestCycle(t *testing.T) {
	mustFail(t, ErrCycle,
		Entry{Path: "x", Kind: KindLink, Target: "y"},
		Entry{Path: "y", Kind: KindLink, Target: "x"},
	)
	mustFail(t, ErrCycle,
		Entry{Path: "s", Kind: KindLink, Target: "s"},
	)
	// A link that mounts an ancestor directory below itself.
	mustFail(t, ErrCycle,
		Entry{Path: "a/f.txt", Kind: KindFile, Digest: "sha256:f"},
		Entry{Path: "a/b", Kind: KindLink, Target: ".."},
	)
}

func TestMalformedAndConflict(t *testing.T) {
	mustFail(t, ErrMalformed,
		Entry{Path: "f.txt", Kind: KindFile},
	)
	mustFail(t, ErrMalformed,
		Entry{Path: "l", Kind: KindLink},
	)
	mustFail(t, ErrConflict,
		Entry{Path: "f.txt", Kind: KindFile, Digest: "sha256:a"},
		Entry{Path: "f.txt", Kind: KindFile, Digest: "sha256:b"},
	)
	mustFail(t, ErrConflict,
		Entry{Path: "p", Kind: KindFile, Digest: "sha256:a"},
		Entry{Path: "p", Kind: KindLink, Target: "q"},
	)
	mustFail(t, ErrInvalidPath,
		Entry{Path: "../f.txt", Kind: KindFile, Digest: "sha256:a"},
	)
	mustFail(t, ErrInvalidPath,
		Entry{Path: "/abs.txt", Kind: KindFile, Digest: "sha256:a"},
	)
}

func TestListSorted(t *testing.T) {
	v := mustAssemble(t,
		Entry{Path: "b.txt", Kind: KindFile, Digest: "sha256:b"},
		Entry{Path: "a.txt", Kind: KindFile, Digest: "sha256:a"},
	)
	list := v.List()
	if len(list) != 2 || list[0].Path != "a.txt" || list[1].Path != "b.txt" {
		t.Fatalf("List() = %v", list)
	}
}
