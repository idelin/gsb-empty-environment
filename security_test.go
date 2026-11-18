package netsec

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
	if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func makeTestSymlink(t *testing.T, target, name string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symbolic-link tests require Unix-like symlink semantics")
	}
	if err := os.Symlink(target, name); err != nil {
		t.Fatalf("symlink %s -> %s: %v", name, target, err)
	}
}

func assertNoRealPathLeak(t *testing.T, err error, roots ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	message := err.Error()
	for _, root := range roots {
		if root != "" && strings.Contains(message, root) {
			t.Fatalf("error leaks real path %q: %v", root, err)
		}
	}
}

func TestTranslationNormalizesAndOpens(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "docs", "a.tcl"), "puts ok\n")

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	for _, virtualPath := range []string{
		"$p(:0:)/docs/a.tcl",
		"$p(:0:)/docs/./a.tcl",
		"$p(:0:)/docs/../docs/a.tcl",
		"$p(:0:)//docs//a.tcl",
	} {
		got, err := translator.Translate(virtualPath)
		if err != nil {
			t.Fatalf("Translate(%q): %v", virtualPath, err)
		}
		want := filepath.Join(root, "docs", "a.tcl")
		if got != want {
			t.Fatalf("Translate(%q) = %q, want %q", virtualPath, got, want)
		}
	}

	file, err := translator.Open("$p(:0:)/docs/a.tcl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "puts ok\n" {
		t.Fatalf("unexpected content %q", content)
	}
}

func TestForgedAndEscapingTokensRejected(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "inside.tcl"), "ok")

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"forged id", "$p(:1:)/inside.tcl", ErrTokenOutOfRange},
		{"huge id", "$p(:999999999999999999999:)/inside.tcl", ErrTokenOutOfRange},
		{"negative id", "$p(:-1:)/inside.tcl", ErrInvalidToken},
		{"non-numeric id", "$p(:x:)/inside.tcl", ErrInvalidToken},
		{"missing separator", "$p(:0:)inside.tcl", ErrInvalidToken},
		{"parent traversal", "$p(:0:)/../inside.tcl", ErrPathEscape},
		{"nested parent traversal", "$p(:0:)/a/../../inside.tcl", ErrPathEscape},
		{"backslash traversal", `$p(:0:)/..\inside.tcl`, ErrPathEscape},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := translator.Translate(test.path)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Translate(%q) error = %v, want %v", test.path, err, test.wantErr)
			}
			assertNoRealPathLeak(t, err, root, filepath.Dir(root))

			_, err = translator.Open(test.path)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Open(%q) error = %v, want %v", test.path, err, test.wantErr)
			}
			assertNoRealPathLeak(t, err, root, filepath.Dir(root))
		})
	}
}

func TestSymlinkEscapeRejectedByAllOperations(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(root, "inside.tcl"), "inside")
	writeTestFile(t, filepath.Join(outside, "secret.tcl"), "outside-secret")

	link := filepath.Join(root, "link.tcl")
	dirLink := filepath.Join(root, "linkdir")
	makeTestSymlink(t, filepath.Join(outside, "secret.tcl"), link)
	makeTestSymlink(t, outside, dirLink)

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	if _, err := translator.Translate("$p(:0:)/link.tcl"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Translate symlink error = %v, want ErrSymlinkEscape", err)
	} else {
		assertNoRealPathLeak(t, err, root, outside)
	}
	if _, err := translator.Open("$p(:0:)/link.tcl"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Open symlink error = %v, want ErrSymlinkEscape", err)
	} else {
		assertNoRealPathLeak(t, err, root, outside)
	}
	if _, err := translator.Open("$p(:0:)/linkdir/secret.tcl"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Open symlinked directory error = %v, want ErrSymlinkEscape", err)
	} else {
		assertNoRealPathLeak(t, err, root, outside)
	}
	if _, err := translator.Source("$p(:0:)/link.tcl"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Source symlink error = %v, want ErrSymlinkEscape", err)
	} else {
		assertNoRealPathLeak(t, err, root, outside)
	}
	if _, err := translator.Tokenize(link); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Tokenize symlink error = %v, want ErrSymlinkEscape", err)
	} else {
		assertNoRealPathLeak(t, err, root, outside)
	}

	matches, err := translator.Glob("$p(:0:)/*.tcl")
	if !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Glob symlink error = %v, want ErrSymlinkEscape", err)
	}
	if matches != nil {
		t.Fatalf("Glob returned a partial batch: %#v", matches)
	}
	assertNoRealPathLeak(t, err, root, outside)
}

func TestInternalSymlinkRemainsAccessible(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "real.tcl"), "internal")
	makeTestSymlink(t, "real.tcl", filepath.Join(root, "link.tcl"))

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	resolved, err := translator.Translate("$p(:0:)/link.tcl")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(root, "real.tcl") {
		t.Fatalf("internal symlink translation = %q", resolved)
	}

	content, err := translator.Source("$p(:0:)/link.tcl")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "internal" {
		t.Fatalf("internal symlink source = %q", content)
	}
}

func TestDirectoryReplacementBeforeOpenCannotEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(root, "safe", "script.tcl"), "safe-content")
	writeTestFile(t, filepath.Join(outside, "script.tcl"), "outside-secret")

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	translated, err := translator.Translate("$p(:0:)/safe/script.tcl")
	if err != nil {
		t.Fatal(err)
	}
	if translated != filepath.Join(root, "safe", "script.tcl") {
		t.Fatalf("unexpected translation %q", translated)
	}

	if err := os.Rename(filepath.Join(root, "safe"), filepath.Join(root, "safe.original")); err != nil {
		t.Fatal(err)
	}
	makeTestSymlink(t, outside, filepath.Join(root, "safe"))

	if _, err := translator.Open("$p(:0:)/safe/script.tcl"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Open after directory replacement error = %v, want ErrSymlinkEscape", err)
	} else {
		assertNoRealPathLeak(t, err, root, outside)
	}
	if _, err := translator.Translate("$p(:0:)/safe/script.tcl"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Translate after directory replacement error = %v, want ErrSymlinkEscape", err)
	} else {
		assertNoRealPathLeak(t, err, root, outside)
	}
}

func TestPreopenedAccessibleSubdirIsIdentityAnchored(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(root, "docs", "script.tcl"), "safe-content")
	writeTestFile(t, filepath.Join(outside, "script.tcl"), "outside-secret")

	translator, err := New([]string{root}, []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	if err := os.Rename(filepath.Join(root, "docs"), filepath.Join(root, "docs.original")); err != nil {
		t.Fatal(err)
	}
	makeTestSymlink(t, outside, filepath.Join(root, "docs"))

	file, err := translator.Open("$p(:0:)/docs/script.tcl")
	if err != nil {
		t.Fatalf("anchored Open failed: %v", err)
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "outside-secret") {
		t.Fatalf("Open followed replacement directory outside the subdir")
	}
	if !strings.Contains(string(content), "safe-content") {
		t.Fatalf("unexpected anchored content %q", content)
	}

	if _, err := translator.Translate("$p(:0:)/docs/script.tcl"); !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Translate after replacement error = %v, want ErrSymlinkEscape", err)
	} else {
		assertNoRealPathLeak(t, err, root, outside)
	}
}

func TestGlobReversesNestedPathsAndNeverLeaksNestedPrefix(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "nested")
	writeTestFile(t, filepath.Join(base, "top.tcl"), "top")
	writeTestFile(t, filepath.Join(nested, "inner.tcl"), "inner")

	translator, err := New([]string{base, nested}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	matches, err := translator.Glob("$p(:0:)/*/*.tcl")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"$p(:1:)/inner.tcl"}
	if len(matches) != len(want) || matches[0] != want[0] {
		t.Fatalf("Glob matches = %#v, want %#v", matches, want)
	}

	if _, err := translator.Translate("$p(:1:)/../../secret.tcl"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("nested escape error = %v, want ErrPathEscape", err)
	} else {
		assertNoRealPathLeak(t, err, base, nested, filepath.Dir(base))
	}
}

func TestGlobMixedBatchFailsAtomically(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(root, "scripts", "good.tcl"), "good")
	writeTestFile(t, filepath.Join(outside, "secret.tcl"), "outside-secret")
	makeTestSymlink(t, filepath.Join(outside, "secret.tcl"), filepath.Join(root, "scripts", "evil.tcl"))

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	matches, err := translator.Glob("$p(:0:)/scripts/*.tcl")
	if err == nil {
		t.Fatalf("Glob mixed batch unexpectedly succeeded: %#v", matches)
	}
	if matches != nil {
		t.Fatalf("Glob returned partial results: %#v", matches)
	}
	assertNoRealPathLeak(t, err, root, outside)
}

func TestGlobRejectsTokenContainingRealPrefix(t *testing.T) {
	root := t.TempDir()
	relativeRoot := strings.TrimPrefix(filepath.ToSlash(root), "/")
	leaking := filepath.Join(root, filepath.FromSlash(relativeRoot), "leak.tcl")
	writeTestFile(t, leaking, "leak")

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	virtualPattern := "$p(:0:)/" + relativeRoot + "/*.tcl"
	matches, err := translator.Glob(virtualPattern)
	if !errors.Is(err, ErrGlobLeak) {
		t.Fatalf("Glob error = %v, want ErrGlobLeak", err)
	}
	if matches != nil {
		t.Fatalf("Glob returned a leaking batch: %#v", matches)
	}
	assertNoRealPathLeak(t, err, root)
}

func TestGlobRejectsExternalSymlinkInWildcardTraversal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(root, "scripts", "good.tcl"), "good")
	writeTestFile(t, filepath.Join(outside, "secret.tcl"), "outside-secret")
	makeTestSymlink(t, outside, filepath.Join(root, "linked"))

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	matches, err := translator.Glob("$p(:0:)/*/secret.tcl")
	if !errors.Is(err, ErrSymlinkEscape) {
		t.Fatalf("Glob traversal error = %v, want ErrSymlinkEscape", err)
	}
	if matches != nil {
		t.Fatalf("Glob returned partial results: %#v", matches)
	}
	assertNoRealPathLeak(t, err, root, outside)
}

func TestAccessibleSubdirectoriesRestrictAllOperations(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "docs", "public.tcl"), "public")
	writeTestFile(t, filepath.Join(root, "private", "secret.tcl"), "private")

	translator, err := New([]string{root}, []string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	if _, err := translator.Open("$p(:0:)/docs/public.tcl"); err != nil {
		t.Fatalf("allowed Open failed: %v", err)
	}
	if _, err := translator.Translate("$p(:0:)/private/secret.tcl"); !errors.Is(err, ErrSubdirDenied) {
		t.Fatalf("denied Translate error = %v, want ErrSubdirDenied", err)
	}
	if _, err := translator.Open("$p(:0:)/private/secret.tcl"); !errors.Is(err, ErrSubdirDenied) {
		t.Fatalf("denied Open error = %v, want ErrSubdirDenied", err)
	}
	if _, err := translator.Source("$p(:0:)/private/secret.tcl"); !errors.Is(err, ErrSubdirDenied) {
		t.Fatalf("denied Source error = %v, want ErrSubdirDenied", err)
	}

	matches, err := translator.Glob("$p(:0:)/*/*.tcl")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0] != "$p(:0:)/docs/public.tcl" {
		t.Fatalf("filtered Glob = %#v", matches)
	}

	matches, err = translator.Glob("$p(:0:)/private/*.tcl")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("disallowed subdir leaked through Glob: %#v", matches)
	}
}

func TestSourceOnlyRegularTclAndSameChecks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "ok.tcl"), "set value 42\n")
	writeTestFile(t, filepath.Join(root, "not-tcl.txt"), "nope")
	writeTestFile(t, filepath.Join(root, "UPPER.TCL"), "nope")
	if err := os.Mkdir(filepath.Join(root, "directory.tcl"), 0o755); err != nil {
		t.Fatal(err)
	}

	translator, err := New([]string{root}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = translator.Close() })

	content, err := translator.Source("$p(:0:)/ok.tcl")
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "set value 42\n" {
		t.Fatalf("unexpected source content %q", content)
	}

	tests := []struct {
		path    string
		wantErr error
	}{
		{"$p(:0:)/not-tcl.txt", ErrNotTclFile},
		{"$p(:0:)/UPPER.TCL", ErrNotTclFile},
		{"$p(:0:)/../secret.tcl", ErrPathEscape},
		{"$p(:0:)/directory.tcl", ErrNotRegularFile},
	}
	for _, test := range tests {
		_, err := translator.Source(test.path)
		if !errors.Is(err, test.wantErr) {
			t.Fatalf("Source(%q) error = %v, want %v", test.path, err, test.wantErr)
		}
		assertNoRealPathLeak(t, err, root, filepath.Dir(root))
	}
}
