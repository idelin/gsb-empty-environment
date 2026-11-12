package installpath_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"example.org/netsec/q0255/installpath"
)

func newTestResolver(t *testing.T, root, template string) *installpath.Resolver {
	t.Helper()
	resolver, err := installpath.NewResolver(root, template)
	if err != nil {
		t.Fatalf("NewResolver(%q, %q) unexpected error: %v", root, template, err)
	}
	return resolver
}

func mustResolve(t *testing.T, resolver *installpath.Resolver, pkg installpath.Package) string {
	t.Helper()
	target, err := resolver.Resolve(pkg)
	if err != nil {
		t.Fatalf("Resolve(%+v) unexpected error: %v", pkg, err)
	}
	return target
}

func assertResolveError(t *testing.T, resolver *installpath.Resolver, pkg installpath.Package, target error) {
	t.Helper()
	_, err := resolver.Resolve(pkg)
	if !errors.Is(err, target) {
		t.Fatalf("Resolve(%+v) error = %v, want errors.Is %v", pkg, err, target)
	}
}

func assertTemplateError(t *testing.T, root, template string) {
	t.Helper()
	if _, err := installpath.NewResolver(root, template); !errors.Is(err, installpath.ErrTemplate) {
		t.Fatalf("NewResolver(%q) error = %v, want errors.Is ErrTemplate", template, err)
	}
}

func TestNewResolverRejectsBadRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := installpath.NewResolver(missing, "{$name}"); !errors.Is(err, installpath.ErrTemplate) {
		t.Fatalf("missing root error = %v, want ErrTemplate", err)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := installpath.NewResolver(file, "{$name}"); !errors.Is(err, installpath.ErrTemplate) {
		t.Fatalf("file root error = %v, want ErrTemplate", err)
	}
}

func TestInvalidTemplates(t *testing.T) {
	root := t.TempDir()
	badTemplates := []string{
		"",
		"   ",
		"/absolute/{$name}",
		"{$name}/../escape",
		"../{$name}",
		"./{$name}",
		"{$vendor}/{$name}/{$version}",
		"{vendor}/{$name}",
		"{$name}/{$vendor",
		"{$name}/vendor}",
		"{$name}/bad}segment",
		"{$NAME}",
		"{$name}\\{$vendor}",
	}
	for _, template := range badTemplates {
		assertTemplateError(t, root, template)
	}
}

func TestResolveRendersLowercasedPlaceholders(t *testing.T) {
	root := t.TempDir()
	resolver := newTestResolver(t, root, "{$type}/{$vendor}/{$name}")

	target := mustResolve(t, resolver, installpath.Package{
		Vendor: "AcMe",
		Name:   "Widget",
		Type:   "library",
	})

	want := filepath.Join(root, "library", "acme", "widget")
	if target != want {
		t.Fatalf("target = %q, want %q", target, want)
	}
	if !strings.HasPrefix(target, root+string(filepath.Separator)) {
		t.Fatalf("target %q is not inside root %q", target, root)
	}
}

func TestInstallerNameOverridesName(t *testing.T) {
	root := t.TempDir()
	resolver := newTestResolver(t, root, "{$type}/{$name}")

	target := mustResolve(t, resolver, installpath.Package{
		Vendor:        "acme",
		Name:          "widget",
		Type:          "library",
		InstallerName: "custom-widget-1.2",
	})

	want := filepath.Join(root, "library", "custom-widget-1.2")
	if target != want {
		t.Fatalf("target = %q, want %q", target, want)
	}
}

func TestTemplateWithLiteralSegmentsAndTrailingSlash(t *testing.T) {
	root := t.TempDir()
	resolver := newTestResolver(t, root, "packages/{$type}/{$vendor}/{$name}/")

	target := mustResolve(t, resolver, installpath.Package{
		Vendor: "Acme",
		Name:   "Widget",
		Type:   "library",
	})

	want := filepath.Join(root, "packages", "library", "acme", "widget")
	if target != want {
		t.Fatalf("target = %q, want %q", target, want)
	}
}

func TestAbsoluteInstallerNameFormRejected(t *testing.T) {
	root := t.TempDir()
	resolver := newTestResolver(t, root, "{$name}")

	// Leading separators and Windows-style roots must never survive
	// validation, regardless of the host operating system.
	for _, installerName := range []string{"/abs/path", "//server/share", `\\server\share`} {
		assertResolveError(t, resolver, installpath.Package{
			Vendor: "acme", Name: "widget", Type: "library", InstallerName: installerName,
		}, installpath.ErrInstallerName)
	}
}

func TestTraversalViaInstallerNameRejected(t *testing.T) {
	root := t.TempDir()
	resolver := newTestResolver(t, root, "{$type}/{$name}")

	attackNames := []string{
		"..",
		".",
		"../evil",
		"../../etc/passwd",
		"/etc/passwd",
		"C:evil",
		"C:\\evil",
		"foo/bar",
		`foo\bar`,
		"has space",
		"UP",
		"-leading-dash",
		"trailing-dot.",
		"a:b",
	}
	for _, installerName := range attackNames {
		assertResolveError(t, resolver, installpath.Package{
			Vendor:        "acme",
			Name:          "widget",
			Type:          "library",
			InstallerName: installerName,
		}, installpath.ErrInstallerName)
	}

	assertResolveError(t, resolver, installpath.Package{
		Vendor:        "acme",
		Name:          "widget",
		Type:          "library",
		InstallerName: "LPT1",
	}, installpath.ErrReservedName)
}

func TestInvalidPackageFieldsRejected(t *testing.T) {
	root := t.TempDir()
	resolver := newTestResolver(t, root, "{$type}/{$vendor}/{$name}")

	cases := []struct {
		pkg  installpath.Package
		want error
	}{
		{installpath.Package{Vendor: "", Name: "widget", Type: "library"}, installpath.ErrPackage},
		{installpath.Package{Vendor: "acme", Name: "", Type: "library"}, installpath.ErrPackage},
		{installpath.Package{Vendor: "acme", Name: "widget", Type: ""}, installpath.ErrPackage},
		{installpath.Package{Vendor: "ac/me", Name: "widget", Type: "library"}, installpath.ErrPackage},
		{installpath.Package{Vendor: "acme", Name: "wid get", Type: "library"}, installpath.ErrPackage},
		{installpath.Package{Vendor: "acme", Name: "widget", Type: "lib/ary"}, installpath.ErrPackage},
		{installpath.Package{Vendor: "acme", Name: "widget", Type: ".."}, installpath.ErrPackage},
		{installpath.Package{Vendor: "acme", Name: "widget", Type: "C:evil"}, installpath.ErrPackage},
		{installpath.Package{Vendor: "CON", Name: "widget", Type: "library"}, installpath.ErrReservedName},
		{installpath.Package{Vendor: "acme", Name: "nul.txt", Type: "library"}, installpath.ErrReservedName},
		{installpath.Package{Vendor: "acme", Name: "widget", Type: "Com1"}, installpath.ErrReservedName},
		{installpath.Package{Vendor: "acme", Name: "widget", Type: "aux:stream"}, installpath.ErrReservedName},
	}
	for _, tc := range cases {
		assertResolveError(t, resolver, tc.pkg, tc.want)
	}
}

func TestReservedNamesCovered(t *testing.T) {
	reserved := []string{
		"CON", "PRN", "AUX", "NUL",
		"com1", "com9", "lpt1", "lpt9",
		"con.txt", "NUL.log", "LPT4.old", "aux:data",
	}
	for _, name := range reserved {
		root := t.TempDir()
		resolver := newTestResolver(t, root, "{$name}")
		assertResolveError(t, resolver, installpath.Package{
			Vendor:        "acme",
			Name:          "widget",
			Type:          "library",
			InstallerName: name,
		}, installpath.ErrReservedName)
	}
}

func TestCaseOnlyConflictsRejected(t *testing.T) {
	t.Run("vendor-name-case", func(t *testing.T) {
		resolver := newTestResolver(t, t.TempDir(), "{$vendor}/{$name}")
		mustResolve(t, resolver, installpath.Package{Vendor: "Acme", Name: "Widget", Type: "library"})
		// The lowercased identity acme/widget is already claimed; an
		// installer-name override cannot re-seat it elsewhere without the
		// case-folded package identity being recognized as a duplicate.
		assertResolveError(t, resolver,
			installpath.Package{Vendor: "acme", Name: "WIDGET", Type: "library"},
			installpath.ErrConflict)
	})

	t.Run("installer-name-case", func(t *testing.T) {
		// Uppercase installer-names are rejected outright; case-only conflicts
		// arise from the lowercased vendor/name identity instead. Use a type
		// segment to drive two different packages onto the same path.
		resolver := newTestResolver(t, t.TempDir(), "{$name}")
		mustResolve(t, resolver, installpath.Package{
			Vendor: "Acme", Name: "Widget", Type: "library",
		})
		assertResolveError(t, resolver, installpath.Package{
			Vendor: "other", Name: "package", Type: "library", InstallerName: "widget",
		}, installpath.ErrConflict)
	})

	t.Run("type-case", func(t *testing.T) {
		resolver := newTestResolver(t, t.TempDir(), "{$type}/{$name}")
		// Types preserve their literal casing on disk, but the lowercased
		// conflict key treats MyType and mytype as the same location.
		mustResolve(t, resolver, installpath.Package{Vendor: "a", Name: "b", Type: "MyType"})
		assertResolveError(t, resolver,
			installpath.Package{Vendor: "c", Name: "b", Type: "mytype"},
			installpath.ErrConflict)
	})

	t.Run("identical-package-idempotent", func(t *testing.T) {
		resolver := newTestResolver(t, t.TempDir(), "{$vendor}/{$name}")
		pkg := installpath.Package{Vendor: "acme", Name: "widget", Type: "library"}
		first := mustResolve(t, resolver, pkg)
		second := mustResolve(t, resolver, pkg)
		if first != second {
			t.Fatalf("re-resolve returned %q, want %q", second, first)
		}
	})
}

func TestSymlinkEscapeRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// An existing link inside the root that points outside it. The renderer
	// can be steered through the link by choosing the installer-name.
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	resolver := newTestResolver(t, root, "linked/{$name}")
	assertResolveError(t, resolver, installpath.Package{
		Vendor: "acme", Name: "widget", Type: "library",
	}, installpath.ErrEscape)

	// A link that escapes through multiple existing components.
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(nested, "out")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	resolver = newTestResolver(t, root, "nested/out/{$name}")
	assertResolveError(t, resolver, installpath.Package{
		Vendor: "acme", Name: "widget", Type: "library",
	}, installpath.ErrEscape)
}

func TestInternalSymlinkAllowedAndConflicts(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "real")
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(root, "alias")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Internal links that stay inside the root are followed but permitted.
	resolver := newTestResolver(t, root, "alias/{$name}")
	target := mustResolve(t, resolver, installpath.Package{
		Vendor: "acme", Name: "widget", Type: "library",
	})
	if filepath.Dir(target) != filepath.Join(root, "alias") {
		t.Fatalf("target = %q, want it rendered through the internal alias", target)
	}

	// With a single resolver (one install session), two distinct packages
	// whose templates collapse onto the same real path through the internal
	// link are still rejected. The literal {$name} differs, so the collision
	// is only visible after symbolic links are resolved.
	colliding := newTestResolver(t, root, "alias/shared/{$name}")
	if err := os.Mkdir(filepath.Join(inside, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustResolve(t, colliding, installpath.Package{Vendor: "a", Name: "first", Type: "library", InstallerName: "x"})
	assertResolveError(t, colliding, installpath.Package{
		Vendor: "b", Name: "second", Type: "library", InstallerName: "x",
	}, installpath.ErrConflict)
}

func TestRootItselfResolves(t *testing.T) {
	root := t.TempDir()
	resolver := newTestResolver(t, root, "{$name}")
	target := mustResolve(t, resolver, installpath.Package{
		Vendor: "acme", Name: "widget", Type: "library",
	})
	if filepath.Dir(target) != root {
		t.Fatalf("parent = %q, want root %q", filepath.Dir(target), root)
	}
}

func TestConcurrentResolve(t *testing.T) {
	resolver := newTestResolver(t, t.TempDir(), "{$vendor}/{$name}")

	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			pkg := installpath.Package{
				Vendor: "acme",
				Name:   "pkg" + string(rune('a'+i%4)),
				Type:   "library",
			}
			if _, err := resolver.Resolve(pkg); err != nil && !errors.Is(err, installpath.ErrConflict) {
				t.Errorf("Resolve(%+v) unexpected error: %v", pkg, err)
			}
		}(i)
	}
	wait.Wait()
}

func TestRootReturnsAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relative-root check uses POSIX layout")
	}
	relative := "project"
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	absolute := filepath.Join(cwd, relative)
	if err := os.Mkdir(absolute, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(absolute) })

	resolver := newTestResolver(t, relative, "{$name}")
	if resolver.Root() != absolute {
		t.Fatalf("Root() = %q, want absolute %q", resolver.Root(), absolute)
	}

	rel, err := filepath.Rel(cwd, mustResolve(t, resolver, installpath.Package{
		Vendor: "acme", Name: "widget", Type: "library",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(rel) != filepath.Join("project", "widget") {
		t.Fatalf("relative target = %q, want project/widget", rel)
	}
}
