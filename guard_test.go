package netsec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePathRejects(t *testing.T) {
	bad := []string{
		"../evil",
		"a/../../evil",
		"a/./b",
		"a//b",
		"/etc/passwd",
		`C:\windows\system32`,
		`C:/windows/system32`,
		`\\server\share`,
		`a\b`,
		".git",
		".GIT",
		".Git",
		".git/config",
		"sub/.GiT/config",
		".git.",
		".git ",
		".git. .",
		"GIT~1",
		"git~2",
		"dir/git~1",
		"",
	}
	for _, p := range bad {
		if err := ValidatePath(p); err == nil {
			t.Errorf("ValidatePath(%q) = nil, want error", p)
		}
	}
}

func TestValidatePathAccepts(t *testing.T) {
	good := []string{
		"a",
		"a/b/c",
		".github/workflows/ci.yml",
		".gitignore",
		"git~x",
		"foo..bar",
		"a..b/c",
	}
	for _, p := range good {
		if err := ValidatePath(p); err != nil {
			t.Errorf("ValidatePath(%q) = %v, want nil", p, err)
		}
	}
}

func TestValidateNameRejectsGitAliases(t *testing.T) {
	for _, n := range []string{".git", ".GIT", ".gIt", ".git.", ".git ", "GIT~1", "a/b", `a\b`, "..", ""} {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", n)
		}
	}
}

func fileEntry(path string) Entry {
	base := path
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			base = path[i+1:]
			break
		}
	}
	return Entry{Path: path, Name: base, Type: EntryFile, Data: []byte("x")}
}

func TestValidateEntriesSymlinkParent(t *testing.T) {
	entries := []Entry{
		{Path: "link", Name: "link", Type: EntrySymlink, Target: "/outside"},
		fileEntry("link/evil.txt"),
	}
	if err := ValidateEntries(entries); err == nil {
		t.Fatal("ValidateEntries accepted entry with symlink parent")
	}
}

func TestValidateEntriesDeepSymlinkParent(t *testing.T) {
	entries := []Entry{
		{Path: "a", Name: "a", Type: EntryDir},
		{Path: "a/link", Name: "link", Type: EntrySymlink, Target: "../../outside"},
		fileEntry("a/link/deep/evil.txt"),
	}
	if err := ValidateEntries(entries); err == nil {
		t.Fatal("ValidateEntries accepted entry with deep symlink parent")
	}
}

func TestValidateEntriesCaseCollision(t *testing.T) {
	cases := [][]Entry{
		{fileEntry("Foo"), fileEntry("foo")},
		{fileEntry("README.md"), fileEntry("readme.md")},
		{fileEntry("dir/A.txt"), fileEntry("DIR/a.txt")},
		{fileEntry("a/Name."), fileEntry("a/name")},
	}
	for i, entries := range cases {
		if err := ValidateEntries(entries); err == nil {
			t.Errorf("case %d: ValidateEntries accepted colliding entries", i)
		}
	}
}

func TestValidateEntriesOK(t *testing.T) {
	entries := []Entry{
		{Path: "dir", Name: "dir", Type: EntryDir},
		fileEntry("dir/file.txt"),
		fileEntry("other.txt"),
	}
	if err := ValidateEntries(entries); err != nil {
		t.Fatalf("ValidateEntries = %v, want nil", err)
	}
}

func TestValidateURL(t *testing.T) {
	bad := []string{
		"-upload-pack=touch /tmp/pwned",
		"--upload-pack=touch pwned",
		"ext::sh -c touch%20pwned",
		"EXT::sh -c id",
		"",
	}
	for _, u := range bad {
		if err := ValidateURL(u); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error", u)
		}
	}
	good := []string{
		"https://example.org/repo.git",
		"git@example.org:repo.git",
		"../relative/repo.git",
	}
	for _, u := range good {
		if err := ValidateURL(u); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", u, err)
		}
	}
}

// assertEmptyDir fails the test if dir contains anything at all.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	found, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("rejected checkout created %q", filepath.Join(dir, f.Name()))
	}
}

func TestCheckoutRejectsSymlinkParentWithoutCreatingFiles(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{
		{Path: "link", Name: "link", Type: EntrySymlink, Target: "/outside"},
		fileEntry("link/evil.txt"),
	}
	if err := Checkout(root, entries, nil); err == nil {
		t.Fatal("Checkout accepted symlink parent")
	}
	assertEmptyDir(t, root)
}

func TestCheckoutRejectsGitAliasesWithoutCreatingFiles(t *testing.T) {
	for _, path := range []string{".GIT/config", ".git./config", "git~1/config", "sub/.Git/config"} {
		root := t.TempDir()
		var entries []Entry
		if path[:4] == "sub/" {
			entries = append(entries, Entry{Path: "sub", Name: "sub", Type: EntryDir})
		}
		entries = append(entries, fileEntry(path))
		if err := Checkout(root, entries, nil); err == nil {
			t.Errorf("Checkout accepted .git alias path %q", path)
		}
		assertEmptyDir(t, root)
	}
}

func TestCheckoutRejectsCollisionWithoutCreatingFiles(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{fileEntry("Foo.txt"), fileEntry("foo.TXT")}
	if err := Checkout(root, entries, nil); err == nil {
		t.Fatal("Checkout accepted case-colliding entries")
	}
	assertEmptyDir(t, root)
}

func TestCheckoutRejectsDotDotAndAbsoluteWithoutCreatingFiles(t *testing.T) {
	for _, path := range []string{"../escape.txt", "a/../../escape.txt", "/abs.txt"} {
		root := t.TempDir()
		if err := Checkout(root, []Entry{fileEntry(path)}, nil); err == nil {
			t.Errorf("Checkout accepted path %q", path)
		}
		assertEmptyDir(t, root)
	}
}

func TestCheckoutRejectsMaliciousSubmoduleURLWithoutCreatingFiles(t *testing.T) {
	for _, url := range []string{"-upload-pack=touch pwned", "ext::sh -c touch%20pwned"} {
		root := t.TempDir()
		subs := []Submodule{{Name: "sub", Path: "sub", URL: url}}
		if err := Checkout(root, nil, subs); err == nil {
			t.Errorf("Checkout accepted submodule url %q", url)
		}
		assertEmptyDir(t, root)
	}
}

func TestCheckoutRejectsSubmoduleUnderSymlink(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{
		{Path: "link", Name: "link", Type: EntrySymlink, Target: "/outside"},
	}
	subs := []Submodule{{Name: "sub", Path: "link/sub", URL: "https://example.org/r.git"}}
	if err := Checkout(root, entries, subs); err == nil {
		t.Fatal("Checkout accepted submodule path under symlink parent")
	}
	assertEmptyDir(t, root)
}

func TestCheckoutRejectsSymlinkAlreadyOnDisk(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{fileEntry("link/evil.txt")}
	if err := Checkout(root, entries, nil); err == nil {
		t.Fatal("Checkout accepted entry under on-disk symlink")
	}
	if _, err := os.Lstat(filepath.Join(outside, "evil.txt")); !os.IsNotExist(err) {
		t.Error("rejected checkout wrote through symlink to outside directory")
	}
	found, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name() != "link" {
		t.Errorf("rejected checkout modified root, now contains %v", found)
	}
}

func TestCheckoutValidTree(t *testing.T) {
	root := t.TempDir()
	entries := []Entry{
		{Path: "dir", Name: "dir", Type: EntryDir},
		{Path: "dir/file.txt", Name: "file.txt", Type: EntryFile, Data: []byte("hello")},
	}
	subs := []Submodule{{Name: "sub", Path: "sub", URL: "https://example.org/repo.git"}}
	if err := Checkout(root, entries, subs); err != nil {
		t.Fatalf("Checkout = %v, want nil", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "dir", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("file content = %q, want %q", data, "hello")
	}
	if info, err := os.Stat(filepath.Join(root, "sub")); err != nil || !info.IsDir() {
		t.Errorf("submodule dir not created: %v", err)
	}
}
