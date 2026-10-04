package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildArchive(t *testing.T, entries ...*entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := writeArchive(&buf, entries); err != nil {
		t.Fatalf("writeArchive: %v", err)
	}
	return buf.Bytes()
}

// unpackBytes extracts an in-memory archive into base/root and returns
// the root path plus the error.
func unpackBytes(t *testing.T, base string, data []byte) (string, error) {
	t.Helper()
	entries, err := readArchive(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("readArchive: %v", err)
	}
	root := filepath.Join(base, "root")
	return root, extractArchive(entries, root)
}

// stagingLeftovers returns names in dir that look like leftover staging
// directories for root.
func stagingLeftovers(t *testing.T, root string) []string {
	t.Helper()
	dir, pattern := stagingFor(root)
	prefix := strings.TrimSuffix(pattern, "-")
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var out []string
	for _, de := range des {
		if strings.HasPrefix(de.Name(), prefix) {
			out = append(out, de.Name())
		}
	}
	return out
}

func fileEntry(p, data string) *entry {
	return &entry{typ: entFile, path: p, mode: 0o644, data: []byte(data)}
}

func TestExtractBasic(t *testing.T) {
	base := t.TempDir()
	data := buildArchive(t,
		&entry{typ: entDir, path: "sub"},
		fileEntry("sub/hello.txt", "hello"),
		fileEntry("top.txt", "top"),
		&entry{typ: entSymlink, path: "link", target: "sub/hello.txt"},
		&entry{typ: entHardlink, path: "hard.txt", target: "top.txt"},
	)
	root, err := unpackBytes(t, base, data)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "sub", "hello.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("file content = %q, %v", got, err)
	}
	target, err := os.Readlink(filepath.Join(root, "link"))
	if err != nil || target != "sub/hello.txt" {
		t.Fatalf("symlink = %q, %v", target, err)
	}
	got, err = os.ReadFile(filepath.Join(root, "hard.txt"))
	if err != nil || string(got) != "top" {
		t.Fatalf("hardlink content = %q, %v", got, err)
	}
	fi1, _ := os.Stat(filepath.Join(root, "top.txt"))
	fi2, _ := os.Stat(filepath.Join(root, "hard.txt"))
	if !os.SameFile(fi1, fi2) {
		t.Fatalf("hardlink does not share inode")
	}
	if left := stagingLeftovers(t, root); len(left) != 0 {
		t.Fatalf("staging leftovers: %v", left)
	}
}

func TestRejectAbsolutePath(t *testing.T) {
	base := t.TempDir()
	data := buildArchive(t, fileEntry("/etc/pwned", "x"))
	if _, err := unpackBytes(t, base, data); err == nil {
		t.Fatal("expected error for absolute path")
	}
}

func TestRejectEscapes(t *testing.T) {
	for _, p := range []string{"../evil", "a/../../evil", "..", "a/../../../.."} {
		base := t.TempDir()
		data := buildArchive(t, fileEntry(p, "x"))
		if _, err := unpackBytes(t, base, data); err == nil {
			t.Fatalf("expected error for escaping path %q", p)
		}
	}
}

func TestRejectTypeConflict(t *testing.T) {
	cases := [][]*entry{
		{{typ: entDir, path: "x"}, fileEntry("x", "x")},
		{fileEntry("x", "x"), {typ: entSymlink, path: "x", target: "y"}},
		{fileEntry("x", "x"), {typ: entDir, path: "x"}},
		{fileEntry("f", "x"), {typ: entDir, path: "x"}, {typ: entHardlink, path: "x", target: "f"}},
		// conflict through symlink resolution: dir "s" resolves to file "f"
		{fileEntry("f", "x"), {typ: entSymlink, path: "s", target: "f"}, {typ: entDir, path: "s"}},
	}
	for i, entries := range cases {
		base := t.TempDir()
		if _, err := unpackBytes(t, base, buildArchive(t, entries...)); err == nil {
			t.Fatalf("case %d: expected type conflict error", i)
		}
	}
}

func TestAllowSameTypeDuplicates(t *testing.T) {
	base := t.TempDir()
	data := buildArchive(t,
		&entry{typ: entDir, path: "d"},
		&entry{typ: entDir, path: "d"},
		fileEntry("f", "old"),
		fileEntry("f", "new"),
	)
	root, err := unpackBytes(t, base, data)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f"))
	if string(got) != "new" {
		t.Fatalf("overwrite content = %q", got)
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	cases := [][]*entry{
		// symlink target escapes root directly
		{{typ: entSymlink, path: "a", target: ".."}},
		{{typ: entSymlink, path: "d/a", target: "../.."}},
		// absolute symlink target
		{{typ: entSymlink, path: "a", target: "/etc"}},
	}
	for i, entries := range cases {
		base := t.TempDir()
		if _, err := unpackBytes(t, base, buildArchive(t, entries...)); err == nil {
			t.Fatalf("case %d: expected symlink escape error", i)
		}
	}
}

// TestLinkThenWriteBlocked: a symlink is created first, then a file is
// written through it. In-root redirection must work; anything escaping
// must be rejected and leave nothing behind.
func TestLinkThenWriteBlocked(t *testing.T) {
	// in-root redirection is fine
	base := t.TempDir()
	data := buildArchive(t,
		&entry{typ: entSymlink, path: "a", target: "b"},
		fileEntry("a/x.txt", "via-link"),
	)
	root, err := unpackBytes(t, base, data)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "b", "x.txt"))
	if err != nil || string(got) != "via-link" {
		t.Fatalf("redirected file = %q, %v", got, err)
	}

	// escape attempt: chained symlink hops out of the root
	base = t.TempDir()
	outside := filepath.Join(base, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	data = buildArchive(t,
		&entry{typ: entSymlink, path: "sneaky", target: "../outside"},
		fileEntry("sneaky/evil.txt", "pwned"),
	)
	root, err = unpackBytes(t, base, data)
	if err == nil {
		t.Fatal("expected escape via symlink to be rejected")
	}
	if _, statErr := os.Lstat(filepath.Join(outside, "evil.txt")); !os.IsNotExist(statErr) {
		t.Fatal("file escaped the extraction root")
	}
	if left := stagingLeftovers(t, root); len(left) != 0 {
		t.Fatalf("staging leftovers: %v", left)
	}
}

func TestSymlinkRing(t *testing.T) {
	cases := [][]*entry{
		// two-node ring, then a write through it
		{
			{typ: entSymlink, path: "a", target: "b"},
			{typ: entSymlink, path: "b", target: "a"},
			fileEntry("a/x", "x"),
		},
		// self loop
		{
			{typ: entSymlink, path: "s", target: "s"},
			fileEntry("s/y", "y"),
		},
	}
	for i, entries := range cases {
		base := t.TempDir()
		root, err := unpackBytes(t, base, buildArchive(t, entries...))
		if err == nil {
			t.Fatalf("case %d: expected symlink loop error", i)
		}
		if left := stagingLeftovers(t, root); len(left) != 0 {
			t.Fatalf("case %d: staging leftovers: %v", i, left)
		}
	}
}

func TestHardlinkRequiresVerifiedInternal(t *testing.T) {
	cases := []struct {
		name    string
		entries []*entry
		wantErr bool
	}{
		{"valid", []*entry{fileEntry("f", "data"), {typ: entHardlink, path: "g", target: "f"}}, false},
		{"missing target", []*entry{{typ: entHardlink, path: "g", target: "nope"}}, true},
		{"target declared later", []*entry{{typ: entHardlink, path: "g", target: "f"}, fileEntry("f", "data")}, true},
		{"target is dir", []*entry{{typ: entDir, path: "d"}, {typ: entHardlink, path: "g", target: "d"}}, true},
		{"target escapes", []*entry{fileEntry("f", "data"), {typ: entHardlink, path: "g", target: "../f"}}, true},
		{"target absolute", []*entry{fileEntry("f", "data"), {typ: entHardlink, path: "g", target: "/etc/passwd"}}, true},
		{"via symlink", []*entry{fileEntry("f", "data"), {typ: entSymlink, path: "s", target: "f"}, {typ: entHardlink, path: "g", target: "s"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			root, err := unpackBytes(t, base, buildArchive(t, tc.entries...))
			if tc.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("extract: %v", err)
				}
				got, rerr := os.ReadFile(filepath.Join(root, "g"))
				if rerr != nil || string(got) != "data" {
					t.Fatalf("hardlink content = %q, %v", got, rerr)
				}
			}
		})
	}
}

func TestFileAsParentRejected(t *testing.T) {
	base := t.TempDir()
	data := buildArchive(t,
		fileEntry("f", "x"),
		fileEntry("f/g", "y"),
	)
	if _, err := unpackBytes(t, base, data); err == nil {
		t.Fatal("expected not-a-directory error")
	}
}

// TestStagingSamePrefix verifies the staging directory is a sibling of
// the destination sharing its name as prefix (same filesystem, atomic
// rename).
func TestStagingSamePrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	dir, pattern := stagingFor(root)
	if dir != filepath.Dir(root) {
		t.Fatalf("staging dir %q is not the destination parent %q", dir, filepath.Dir(root))
	}
	if !strings.HasPrefix(pattern, "."+filepath.Base(root)) {
		t.Fatalf("staging pattern %q does not share the destination name prefix", pattern)
	}
}

// TestFailureCleansStaging: a mid-archive validation failure and a
// rename-time failure must both leave no staging directories behind.
func TestFailureCleansStaging(t *testing.T) {
	// validation failure after some valid entries
	base := t.TempDir()
	data := buildArchive(t,
		&entry{typ: entDir, path: "ok"},
		fileEntry("ok/f", "x"),
		fileEntry("../evil", "x"),
	)
	root, err := unpackBytes(t, base, data)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if left := stagingLeftovers(t, root); len(left) != 0 {
		t.Fatalf("staging leftovers after validation failure: %v", left)
	}

	// rename failure: destination already exists as a file
	base = t.TempDir()
	root = filepath.Join(base, "root")
	if err := os.WriteFile(root, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := readArchive(bytes.NewReader(buildArchive(t, fileEntry("f", "x"))))
	if err != nil {
		t.Fatal(err)
	}
	if err := extractArchive(entries, root); err == nil {
		t.Fatal("expected destination-exists error")
	}
	if left := stagingLeftovers(t, root); len(left) != 0 {
		t.Fatalf("staging leftovers after rename failure: %v", left)
	}
}

// TestRoundTripCLI exercises pack + unpack through the real commands.
func TestRoundTripCLI(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("aaa"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("sub/a.txt", filepath.Join(src, "ln")); err != nil {
		t.Fatal(err)
	}
	arc := filepath.Join(base, "out.unarc")
	if err := packDir(src, arc); err != nil {
		t.Fatalf("pack: %v", err)
	}
	dst := filepath.Join(base, "dst")
	if err := extract(arc, dst); err != nil {
		t.Fatalf("unpack: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "sub", "a.txt"))
	if err != nil || string(got) != "aaa" {
		t.Fatalf("roundtrip content = %q, %v", got, err)
	}
	target, err := os.Readlink(filepath.Join(dst, "ln"))
	if err != nil || target != "sub/a.txt" {
		t.Fatalf("roundtrip symlink = %q, %v", target, err)
	}
}
