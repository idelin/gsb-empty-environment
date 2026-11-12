package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeSymlink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func requireNoRealPath(t *testing.T, err error, roots ...string) {
	t.Helper()
	if err == nil {
		return
	}
	for _, root := range roots {
		if strings.Contains(err.Error(), root) {
			t.Fatalf("error leaks real path %q: %v", root, err)
		}
	}
}

func TestTranslateValidAndRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.tcl"), "puts ok\n")

	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got, err := tr.Translate("$p(:0:)/ok.tcl")
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, "ok.tcl"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Translate=%q, want %q", got, want)
	}

	got, err = tr.Translate("$p(:0:)")
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("root token=%q, want %q", got, root)
	}
}

func TestTranslateRejectsForgedAndTraversalTokens(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.tcl"), "puts ok\n")
	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{"forged index", "$p(:1:)/ok.tcl", ErrTokenOutOfRange},
		{"huge index", "$p(:999999999999999999999999:)/ok.tcl", ErrTokenOutOfRange},
		{"nonnumeric index", "$p(:x:)/ok.tcl", ErrInvalidToken},
		{"negative index", "$p(:-1:)/ok.tcl", ErrInvalidToken},
		{"noncanonical index", "$p(:00:)/ok.tcl", ErrInvalidToken},
		{"missing separator", "$p(:0:)../ok.tcl", ErrInvalidToken},
		{"parent segment", "$p(:0:)/../ok.tcl", ErrPathTraversal},
		{"nested parent", "$p(:0:)/sub/../../ok.tcl", ErrPathTraversal},
		{"parent after file", "$p(:0:)/ok.tcl/..", ErrPathTraversal},
		{"windows traversal", `$p(:0:)/..\..\ok.tcl`, ErrPathTraversal},
		{"real path instead of token", filepath.Join(root, "ok.tcl"), ErrInvalidToken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tr.Translate(tt.token)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Translate(%q) error=%v, want %v", tt.token, err, tt.want)
			}
			requireNoRealPath(t, err, root)
		})
	}
}

func TestTranslateRejectsExternalSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.tcl"), "puts ok\n")
	writeFile(t, filepath.Join(outside, "secret.tcl"), "puts secret\n")

	makeSymlink(t, filepath.Join(outside, "secret.tcl"), filepath.Join(root, "bad.tcl"))
	makeSymlink(t, outside, filepath.Join(root, "escape"))

	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	for _, token := range []string{
		"$p(:0:)/bad.tcl",
		"$p(:0:)/escape/secret.tcl",
	} {
		_, err := tr.Translate(token)
		if !errors.Is(err, ErrAccessDenied) || !errors.Is(err, ErrOutsideAccessPath) {
			t.Fatalf("Translate(%q) error=%v, want outside access path", token, err)
		}
		requireNoRealPath(t, err, root, outside)
	}

	// A symlink that stays inside the access path remains usable.
	makeSymlink(t, filepath.Join(root, "ok.tcl"), filepath.Join(root, "alias.tcl"))
	got, err := tr.Translate("$p(:0:)/alias.tcl")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "ok.tcl") {
		t.Fatalf("inside symlink resolved to %q", got)
	}
}

func TestSymlinkReplacementAfterConfiguration(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.tcl"), "puts secret\n")
	writeFile(t, filepath.Join(root, "swap", "inside.tcl"), "puts inside\n")
	writeFile(t, filepath.Join(root, "swap.tcl"), "puts safe\n")

	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Translate("$p(:0:)/swap/inside.tcl"); err != nil {
		t.Fatal(err)
	}

	// Replace an approved directory with a symlink that leaves the access
	// path after the translator has already been configured.
	if err := os.RemoveAll(filepath.Join(root, "swap")); err != nil {
		t.Fatal(err)
	}
	makeSymlink(t, outside, filepath.Join(root, "swap"))

	// Replace an approved source file with an external symlink as well.
	if err := os.Remove(filepath.Join(root, "swap.tcl")); err != nil {
		t.Fatal(err)
	}
	makeSymlink(t, filepath.Join(outside, "secret.tcl"), filepath.Join(root, "swap.tcl"))

	if _, err := tr.Translate("$p(:0:)/swap/secret.tcl"); !errors.Is(err, ErrOutsideAccessPath) {
		t.Fatalf("replaced directory error=%v", err)
	}
	got, err := tr.Source("$p(:0:)/swap.tcl")
	if err == nil {
		t.Fatalf("replaced source was opened: %q", got)
	}
	if !errors.Is(err, ErrOutsideAccessPath) {
		t.Fatalf("replaced source error=%v", err)
	}
	requireNoRealPath(t, err, root, outside)
}

func TestGlobReturnsOnlyTokens(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.tcl"), "puts a\n")
	writeFile(t, filepath.Join(root, "b.txt"), "text\n")
	writeFile(t, filepath.Join(root, "sub", "nested.tcl"), "puts nested\n")

	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got, err := tr.Glob("$p(:0:)/*.tcl")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"$p(:0:)/a.tcl"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("glob=%v, want %v", got, want)
	}
	for _, token := range got {
		if strings.Contains(token, root) {
			t.Fatalf("glob token leaks real path: %q", token)
		}
		if _, err := tr.Translate(token); err != nil {
			t.Fatalf("glob token does not translate back: %v", err)
		}
	}

	got, err = tr.Glob("$p(:0:)/sub/*.tcl")
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"$p(:0:)/sub/nested.tcl"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("glob=%v, want %v", got, want)
	}

	got, err = tr.Glob("$p(:0:)/none*")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no matches, got %v", got)
	}
}

func TestGlobDoesNotExposeExternalSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.tcl"), "puts ok\n")
	writeFile(t, filepath.Join(outside, "secret.tcl"), "puts secret\n")
	makeSymlink(t, outside, filepath.Join(root, "escape"))
	makeSymlink(t, filepath.Join(outside, "secret.tcl"), filepath.Join(root, "secret.tcl"))

	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got, err := tr.Glob("$p(:0:)/*")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range got {
		if strings.Contains(token, root) || strings.Contains(token, outside) {
			t.Fatalf("glob leaks a real path: %q", token)
		}
		if strings.Contains(token, "escape") || strings.Contains(token, "secret") {
			t.Fatalf("glob returned an external symlink: %q", token)
		}
	}

	if _, err := tr.Glob("$p(:0:)/secret.tcl"); err == nil {
		t.Fatal("explicit external symlink glob was accepted")
	}
	if _, err := tr.Glob("$p(:0:)/escape/*"); err == nil {
		t.Fatal("glob traversed an external symlink directory")
	}
}

func TestGlobRejectsBadTokensAndPatterns(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.tcl"), "puts ok\n")
	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	for _, pattern := range []string{
		"$p(:1:)/*.tcl",
		"$p(:0:)/../*",
		"$p(:0:)/[",
	} {
		_, err := tr.Glob(pattern)
		if err == nil {
			t.Fatalf("Glob(%q) unexpectedly succeeded", pattern)
		}
		requireNoRealPath(t, err, root)
	}
}

func TestSource(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.tcl"), "set value 42\n")
	writeFile(t, filepath.Join(root, "not-tcl.txt"), "not tcl\n")
	writeFile(t, filepath.Join(outside, "secret.tcl"), "puts secret\n")
	makeSymlink(t, filepath.Join(outside, "secret.tcl"), filepath.Join(root, "bad.tcl"))
	makeSymlink(t, filepath.Join(root, "not-tcl.txt"), filepath.Join(root, "looks-tcl.tcl"))
	makeSymlink(t, filepath.Join(root, "ok.tcl"), filepath.Join(root, "looks-text.txt"))
	if err := os.Mkdir(filepath.Join(root, "directory.tcl"), 0o755); err != nil {
		t.Fatal(err)
	}

	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	got, err := tr.Source("$p(:0:)/ok.tcl")
	if err != nil {
		t.Fatal(err)
	}
	if got != "set value 42\n" {
		t.Fatalf("Source contents=%q", got)
	}

	tests := []struct {
		name  string
		token string
		want  error
	}{
		{"non tcl", "$p(:0:)/not-tcl.txt", ErrNotTcl},
		{"symlink to non tcl", "$p(:0:)/looks-tcl.tcl", ErrNotTcl},
		{"non tcl symlink target", "$p(:0:)/looks-text.txt", ErrNotTcl},
		{"external symlink", "$p(:0:)/bad.tcl", ErrOutsideAccessPath},
		{"directory", "$p(:0:)/directory.tcl", ErrNotRegular},
		{"traversal", "$p(:0:)/../secret.tcl", ErrPathTraversal},
		{"forged", "$p(:1:)/ok.tcl", ErrTokenOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tr.Source(tt.token)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Source(%q) error=%v, want %v", tt.token, err, tt.want)
			}
			requireNoRealPath(t, err, root, outside)
		})
	}
}

func TestAllowedSubdirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "public", "good.tcl"), "puts good\n")
	writeFile(t, filepath.Join(root, "private", "secret.tcl"), "puts secret\n")

	tr, err := New([]string{root}, []string{"public"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tr.Translate("$p(:0:)/public/good.tcl"); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Translate("$p(:0:)/private/secret.tcl"); err == nil {
		t.Fatal("private subdirectory was accessible")
	}
	if _, err := tr.Source("$p(:0:)/private/secret.tcl"); err == nil {
		t.Fatal("private source file was accessible")
	}

	got, err := tr.Glob("$p(:0:)/*")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"$p(:0:)/public"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restricted glob=%v, want %v", got, want)
	}
}

func TestErrorsDoNotLeakAccessPath(t *testing.T) {
	root := t.TempDir()
	tr, err := New([]string{root})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		call func() error
	}{
		{func() error { _, err := tr.Translate("$p(:2:)/x.tcl"); return err }},
		{func() error { _, err := tr.Translate("$p(:0:)/../x.tcl"); return err }},
		{func() error { _, err := tr.Glob("$p(:2:)/*"); return err }},
		{func() error { _, err := tr.Source("$p(:0:)/missing.tcl"); return err }},
	}
	for i, tc := range cases {
		err := tc.call()
		if err == nil {
			t.Fatalf("case %d unexpectedly succeeded", i)
		}
		requireNoRealPath(t, err, root)
	}
}
