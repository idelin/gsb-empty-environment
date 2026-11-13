package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func mustBlob(t *testing.T, store *MemoryStore, repository RepositoryID, content string) Digest {
	t.Helper()
	digest, err := store.AddBlob(repository, []byte(content))
	if err != nil {
		t.Fatalf("AddBlob(%s): %v", repository, err)
	}
	return digest
}

func mustTree(t *testing.T, store *MemoryStore, repository RepositoryID, entries ...TreeEntry) Digest {
	t.Helper()
	digest, err := store.AddTree(repository, entries)
	if err != nil {
		t.Fatalf("AddTree(%s): %v", repository, err)
	}
	return digest
}

func mustCommit(t *testing.T, store *MemoryStore, repository RepositoryID, tree Digest, parents ...Digest) Digest {
	t.Helper()
	digest, err := store.AddCommit(repository, tree, parents...)
	if err != nil {
		t.Fatalf("AddCommit(%s): %v", repository, err)
	}
	return digest
}

func fileEntry(name string, digest Digest) TreeEntry {
	return TreeEntry{Name: name, Mode: ModeRegular, Kind: KindBlob, Digest: digest}
}

func execEntry(name string, digest Digest) TreeEntry {
	return TreeEntry{Name: name, Mode: ModeExecutable, Kind: KindBlob, Digest: digest}
}

func symlinkEntry(name string, digest Digest) TreeEntry {
	return TreeEntry{Name: name, Mode: ModeSymlink, Kind: KindBlob, Digest: digest}
}

func treeEntry(name string, digest Digest) TreeEntry {
	return TreeEntry{Name: name, Mode: ModeTree, Kind: KindTree, Digest: digest}
}

func gitlinkEntry(name string, digest Digest) TreeEntry {
	return TreeEntry{Name: name, Mode: ModeGitlink, Kind: KindGitlink, Digest: digest}
}

func requireErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error matching %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("expected error matching %v, got %v", target, err)
	}
}

func requireEntry(t *testing.T, manifest *SourceManifest, path string) SourceEntry {
	t.Helper()
	entry, ok := manifest.EntryByPath(path)
	if !ok {
		t.Fatalf("manifest has no entry %q; entries=%v", path, entryPaths(manifest))
	}
	return entry
}

func entryPaths(manifest *SourceManifest) []string {
	paths := make([]string, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		paths = append(paths, entry.WorkspacePath)
	}
	return paths
}

func TestClosureUsesObjectDatabaseNotWorkspace(t *testing.T) {
	store := NewMemoryStore()
	const (
		mainRepo = RepositoryID("main")
		libRepo  = RepositoryID("lib")
	)

	rootBlob := mustBlob(t, store, mainRepo, "package main // object copy\n")
	pkgBlob := mustBlob(t, store, mainRepo, "package pkg // object copy\n")
	libBlob := mustBlob(t, store, libRepo, "package lib // trusted object\n")
	replacedBlob := mustBlob(t, store, libRepo, "package lib // replaced checkout\n")

	libTree := mustTree(t, store, libRepo, fileEntry("lib.go", libBlob))
	libCommit := mustCommit(t, store, libRepo, libTree)
	replacedTree := mustTree(t, store, libRepo, fileEntry("lib.go", replacedBlob))
	_ = mustCommit(t, store, libRepo, replacedTree)

	pkgTree := mustTree(t, store, mainRepo, fileEntry("a.go", pkgBlob))
	vendorTree := mustTree(t, store, mainRepo, gitlinkEntry("lib", libCommit))
	rootTree := mustTree(
		t,
		store,
		mainRepo,
		fileEntry("main.go", rootBlob),
		treeEntry("pkg", pkgTree),
		treeEntry("vendor", vendorTree),
	)
	rootCommit := mustCommit(t, store, mainRepo, rootTree)

	policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
		mainRepo: {
			TrustedRoots: []Digest{rootCommit},
			Submodules:   map[string]RepositoryID{"vendor/lib": libRepo},
		},
		libRepo: {
			TrustedRoots: []Digest{libCommit},
		},
	}}

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main // attacker checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "vendor", "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "vendor", "lib", "lib.go"), []byte("package lib // replaced checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "UNTRACKED"), []byte("not in the closure\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest, err := BuildSourceClosure(store, policy, mainRepo, rootCommit)
	if err != nil {
		t.Fatalf("BuildSourceClosure: %v", err)
	}
	if len(manifest.Entries) != 3 {
		t.Fatalf("entries = %v, want exactly 3 object blobs", entryPaths(manifest))
	}
	if got := requireEntry(t, manifest, "main.go").Digest; got != rootBlob {
		t.Fatalf("main.go digest = %s, want object digest %s", got, rootBlob)
	}
	libEntry := requireEntry(t, manifest, "vendor/lib/lib.go")
	if libEntry.Digest != libBlob {
		t.Fatalf("submodule blob = %s, want trusted object %s", libEntry.Digest, libBlob)
	}
	if libEntry.Repository != libRepo {
		t.Fatalf("submodule repository = %q, want %q", libEntry.Repository, libRepo)
	}
	if _, ok := manifest.EntryByPath("UNTRACKED"); ok {
		t.Fatalf("untracked workspace file appeared in manifest")
	}
	if _, ok := manifest.EntryByPath("vendor/lib/replaced.go"); ok {
		t.Fatalf("workspace-only file appeared in manifest")
	}
	for _, entry := range manifest.Entries {
		if entry.Digest == replacedBlob {
			t.Fatalf("replaced submodule checkout was used at %q", entry.WorkspacePath)
		}
	}
}

func TestNestedGitlinksPreserveRepositoryPathChain(t *testing.T) {
	store := NewMemoryStore()
	const (
		mainRepo = RepositoryID("main")
		oneRepo  = RepositoryID("one")
		twoRepo  = RepositoryID("two")
	)

	mainBlob := mustBlob(t, store, mainRepo, "main\n")
	oneBlob := mustBlob(t, store, oneRepo, "one\n")
	twoBlob := mustBlob(t, store, twoRepo, "two\n")

	twoTree := mustTree(t, store, twoRepo, fileEntry("two.go", twoBlob))
	twoCommit := mustCommit(t, store, twoRepo, twoTree)

	nestedTree := mustTree(t, store, oneRepo, gitlinkEntry("two", twoCommit))
	oneTree := mustTree(
		t,
		store,
		oneRepo,
		fileEntry("one.go", oneBlob),
		treeEntry("nested", nestedTree),
	)
	oneCommit := mustCommit(t, store, oneRepo, oneTree)

	depsTree := mustTree(t, store, mainRepo, gitlinkEntry("one", oneCommit))
	mainTree := mustTree(
		t,
		store,
		mainRepo,
		fileEntry("main.go", mainBlob),
		treeEntry("deps", depsTree),
	)
	mainCommit := mustCommit(t, store, mainRepo, mainTree)

	policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
		mainRepo: {
			TrustedRoots: []Digest{mainCommit},
			Submodules:   map[string]RepositoryID{"deps/one": oneRepo},
		},
		oneRepo: {
			TrustedRoots: []Digest{oneCommit},
			Submodules:   map[string]RepositoryID{"nested/two": twoRepo},
		},
		twoRepo: {
			TrustedRoots: []Digest{twoCommit},
		},
	}}

	manifest, err := BuildSourceClosure(store, policy, mainRepo, mainCommit)
	if err != nil {
		t.Fatalf("BuildSourceClosure: %v", err)
	}
	if len(manifest.Entries) != 3 {
		t.Fatalf("entries = %v, want 3", entryPaths(manifest))
	}
	if got := requireEntry(t, manifest, "deps/one/one.go").Chain; len(got) != 2 {
		t.Fatalf("one.go chain length = %d, want 2", len(got))
	}
	deep := requireEntry(t, manifest, "deps/one/nested/two/two.go")
	if len(deep.Chain) != 3 {
		t.Fatalf("two.go chain length = %d, want 3", len(deep.Chain))
	}
	wantRepo := []RepositoryID{mainRepo, oneRepo, twoRepo}
	wantPath := []string{"deps/one", "nested/two", "two.go"}
	for i := range deep.Chain {
		if deep.Chain[i].Repository != wantRepo[i] || deep.Chain[i].Path != wantPath[i] {
			t.Fatalf("chain[%d] = %+v, want repo=%q path=%q", i, deep.Chain[i], wantRepo[i], wantPath[i])
		}
	}
}

func TestSharedBlobAndTrustedParentClosure(t *testing.T) {
	store := NewMemoryStore()
	const (
		mainRepo  = RepositoryID("main")
		childRepo = RepositoryID("child")
	)

	shared := "shared bytes\n"
	mainBlob := mustBlob(t, store, mainRepo, shared)
	childBlob := mustBlob(t, store, childRepo, shared)
	if mainBlob != childBlob {
		t.Fatalf("identical content produced different blob digests")
	}

	childTree := mustTree(t, store, childRepo, fileEntry("child.txt", childBlob))
	childOld := mustCommit(t, store, childRepo, childTree)
	childTip := mustCommit(t, store, childRepo, childTree, childOld)

	subTree := mustTree(t, store, mainRepo, gitlinkEntry("child", childOld))
	mainTree := mustTree(
		t,
		store,
		mainRepo,
		fileEntry("main.txt", mainBlob),
		treeEntry("sub", subTree),
	)
	mainCommit := mustCommit(t, store, mainRepo, mainTree)

	policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
		mainRepo: {
			TrustedRoots: []Digest{mainCommit},
			Submodules:   map[string]RepositoryID{"sub/child": childRepo},
		},
		childRepo: {
			TrustedRoots: []Digest{childTip},
		},
	}}

	manifest, err := BuildSourceClosure(store, policy, mainRepo, mainCommit)
	if err != nil {
		t.Fatalf("BuildSourceClosure: %v", err)
	}
	count := 0
	for _, entry := range manifest.Entries {
		if entry.Digest == mainBlob {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("shared digest appears %d times, want 2", count)
	}
}

func TestUndeclaredGitlinkFailsWholeBuild(t *testing.T) {
	store := NewMemoryStore()
	const (
		mainRepo  = RepositoryID("main")
		childRepo = RepositoryID("child")
	)
	blob := mustBlob(t, store, mainRepo, "main\n")
	childTree := mustTree(t, store, childRepo)
	childCommit := mustCommit(t, store, childRepo, childTree)
	subTree := mustTree(t, store, mainRepo, gitlinkEntry("undeclared", childCommit))
	mainTree := mustTree(t, store, mainRepo, fileEntry("main.go", blob), treeEntry("sub", subTree))
	mainCommit := mustCommit(t, store, mainRepo, mainTree)

	policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
		mainRepo:  {TrustedRoots: []Digest{mainCommit}},
		childRepo: {TrustedRoots: []Digest{childCommit}},
	}}
	manifest, err := BuildSourceClosure(store, policy, mainRepo, mainCommit)
	if manifest != nil {
		t.Fatalf("expected nil manifest on failure, got %+v", manifest)
	}
	requireErrorIs(t, err, ErrUndeclaredGitlink)
}

func TestUntrustedSubmoduleCommitFailsWholeBuild(t *testing.T) {
	store := NewMemoryStore()
	const (
		mainRepo  = RepositoryID("main")
		childRepo = RepositoryID("child")
	)
	mainBlob := mustBlob(t, store, mainRepo, "main\n")
	trustedTree := mustTree(t, store, childRepo)
	trusted := mustCommit(t, store, childRepo, trustedTree)
	untrustedBlob := mustBlob(t, store, childRepo, "untrusted\n")
	untrustedTree := mustTree(t, store, childRepo, fileEntry("evil.go", untrustedBlob))
	untrusted := mustCommit(t, store, childRepo, untrustedTree)

	subTree := mustTree(t, store, mainRepo, gitlinkEntry("child", untrusted))
	mainTree := mustTree(t, store, mainRepo, fileEntry("main.go", mainBlob), treeEntry("sub", subTree))
	mainCommit := mustCommit(t, store, mainRepo, mainTree)

	policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
		mainRepo: {
			TrustedRoots: []Digest{mainCommit},
			Submodules:   map[string]RepositoryID{"sub/child": childRepo},
		},
		childRepo: {
			TrustedRoots: []Digest{trusted},
		},
	}}
	manifest, err := BuildSourceClosure(store, policy, mainRepo, mainCommit)
	if manifest != nil {
		t.Fatalf("expected nil manifest on failure, got %+v", manifest)
	}
	requireErrorIs(t, err, ErrUntrustedCommit)
}

func TestMissingObjectAndDigestMismatchFailWholeBuild(t *testing.T) {
	t.Run("missing blob", func(t *testing.T) {
		store := NewMemoryStore()
		const repo = RepositoryID("main")
		missing := HashBlob([]byte("not stored"))
		tree := mustTree(t, store, repo, fileEntry("missing.go", missing))
		commit := mustCommit(t, store, repo, tree)
		policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
			repo: {TrustedRoots: []Digest{commit}},
		}}
		manifest, err := BuildSourceClosure(store, policy, repo, commit)
		if manifest != nil {
			t.Fatalf("expected nil manifest on failure")
		}
		requireErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("missing parent commit", func(t *testing.T) {
		store := NewMemoryStore()
		const repo = RepositoryID("main")
		blob := mustBlob(t, store, repo, "main\n")
		tree := mustTree(t, store, repo, fileEntry("main.go", blob))
		missingParent := HashBlob([]byte("missing parent"))
		commit := mustCommit(t, store, repo, tree, missingParent)
		policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
			repo: {TrustedRoots: []Digest{commit}},
		}}
		_, err := BuildSourceClosure(store, policy, repo, commit)
		requireErrorIs(t, err, ErrObjectNotFound)
	})

	t.Run("digest mismatch", func(t *testing.T) {
		store := NewMemoryStore()
		const repo = RepositoryID("main")
		declared := HashBlob([]byte("declared bytes"))
		actual := []byte("actual bytes")
		if err := store.PutRaw(repo, declared, Object{Type: KindBlob, Data: actual}); err != nil {
			t.Fatal(err)
		}
		tree := mustTree(t, store, repo, fileEntry("file.go", declared))
		commit := mustCommit(t, store, repo, tree)
		policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
			repo: {TrustedRoots: []Digest{commit}},
		}}
		_, err := BuildSourceClosure(store, policy, repo, commit)
		requireErrorIs(t, err, ErrDigestMismatch)
	})
}

func TestSubmoduleDepthBoundary(t *testing.T) {
	buildChain := func(t *testing.T, edges int) (*MemoryStore, TrustManifest, RepositoryID, Digest) {
		t.Helper()
		store := NewMemoryStore()
		policy := TrustManifest{Repositories: make(map[RepositoryID]RepositoryPolicy)}
		repos := make([]RepositoryID, edges+1)
		for i := range repos {
			repos[i] = RepositoryID(fmt.Sprintf("r%d", i))
		}
		var childCommit Digest
		for i := edges; i >= 0; i-- {
			repo := repos[i]
			var entries []TreeEntry
			if i == edges {
				blob := mustBlob(t, store, repo, "leaf\n")
				entries = append(entries, fileEntry("leaf.txt", blob))
			} else {
				entries = append(entries, gitlinkEntry("sub", childCommit))
			}
			tree := mustTree(t, store, repo, entries...)
			childCommit = mustCommit(t, store, repo, tree)
			repoPolicy := RepositoryPolicy{TrustedRoots: []Digest{childCommit}}
			if i != edges {
				repoPolicy.Submodules = map[string]RepositoryID{"sub": repos[i+1]}
			}
			policy.Repositories[repo] = repoPolicy
		}
		return store, policy, repos[0], childCommit
	}

	t.Run("eight edges allowed", func(t *testing.T) {
		store, policy, rootRepo, rootCommit := buildChain(t, MaxSubmoduleDepth)
		manifest, err := BuildSourceClosure(store, policy, rootRepo, rootCommit)
		if err != nil {
			t.Fatalf("depth %d should be allowed: %v", MaxSubmoduleDepth, err)
		}
		if len(manifest.Entries) != 1 {
			t.Fatalf("entries = %v, want one leaf", entryPaths(manifest))
		}
	})

	t.Run("nine edges rejected", func(t *testing.T) {
		store, policy, rootRepo, rootCommit := buildChain(t, MaxSubmoduleDepth+1)
		_, err := BuildSourceClosure(store, policy, rootRepo, rootCommit)
		requireErrorIs(t, err, ErrSubmoduleDepth)
	})
}

func TestRepositoryCommitCycleRejected(t *testing.T) {
	// A real cycle would require a SHA-256 fixed point: the child tree embeds
	// the parent commit digest while the parent commit digest embeds that tree.
	// Inject the active pair at the traversal boundary to exercise the guard
	// without weakening digest verification.
	store := NewMemoryStore()
	const repo = RepositoryID("main")
	tree := mustTree(t, store, repo)
	commit := mustCommit(t, store, repo, tree)
	state := &buildState{
		store:     store,
		policy:    &compiledPolicy{repositories: map[RepositoryID]compiledRepository{repo: {trustedRoots: []Digest{commit}}}},
		closures:  make(map[RepositoryID]map[Digest]struct{}),
		paths:     newPathRegistry(),
		repoStack: []repoCommitKey{{repository: repo, commit: commit}},
	}
	err := state.walkCommit(repo, commit, "", nil, 0)
	requireErrorIs(t, err, ErrSubmoduleCycle)
}

func TestSymlinkContentIsTargetBytesAndCannotBeParent(t *testing.T) {
	store := NewMemoryStore()
	const repo = RepositoryID("main")
	target := "../outside.go"
	targetBlob := mustBlob(t, store, repo, target)
	regularBlob := mustBlob(t, store, repo, "package main\n")
	tree := mustTree(
		t,
		store,
		repo,
		symlinkEntry("link", targetBlob),
		fileEntry("main.go", regularBlob),
	)
	commit := mustCommit(t, store, repo, tree)
	policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
		repo: {TrustedRoots: []Digest{commit}},
	}}
	manifest, err := BuildSourceClosure(store, policy, repo, commit)
	if err != nil {
		t.Fatalf("BuildSourceClosure: %v", err)
	}
	link := requireEntry(t, manifest, "link")
	if link.Mode != ModeSymlink {
		t.Fatalf("link mode = %q, want %q", link.Mode, ModeSymlink)
	}
	if link.Digest != HashBlob([]byte(target)) {
		t.Fatalf("symlink digest does not bind target bytes")
	}
	if link.Size != int64(len(target)) {
		t.Fatalf("symlink size = %d, want %d", link.Size, len(target))
	}
	if _, ok := manifest.EntryByPath("outside.go"); ok {
		t.Fatalf("symlink target was followed")
	}

	registry := newPathRegistry()
	if err := registry.addLeaf("link", true); err != nil {
		t.Fatal(err)
	}
	requireErrorIs(t, registry.addLeaf("link/child", false), ErrPathConflict)

	registry = newPathRegistry()
	if err := registry.addLeaf("link", true); err != nil {
		t.Fatal(err)
	}
	requireErrorIs(t, registry.addDir("link"), ErrPathConflict)
}

func TestDuplicateWorkspacePathAndMalformedTreeFail(t *testing.T) {
	entries := []SourceEntry{
		{
			Repository:    "main",
			Path:          "same.go",
			WorkspacePath: "same.go",
			Mode:          ModeRegular,
			Kind:          KindBlob,
			Digest:        HashBlob([]byte("one")),
			Chain:         []PathStep{{Repository: "main", Path: "same.go"}},
		},
		{
			Repository:    "main",
			Path:          "same.go",
			WorkspacePath: "same.go",
			Mode:          ModeRegular,
			Kind:          KindBlob,
			Digest:        HashBlob([]byte("two")),
			Chain:         []PathStep{{Repository: "main", Path: "same.go"}},
		},
	}
	_, err := ComputeSourceTreeRoot(entries)
	requireErrorIs(t, err, ErrPathConflict)
	_, err = ComputeManifestRoot(entries)
	requireErrorIs(t, err, ErrPathConflict)

	store := NewMemoryStore()
	const repo = RepositoryID("main")
	one := mustBlob(t, store, repo, "one")
	two := mustBlob(t, store, repo, "two")
	// Encode duplicate names without using NewTree's canonicalization. The
	// object digest still matches the malicious body, so the failure must come
	// from canonical tree parsing rather than a digest check.
	var body bytes.Buffer
	for _, entry := range []TreeEntry{fileEntry("dup", one), fileEntry("dup", two)} {
		body.WriteString(string(entry.Mode))
		body.WriteByte(0)
		putUint64(&body, uint64(len(entry.Name)))
		body.WriteString(entry.Name)
		body.Write(entry.Digest[:])
	}
	maliciousTree := hashDomain(TreeTag, body.Bytes())
	if err := store.PutRaw(repo, maliciousTree, Object{Type: KindTree, Data: body.Bytes()}); err != nil {
		t.Fatal(err)
	}
	commit := mustCommit(t, store, repo, maliciousTree)
	policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
		repo: {TrustedRoots: []Digest{commit}},
	}}
	manifest, err := BuildSourceClosure(store, policy, repo, commit)
	if manifest != nil {
		t.Fatalf("expected nil manifest on malformed tree")
	}
	requireErrorIs(t, err, ErrDuplicateEntry)
}

func TestManifestRootsAreDeterministicAndObjectOnly(t *testing.T) {
	store := NewMemoryStore()
	const (
		mainRepo  = RepositoryID("main")
		childRepo = RepositoryID("child")
	)
	mainBlob := mustBlob(t, store, mainRepo, "main\n")
	childBlob := mustBlob(t, store, childRepo, "child\n")
	childTree := mustTree(t, store, childRepo, fileEntry("child.go", childBlob))
	childCommit := mustCommit(t, store, childRepo, childTree)
	subTree := mustTree(t, store, mainRepo, gitlinkEntry("child", childCommit))
	mainTree := mustTree(
		t,
		store,
		mainRepo,
		fileEntry("main.go", mainBlob),
		execEntry("run.sh", mustBlob(t, store, mainRepo, "#!/bin/sh\n")),
		treeEntry("sub", subTree),
	)
	mainCommit := mustCommit(t, store, mainRepo, mainTree)
	policy := TrustManifest{Repositories: map[RepositoryID]RepositoryPolicy{
		mainRepo: {
			TrustedRoots: []Digest{mainCommit},
			Submodules:   map[string]RepositoryID{"sub/child": childRepo},
		},
		childRepo: {
			TrustedRoots: []Digest{childCommit},
		},
	}}

	first, err := BuildSourceClosure(store, policy, mainRepo, mainCommit)
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("changed on disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := BuildSourceClosure(store, policy, mainRepo, mainCommit)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if first.TreeRoot != second.TreeRoot {
		t.Fatalf("tree root changed after workspace mutation: %s != %s", first.TreeRoot, second.TreeRoot)
	}
	if first.ManifestRoot != second.ManifestRoot {
		t.Fatalf("manifest root changed after workspace mutation: %s != %s", first.ManifestRoot, second.ManifestRoot)
	}
	treeRoot, err := ComputeSourceTreeRoot(first.Entries)
	if err != nil {
		t.Fatal(err)
	}
	manifestRoot, err := ComputeManifestRoot(first.Entries)
	if err != nil {
		t.Fatal(err)
	}
	if treeRoot != first.TreeRoot || manifestRoot != first.ManifestRoot {
		t.Fatalf("manifest roots are not reproducible from entries")
	}
}

func TestObjectEncodingIsCanonical(t *testing.T) {
	one := HashBlob([]byte("one"))
	two := HashBlob([]byte("two"))
	unsorted, err := HashTree([]TreeEntry{fileEntry("b", two), fileEntry("a", one)})
	if err != nil {
		t.Fatal(err)
	}
	sorted, err := HashTree([]TreeEntry{fileEntry("a", one), fileEntry("b", two)})
	if err != nil {
		t.Fatal(err)
	}
	if unsorted != sorted {
		t.Fatalf("tree digest depends on input order")
	}
	if _, err := HashTree([]TreeEntry{fileEntry("a", one), fileEntry("a", two)}); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("duplicate tree names: got %v, want %v", err, ErrDuplicateEntry)
	}

	blobObject := Object{Type: KindBlob, Data: []byte("content")}
	actual, err := ObjectDigest(blobObject)
	if err != nil {
		t.Fatal(err)
	}
	if actual != HashBlob([]byte("content")) {
		t.Fatalf("blob digest mismatch")
	}

	commit := NewCommit(sorted, []Digest{one, two})
	parsed, err := ParseCommit(commit)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Tree != sorted || len(parsed.Parents) != 2 || parsed.Parents[0] != one || parsed.Parents[1] != two {
		t.Fatalf("commit round trip mismatch: %+v", parsed)
	}
}
