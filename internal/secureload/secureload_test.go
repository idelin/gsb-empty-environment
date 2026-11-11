package secureload

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fixture is a signed module laid out under an authorized root.
type fixture struct {
	name    string
	root    string
	relPath string
	content []byte
	deps    []string
}

func (f fixture) digest() string {
	sum := sha256.Sum256(f.content)
	return DigestAlgoSHA256 + ":" + hex.EncodeToString(sum[:])
}

// testSandbox holds a directory tree with two authorized roots and a
// three-layer dependency chain: base <- plugin <- entry.
type testSandbox struct {
	t        *testing.T
	baseDir  string
	extDir   string
	base     fixture
	plugin   fixture
	entry    fixture
	fixtures map[string]fixture
}

func newTestSandbox(t *testing.T) *testSandbox {
	t.Helper()
	tmp := t.TempDir()
	baseDir := filepath.Join(tmp, "authorized-base")
	extDir := filepath.Join(tmp, "authorized-ext")
	if err := os.MkdirAll(filepath.Join(baseDir, "natives"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(extDir, "plugins", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}

	sb := &testSandbox{
		t:       t,
		baseDir: baseDir,
		extDir:  extDir,
		base: fixture{
			name:    "base-runtime",
			root:    "base",
			relPath: filepath.Join("natives", "libruntime.dylib"),
			content: []byte("base-runtime-object-v1"),
		},
		plugin: fixture{
			name:    "feature-plugin",
			root:    "ext",
			relPath: filepath.Join("plugins", "libfeature.dylib"),
			content: []byte("feature-plugin-object-v1"),
			deps:    []string{"base-runtime"},
		},
		entry: fixture{
			name:    "extension-entry",
			root:    "ext",
			relPath: filepath.Join("plugins", "deep", "entry.dylib"),
			content: []byte("extension-entry-object-v1"),
			deps:    []string{"feature-plugin"},
		},
	}
	sb.fixtures = map[string]fixture{
		sb.base.name:   sb.base,
		sb.plugin.name: sb.plugin,
		sb.entry.name:  sb.entry,
	}
	for _, fix := range sb.fixtures {
		sb.writeFixture(fix)
	}
	return sb
}

func (sb *testSandbox) writeFixture(f fixture) {
	sb.t.Helper()
	target := sb.onDiskPath(f)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		sb.t.Fatal(err)
	}
	if err := os.WriteFile(target, f.content, 0o644); err != nil {
		sb.t.Fatal(err)
	}
}

func (sb *testSandbox) onDiskPath(f fixture) string {
	root := sb.baseDir
	if f.root == "ext" {
		root = sb.extDir
	}
	return filepath.Join(root, filepath.FromSlash(f.relPath))
}

func (sb *testSandbox) manifest() *Manifest {
	roots := map[string]string{
		"base": sb.baseDir,
		"ext":  sb.extDir,
	}
	modules := make(map[string]ModuleSpec, len(sb.fixtures))
	for name, fix := range sb.fixtures {
		digest, err := ParseDigest(fix.digest())
		if err != nil {
			sb.t.Fatal(err)
		}
		modules[name] = ModuleSpec{
			RootName: fix.root,
			RelPath:  filepath.ToSlash(fix.relPath),
			Digest:   digest,
			Deps:     slices.Clone(fix.deps),
		}
	}
	return &Manifest{Roots: roots, Modules: modules}
}

// guardedCallback records every module reaching the callback and proves the
// handed object is the verified one: its streamed digest must equal the
// manifest digest and its bytes must match the expected fixture.
func guardedCallback(t *testing.T, sb *testSandbox, got *[]string) LoadCallback {
	t.Helper()
	return func(module string, spec ModuleSpec, file *os.File) error {
		body, err := io.ReadAll(file)
		if err != nil {
			return fmt.Errorf("callback read failed: %w", err)
		}
		sum := sha256.Sum256(body)
		gotDigest := DigestAlgoSHA256 + ":" + hex.EncodeToString(sum[:])
		if gotDigest != spec.Digest.String() {
			t.Errorf("callback received unverified object for %q: digest %s, want %s",
				module, gotDigest, spec.Digest)
		}
		if fix, ok := sb.fixtures[module]; ok && string(body) != string(fix.content) {
			t.Errorf("callback object for %q has unexpected content %q", module, body)
		}
		*got = append(*got, module)
		return nil
	}
}

// attackOpener wraps the production SecureOpener and mutates the filesystem in
// the window between path selection and the actual open call. It is the
// controllable substitution point for pre-open TOCTOU attacks.
type attackOpener struct {
	inner   Opener
	attack  func(rootName, relPath string)
	targets map[string]bool
}

func (a *attackOpener) OpenAuthenticated(rootName, relPath string) (*os.File, error) {
	if a.targets[relPath] {
		a.attack(rootName, relPath)
	}
	return a.inner.OpenAuthenticated(rootName, relPath)
}

func (sb *testSandbox) newAttackOpener(t *testing.T, attack func(string, string), targetFixtures ...fixture) *attackOpener {
	secure, err := NewSecureOpener(sb.manifest())
	if err != nil {
		t.Fatal(err)
	}
	targets := make(map[string]bool, len(targetFixtures))
	for _, fix := range targetFixtures {
		targets[filepath.ToSlash(fix.relPath)] = true
	}
	t.Cleanup(func() { _ = secure.Close() })
	return &attackOpener{inner: secure, attack: attack, targets: targets}
}

// assertAbsent fails if any of the named modules reached the callback. Lower
// layers verified before an attack are legitimately present and are passed in
// wantLoaded; the invariant under test is that the unverified object and
// everything above it never reach the callback.
func assertAbsent(t *testing.T, got []string, wantLoaded []string, forbidden ...string) {
	t.Helper()
	if !slices.Equal(got, wantLoaded) {
		t.Fatalf("callback modules = %v, want only verified lower layers %v", got, wantLoaded)
	}
	for _, name := range forbidden {
		if slices.Contains(got, name) {
			t.Errorf("unverified module %q reached callback: %v", name, got)
		}
	}
}

func TestLoadAllLayersAuthenticated(t *testing.T) {
	sb := newTestSandbox(t)
	loader, err := NewLoader(sb.manifest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loader.Close() })

	var got []string
	if err := loader.Load(sb.entry.name, guardedCallback(t, sb, &got)); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	want := []string{sb.base.name, sb.plugin.name, sb.entry.name}
	if !slices.Equal(got, want) {
		t.Fatalf("callback order = %v, want dependency-first order %v", got, want)
	}
}

func TestRenameAfterLookup(t *testing.T) {
	sb := newTestSandbox(t)
	realPath := sb.onDiskPath(sb.plugin)

	// The attacker renames the resolved name away and plants a decoy before
	// the open syscall. The pinned root still resolves the current name, so
	// the decoy is opened and then rejected on digest.
	opener := sb.newAttackOpener(t, func(rootName, relPath string) {
		hidden := realPath + ".hidden"
		if err := os.Rename(realPath, hidden); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(realPath, []byte("attacker-renamed-decoy"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, sb.plugin)

	loader, err := NewLoaderWithOpener(sb.manifest(), opener)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = loader.Load(sb.entry.name, guardedCallback(t, sb, &got))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	assertAbsent(t, got, []string{sb.base.name}, sb.plugin.name, sb.entry.name)
}

func TestSymlinkEscape(t *testing.T) {
	sb := newTestSandbox(t)
	realPath := sb.onDiskPath(sb.plugin)
	outside := filepath.Join(t.TempDir(), "outside.dylib")
	if err := os.WriteFile(outside, []byte("escaped-outside-object"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The attacker swaps the module for a symlink pointing outside every
	// authorized root during the lookup/open window.
	opener := sb.newAttackOpener(t, func(rootName, relPath string) {
		if err := os.Remove(realPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, realPath); err != nil {
			t.Fatal(err)
		}
	}, sb.plugin)

	loader, err := NewLoaderWithOpener(sb.manifest(), opener)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = loader.Load(sb.entry.name, guardedCallback(t, sb, &got))
	if err == nil {
		t.Fatal("symlink escaping the authorized root was accepted")
	}
	assertAbsent(t, got, []string{sb.base.name}, sb.plugin.name, sb.entry.name)
}

func TestSameNameDifferentDigest(t *testing.T) {
	sb := newTestSandbox(t)
	realPath := sb.onDiskPath(sb.entry)

	// Same path and filename, but the inode now holds different bytes.
	opener := sb.newAttackOpener(t, func(rootName, relPath string) {
		if err := os.WriteFile(realPath, []byte("same-name-evil-content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, sb.entry)

	loader, err := NewLoaderWithOpener(sb.manifest(), opener)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = loader.Load(sb.entry.name, guardedCallback(t, sb, &got))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	assertAbsent(t, got, []string{sb.base.name, sb.plugin.name}, sb.entry.name)
}

func TestPostOpenRenameSwapKeepsVerifiedObject(t *testing.T) {
	sb := newTestSandbox(t)
	realPath := sb.onDiskPath(sb.plugin)
	decoyPath := filepath.Join(filepath.Dir(realPath), "decoy.dylib")
	if err := os.WriteFile(decoyPath, []byte("attacker-renamed-decoy"), 0o644); err != nil {
		t.Fatal(err)
	}

	loader, err := NewLoader(sb.manifest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loader.Close() })

	// The swap happens after the open object exists. The fd stays bound to
	// the original inode; hashing and callback see the verified bytes.
	loader.PostOpen = func(module string, spec ModuleSpec, file *os.File) error {
		if module != sb.plugin.name {
			return nil
		}
		return os.Rename(decoyPath, realPath)
	}

	var got []string
	if err := loader.Load(sb.entry.name, guardedCallback(t, sb, &got)); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	want := []string{sb.base.name, sb.plugin.name, sb.entry.name}
	if !slices.Equal(got, want) {
		t.Fatalf("callback order = %v, want %v", got, want)
	}
}

func TestPostOpenInPlaceMutationRejected(t *testing.T) {
	sb := newTestSandbox(t)
	realPath := sb.onDiskPath(sb.entry)

	loader, err := NewLoader(sb.manifest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loader.Close() })

	// Mutating the open inode itself (truncate + rewrite) changes what the
	// fd streams, so digest verification fails and the callback is skipped.
	loader.PostOpen = func(module string, spec ModuleSpec, file *os.File) error {
		if module != sb.entry.name {
			return nil
		}
		return os.WriteFile(realPath, []byte("mutated-after-open"), 0o644)
	}

	var got []string
	err = loader.Load(sb.entry.name, guardedCallback(t, sb, &got))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	assertAbsent(t, got, []string{sb.base.name, sb.plugin.name}, sb.entry.name)
}

func TestTamperedLowerLayerAbortsWholeLoad(t *testing.T) {
	sb := newTestSandbox(t)
	realPath := sb.onDiskPath(sb.base)

	// Compromise the deepest layer; nothing above it may be handed out.
	opener := sb.newAttackOpener(t, func(rootName, relPath string) {
		if err := os.WriteFile(realPath, []byte("corrupted-base"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, sb.base)

	loader, err := NewLoaderWithOpener(sb.manifest(), opener)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = loader.Load(sb.entry.name, guardedCallback(t, sb, &got))
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want ErrDigestMismatch, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("callback invoked despite compromised base layer: %v", got)
	}
}

func TestManifestValidation(t *testing.T) {
	sb := newTestSandbox(t)

	t.Run("relative authorized root", func(t *testing.T) {
		m := sb.manifest()
		m.Roots["ext"] = filepath.Join("relative", "path")
		if err := m.Validate(); err == nil {
			t.Fatal("relative root directory was accepted")
		}
	})

	t.Run("unknown root reference", func(t *testing.T) {
		m := sb.manifest()
		spec := m.Modules[sb.entry.name]
		spec.RootName = "unlisted"
		m.Modules[sb.entry.name] = spec
		if err := m.Validate(); err == nil {
			t.Fatal("module referencing an unknown root was accepted")
		}
	})

	t.Run("escaping module path", func(t *testing.T) {
		m := sb.manifest()
		spec := m.Modules[sb.entry.name]
		spec.RelPath = "../evil.dylib"
		m.Modules[sb.entry.name] = spec
		if err := m.Validate(); err == nil {
			t.Fatal("path escaping its authorized root was accepted")
		}
	})

	t.Run("unknown dependency", func(t *testing.T) {
		m := sb.manifest()
		spec := m.Modules[sb.base.name]
		spec.Deps = []string{"ghost-module"}
		m.Modules[sb.base.name] = spec
		if err := m.Validate(); err == nil {
			t.Fatal("unknown dependency was accepted")
		}
	})

	t.Run("self dependency", func(t *testing.T) {
		m := sb.manifest()
		spec := m.Modules[sb.base.name]
		spec.Deps = []string{sb.base.name}
		m.Modules[sb.base.name] = spec
		if err := m.Validate(); err == nil {
			t.Fatal("self dependency was accepted")
		}
	})

	t.Run("malformed digest", func(t *testing.T) {
		if _, err := ParseDigest("sha256:not-hex"); err == nil {
			t.Fatal("malformed digest was accepted")
		}
		if _, err := ParseDigest("md5:00"); err == nil {
			t.Fatal("unsupported digest algorithm was accepted")
		}
	})
}

func TestNonRegularFileRejected(t *testing.T) {
	sb := newTestSandbox(t)
	target := sb.onDiskPath(sb.entry)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	loader, err := NewLoader(sb.manifest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loader.Close() })

	var got []string
	err = loader.Load(sb.entry.name, guardedCallback(t, sb, &got))
	if err == nil {
		t.Fatal("directory opened as a module was accepted")
	}
	assertAbsent(t, got, []string{sb.base.name, sb.plugin.name}, sb.entry.name)
}

func TestMissingAuthorizedRootAbortsConstruction(t *testing.T) {
	sb := newTestSandbox(t)
	m := sb.manifest()
	m.Roots["ghost"] = filepath.Join(t.TempDir(), "does-not-exist")
	spec := m.Modules[sb.entry.name]
	spec.RootName = "ghost"
	m.Modules[sb.entry.name] = spec

	if _, err := NewLoader(m); err == nil {
		t.Fatal("loader accepted a missing authorized root")
	}
}

func TestDependencyCycleRejected(t *testing.T) {
	sb := newTestSandbox(t)
	m := sb.manifest()
	baseSpec := m.Modules[sb.base.name]
	baseSpec.Deps = []string{sb.entry.name}
	m.Modules[sb.base.name] = baseSpec

	loader, err := NewLoader(m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loader.Close() })

	var got []string
	err = loader.Load(sb.entry.name, guardedCallback(t, sb, &got))
	if err == nil {
		t.Fatal("dependency cycle was not detected")
	}
	if len(got) != 0 {
		t.Fatalf("callback invoked inside a cycle: %v", got)
	}
}

func TestCallbackErrorAborts(t *testing.T) {
	sb := newTestSandbox(t)
	loader, err := NewLoader(sb.manifest())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = loader.Close() })

	sentinel := errors.New("dlopen failed")
	var got []string
	err = loader.Load(sb.entry.name, func(module string, spec ModuleSpec, file *os.File) error {
		got = append(got, module)
		if module == sb.plugin.name {
			return sentinel
		}
		return nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want callback error, got %v", err)
	}
	want := []string{sb.base.name, sb.plugin.name}
	if !slices.Equal(got, want) {
		t.Fatalf("callback invocations = %v, want %v", got, want)
	}
}

func TestLoaderRejectsNilOpener(t *testing.T) {
	sb := newTestSandbox(t)
	if _, err := NewLoaderWithOpener(sb.manifest(), nil); err == nil {
		t.Fatal("loader accepted a nil opener")
	}
}

func TestSymlinkedAuthorizedRootRejected(t *testing.T) {
	sb := newTestSandbox(t)
	linkDir := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(sb.extDir, linkDir); err != nil {
		t.Fatal(err)
	}

	m := sb.manifest()
	m.Roots["ext"] = linkDir
	if _, err := NewSecureOpener(m); err == nil {
		t.Fatal("opener accepted a symbolic link as an authorized root")
	}
}

func TestIntermediateComponentSymlinkEscape(t *testing.T) {
	sb := newTestSandbox(t)

	// The entry lives under plugins/deep/. Replace an intermediate directory
	// component with a symlink to a directory outside every authorized root
	// during the lookup/open window.
	pluginsDir := filepath.Join(sb.extDir, "plugins")
	deepDir := filepath.Join(pluginsDir, "deep")
	outsideDir := t.TempDir()

	opener := sb.newAttackOpener(t, func(rootName, relPath string) {
		if err := os.Rename(deepDir, deepDir+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideDir, deepDir); err != nil {
			t.Fatal(err)
		}
	}, sb.entry)

	loader, err := NewLoaderWithOpener(sb.manifest(), opener)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	err = loader.Load(sb.entry.name, guardedCallback(t, sb, &got))
	if err == nil {
		t.Fatal("resolution through an escaping intermediate symlink was accepted")
	}
	assertAbsent(t, got, []string{sb.base.name, sb.plugin.name}, sb.entry.name)
}
