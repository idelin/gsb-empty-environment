package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// SubmoduleRule declares that a gitlink at Path in the containing repository
// belongs to Repository. Path is repository-relative and must match the
// gitlink entry exactly.
type SubmoduleRule struct {
	Path       string
	Repository RepositoryID
}

// RepositoryPolicy is the caller-declared trust information for one
// repository identity. TrustedRoots are commits from which the host-provided
// parent graph is walked. Submodules maps an allowed gitlink path to the child
// repository identity. SubmoduleRules is an alternate representation for
// callers that prefer a slice.
type RepositoryPolicy struct {
	TrustedRoots   []Digest
	Submodules     map[string]RepositoryID
	SubmoduleRules []SubmoduleRule
}

// TrustManifest is the caller-supplied mapping from repository identities to
// allowed gitlink paths and trusted roots. It is data only; it does not list
// files and it is never read from the checked-out workspace.
type TrustManifest struct {
	Repositories map[RepositoryID]RepositoryPolicy
}

// Policy is an alias for TrustManifest.
type Policy = TrustManifest

// RepoPolicy is an alias for RepositoryPolicy.
type RepoPolicy = RepositoryPolicy

func normalizeRelativePath(value string, allowEmpty bool) (string, error) {
	if value == "" {
		if allowEmpty {
			return "", nil
		}
		return "", fmt.Errorf("%w: empty relative path", ErrInvalidPath)
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%w: absolute path %q", ErrInvalidPath, value)
	}
	parts := strings.Split(value, "/")
	for _, part := range parts {
		if err := validateComponent(part); err != nil {
			return "", err
		}
	}
	return strings.Join(parts, "/"), nil
}

// NormalizeRepoPath validates a repository-relative path and returns its
// canonical slash-separated form.
func NormalizeRepoPath(value string) (string, error) {
	return normalizeRelativePath(value, false)
}

type compiledRepository struct {
	trustedRoots []Digest
	rules        map[string]RepositoryID
}

type compiledPolicy struct {
	repositories map[RepositoryID]compiledRepository
}

func compileTrustManifest(manifest TrustManifest) (*compiledPolicy, error) {
	if manifest.Repositories == nil {
		return nil, fmt.Errorf("%w: no repositories declared", ErrInvalidPolicy)
	}
	compiled := &compiledPolicy{repositories: make(map[RepositoryID]compiledRepository, len(manifest.Repositories))}
	for repository, policy := range manifest.Repositories {
		if err := validateRepositoryID(repository); err != nil {
			return nil, err
		}
		roots := make([]Digest, 0, len(policy.TrustedRoots))
		seenRoots := make(map[Digest]struct{}, len(policy.TrustedRoots))
		for _, root := range policy.TrustedRoots {
			if root.IsZero() {
				return nil, fmt.Errorf("%w: repository %q has a zero trusted root", ErrInvalidPolicy, repository)
			}
			if _, seen := seenRoots[root]; seen {
				continue
			}
			seenRoots[root] = struct{}{}
			roots = append(roots, root)
		}
		rules := make(map[string]RepositoryID, len(policy.Submodules)+len(policy.SubmoduleRules))
		addRule := func(path string, child RepositoryID) error {
			canonical, err := normalizeRelativePath(path, false)
			if err != nil {
				return fmt.Errorf("%w: repository %q submodule path: %v", ErrInvalidPolicy, repository, err)
			}
			if err := validateRepositoryID(child); err != nil {
				return fmt.Errorf("%w: repository %q submodule target: %v", ErrInvalidPolicy, repository, err)
			}
			if previous, exists := rules[canonical]; exists {
				if previous != child {
					return fmt.Errorf("%w: repository %q path %q maps to both %q and %q", ErrInvalidPolicy, repository, canonical, previous, child)
				}
				return nil
			}
			rules[canonical] = child
			return nil
		}
		for path, child := range policy.Submodules {
			if err := addRule(path, child); err != nil {
				return nil, err
			}
		}
		for _, rule := range policy.SubmoduleRules {
			if err := addRule(rule.Path, rule.Repository); err != nil {
				return nil, err
			}
		}
		compiled.repositories[repository] = compiledRepository{
			trustedRoots: roots,
			rules:        rules,
		}
	}
	// Every gitlink target must itself be declared before a build can use it.
	for repository, repo := range compiled.repositories {
		for path, child := range repo.rules {
			if _, ok := compiled.repositories[child]; !ok {
				return nil, fmt.Errorf("%w: repository %q path %q targets undeclared repository %q", ErrInvalidPolicy, repository, path, child)
			}
		}
	}
	return compiled, nil
}

func (p *compiledPolicy) repository(id RepositoryID) (compiledRepository, bool) {
	if p == nil {
		return compiledRepository{}, false
	}
	repo, ok := p.repositories[id]
	return repo, ok
}

func (r compiledRepository) childFor(path string) (RepositoryID, bool) {
	child, ok := r.rules[path]
	return child, ok
}

// SubmodulePaths returns the allowed gitlink paths in stable byte order.
func (p RepositoryPolicy) SubmodulePaths() []string {
	paths := make([]string, 0, len(p.Submodules)+len(p.SubmoduleRules))
	for path := range p.Submodules {
		paths = append(paths, path)
	}
	for _, rule := range p.SubmoduleRules {
		paths = append(paths, rule.Path)
	}
	sort.Strings(paths)
	out := paths[:0]
	for _, path := range paths {
		if len(out) == 0 || out[len(out)-1] != path {
			out = append(out, path)
		}
	}
	return out
}
