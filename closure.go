package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MaxSubmoduleDepth is the maximum number of nested gitlink edges. The
// top-level commit is depth zero, so a chain of eight nested submodules is
// allowed and a ninth is rejected.
const MaxSubmoduleDepth = 8

// MaxDepth is an alias for MaxSubmoduleDepth.
const MaxDepth = MaxSubmoduleDepth

// BuildRequest identifies the top-level repository commit whose object
// closure should be materialized.
type BuildRequest struct {
	Repository RepositoryID
	Commit     Digest
}

// Builder computes source closures from a host object store and a caller trust
// manifest. It never reads a checkout, a working tree, or any other mutable
// filesystem state.
type Builder struct {
	Store  ObjectStore
	Policy TrustManifest
}

// NewBuilder creates a reusable builder.
func NewBuilder(store ObjectStore, policy TrustManifest) *Builder {
	return &Builder{Store: store, Policy: policy}
}

// Build computes a complete source manifest or fails without returning a
// partial result.
func (b *Builder) Build(request BuildRequest) (*SourceManifest, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: nil builder", ErrInvalidObject)
	}
	if b.Store == nil {
		return nil, fmt.Errorf("%w: nil object store", ErrObjectNotFound)
	}
	if err := validateRepositoryID(request.Repository); err != nil {
		return nil, err
	}
	if request.Commit.IsZero() {
		return nil, fmt.Errorf("%w: zero root commit", ErrInvalidObject)
	}
	compiled, err := compileTrustManifest(b.Policy)
	if err != nil {
		return nil, err
	}
	state := &buildState{
		store:     b.Store,
		policy:    compiled,
		closures:  make(map[RepositoryID]map[Digest]struct{}),
		paths:     newPathRegistry(),
		entries:   make([]SourceEntry, 0),
		repoStack: make([]repoCommitKey, 0, MaxSubmoduleDepth+1),
		treeStack: make([]repoTreeKey, 0, 32),
	}
	if err := state.walkCommit(request.Repository, request.Commit, "", nil, 0); err != nil {
		return nil, err
	}
	entries := append([]SourceEntry(nil), state.entries...)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].WorkspacePath < entries[j].WorkspacePath
	})
	for i := 1; i < len(entries); i++ {
		if entries[i-1].WorkspacePath == entries[i].WorkspacePath {
			return nil, fmt.Errorf("%w: workspace path %q has multiple sources", ErrPathConflict, entries[i].WorkspacePath)
		}
	}
	treeRoot, err := ComputeSourceTreeRoot(entries)
	if err != nil {
		return nil, err
	}
	manifestRoot, err := ComputeManifestRoot(entries)
	if err != nil {
		return nil, err
	}
	return &SourceManifest{
		Root:         request.Repository,
		Commit:       request.Commit,
		Entries:      entries,
		TreeRoot:     treeRoot,
		ManifestRoot: manifestRoot,
	}, nil
}

// BuildRoot is a convenience method for callers that do not use BuildRequest.
func (b *Builder) BuildRoot(repository RepositoryID, commit Digest) (*SourceManifest, error) {
	return b.Build(BuildRequest{Repository: repository, Commit: commit})
}

// BuildSourceClosure builds a source manifest from the immutable object
// closure rooted at root/commit.
func BuildSourceClosure(store ObjectStore, policy TrustManifest, root RepositoryID, commit Digest) (*SourceManifest, error) {
	return NewBuilder(store, policy).Build(BuildRequest{Repository: root, Commit: commit})
}

// BuildSourceManifest is an alias for BuildSourceClosure.
func BuildSourceManifest(store ObjectStore, policy TrustManifest, root RepositoryID, commit Digest) (*SourceManifest, error) {
	return BuildSourceClosure(store, policy, root, commit)
}

// Build is an alias for BuildSourceClosure.
func Build(store ObjectStore, policy TrustManifest, root RepositoryID, commit Digest) (*SourceManifest, error) {
	return BuildSourceClosure(store, policy, root, commit)
}

type repoCommitKey struct {
	repository RepositoryID
	commit     Digest
}

type repoTreeKey struct {
	repository RepositoryID
	tree       Digest
}

type buildState struct {
	store     ObjectStore
	policy    *compiledPolicy
	closures  map[RepositoryID]map[Digest]struct{}
	entries   []SourceEntry
	paths     *pathRegistry
	repoStack []repoCommitKey
	treeStack []repoTreeKey
}

func joinPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "/" + name
}

func appendPathStep(chain []PathStep, step PathStep) []PathStep {
	out := make([]PathStep, len(chain)+1)
	copy(out, chain)
	out[len(chain)] = step
	return out
}

func (s *buildState) walkCommit(repository RepositoryID, commit Digest, workspaceBase string, chain []PathStep, depth int) error {
	if depth > MaxSubmoduleDepth {
		return closureError(repository, workspaceBase, ErrSubmoduleDepth, "walk commit")
	}
	key := repoCommitKey{repository: repository, commit: commit}
	for _, active := range s.repoStack {
		if active == key {
			return closureError(repository, workspaceBase, ErrSubmoduleCycle, "walk commit")
		}
	}
	closure, err := s.trustedClosure(repository)
	if err != nil {
		return err
	}
	if _, ok := closure[commit]; !ok {
		return closureError(repository, workspaceBase, ErrUntrustedCommit, "walk commit")
	}
	commitObject, err := s.readObject(repository, commit, KindCommit, workspaceBase)
	if err != nil {
		return err
	}
	parsed, err := ParseCommit(commitObject)
	if err != nil {
		return closureError(repository, workspaceBase, err, "parse commit")
	}

	s.repoStack = append(s.repoStack, key)
	defer func() {
		s.repoStack = s.repoStack[:len(s.repoStack)-1]
	}()
	return s.walkTree(repository, parsed.Tree, "", workspaceBase, chain, depth)
}

func (s *buildState) walkTree(repository RepositoryID, tree Digest, repositoryBase, workspaceBase string, chain []PathStep, depth int) error {
	key := repoTreeKey{repository: repository, tree: tree}
	for _, active := range s.treeStack {
		if active == key {
			return closureError(repository, workspaceBase, ErrTreeCycle, "walk tree")
		}
	}
	treeObject, err := s.readObject(repository, tree, KindTree, workspaceBase)
	if err != nil {
		return err
	}
	entries, err := ParseTree(treeObject)
	if err != nil {
		return closureError(repository, workspaceBase, err, "parse tree")
	}

	s.treeStack = append(s.treeStack, key)
	defer func() {
		s.treeStack = s.treeStack[:len(s.treeStack)-1]
	}()

	for _, entry := range entries {
		repositoryPath := joinPath(repositoryBase, entry.Name)
		workspacePath := joinPath(workspaceBase, entry.Name)
		switch entry.Kind {
		case KindTree:
			if err := s.paths.addDir(workspacePath); err != nil {
				return closureError(repository, workspacePath, err, "walk tree")
			}
			if err := s.walkTree(repository, entry.Digest, repositoryPath, workspacePath, chain, depth); err != nil {
				return err
			}

		case KindBlob:
			blob, err := s.readObject(repository, entry.Digest, KindBlob, workspacePath)
			if err != nil {
				return err
			}
			if err := s.paths.addLeaf(workspacePath, entry.Mode == ModeSymlink); err != nil {
				return closureError(repository, workspacePath, err, "walk tree")
			}
			stepChain := appendPathStep(chain, PathStep{Repository: repository, Path: repositoryPath})
			s.entries = append(s.entries, SourceEntry{
				Repository:    repository,
				Path:          repositoryPath,
				WorkspacePath: workspacePath,
				Mode:          entry.Mode,
				Kind:          KindBlob,
				Digest:        entry.Digest,
				Size:          int64(len(blob.Data)),
				Chain:         stepChain,
			})

		case KindGitlink:
			if depth+1 > MaxSubmoduleDepth {
				return closureError(repository, workspacePath, ErrSubmoduleDepth, "walk gitlink")
			}
			if err := s.paths.addDir(workspacePath); err != nil {
				return closureError(repository, workspacePath, err, "walk gitlink")
			}
			repositoryPolicy, ok := s.policy.repository(repository)
			if !ok {
				return closureError(repository, workspacePath, ErrInvalidPolicy, "walk gitlink")
			}
			child, ok := repositoryPolicy.childFor(repositoryPath)
			if !ok {
				return closureError(repository, workspacePath, ErrUndeclaredGitlink, "walk gitlink")
			}
			childChain := appendPathStep(chain, PathStep{Repository: repository, Path: repositoryPath})
			if err := s.walkCommit(child, entry.Digest, workspacePath, childChain, depth+1); err != nil {
				return err
			}

		default:
			return closureError(repository, workspacePath, fmt.Errorf("%w: unknown tree entry kind %s", ErrInvalidObject, entry.Kind), "walk tree")
		}
	}
	return nil
}

func (s *buildState) readObject(repository RepositoryID, digest Digest, expected Kind, path string) (Object, error) {
	if digest.IsZero() {
		return Object{}, closureError(repository, path, fmt.Errorf("%w: zero digest", ErrInvalidObject), "read object")
	}
	object, err := s.store.Get(repository, digest)
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			return Object{}, closureError(repository, path, fmt.Errorf("%w: digest=%s", ErrObjectNotFound, digest), "read object")
		}
		return Object{}, closureError(repository, path, err, "read object")
	}
	if object.Type != expected {
		return Object{}, closureError(repository, path, fmt.Errorf("%w: digest=%s is %s, want %s", ErrInvalidObject, digest, object.Type, expected), "read object")
	}
	actual, err := ObjectDigest(object)
	if err != nil {
		return Object{}, closureError(repository, path, err, "read object")
	}
	if actual != digest {
		return Object{}, closureError(repository, path, fmt.Errorf("%w: requested=%s actual=%s", ErrDigestMismatch, digest, actual), "read object")
	}
	return object, nil
}

func (s *buildState) trustedClosure(repository RepositoryID) (map[Digest]struct{}, error) {
	if closure, ok := s.closures[repository]; ok {
		return closure, nil
	}
	policy, ok := s.policy.repository(repository)
	if !ok {
		return nil, closureError(repository, "", ErrInvalidPolicy, "trust closure")
	}
	if len(policy.trustedRoots) == 0 {
		return nil, closureError(repository, "", fmt.Errorf("%w: no trusted roots", ErrUntrustedCommit), "trust closure")
	}
	closure := make(map[Digest]struct{})
	stack := append([]Digest(nil), policy.trustedRoots...)
	for len(stack) > 0 {
		digest := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := closure[digest]; seen {
			continue
		}
		object, err := s.readObject(repository, digest, KindCommit, "")
		if err != nil {
			return nil, err
		}
		commit, err := ParseCommit(object)
		if err != nil {
			return nil, closureError(repository, "", err, "trust closure")
		}
		closure[digest] = struct{}{}
		stack = append(stack, commit.Parents...)
	}
	s.closures[repository] = closure
	return closure, nil
}

type pathRegistry struct {
	leaves map[string]bool
	dirs   map[string]bool
}

func newPathRegistry() *pathRegistry {
	return &pathRegistry{
		leaves: make(map[string]bool),
		dirs:   make(map[string]bool),
	}
}

func properPrefixes(value string) []string {
	parts := strings.Split(value, "/")
	prefixes := make([]string, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		prefixes = append(prefixes, strings.Join(parts[:i], "/"))
	}
	return prefixes
}

func (p *pathRegistry) addDir(path string) error {
	if path == "" {
		return nil
	}
	if _, err := normalizeRelativePath(path, false); err != nil {
		return err
	}
	if _, ok := p.leaves[path]; ok {
		return fmt.Errorf("%w: %q is a file or symlink", ErrPathConflict, path)
	}
	for _, prefix := range properPrefixes(path) {
		if _, ok := p.leaves[prefix]; ok {
			return fmt.Errorf("%w: %q is a file or symlink below a directory", ErrPathConflict, prefix)
		}
	}
	p.dirs[path] = true
	for _, prefix := range properPrefixes(path) {
		p.dirs[prefix] = true
	}
	return nil
}

func (p *pathRegistry) addLeaf(path string, symlink bool) error {
	if _, err := normalizeRelativePath(path, false); err != nil {
		return err
	}
	if _, ok := p.leaves[path]; ok {
		return fmt.Errorf("%w: %q already has a source", ErrPathConflict, path)
	}
	if p.dirs[path] {
		return fmt.Errorf("%w: %q is a directory", ErrPathConflict, path)
	}
	for _, prefix := range properPrefixes(path) {
		if _, ok := p.leaves[prefix]; ok {
			return fmt.Errorf("%w: %q is a file or symlink used as a parent directory", ErrPathConflict, prefix)
		}
	}
	p.leaves[path] = symlink
	for _, prefix := range properPrefixes(path) {
		p.dirs[prefix] = true
	}
	return nil
}
