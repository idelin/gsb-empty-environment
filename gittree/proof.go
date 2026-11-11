package gittree

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrInvalidProof indicates a proof is structurally unusable.
	ErrInvalidProof = errors.New("gittree: invalid membership proof")
	// ErrNameMismatch indicates a proof step names an entry absent from the
	// referenced tree.
	ErrNameMismatch = errors.New("gittree: path name not found in tree")
	// ErrNotDirectory indicates a path component is expected to be a directory
	// but the tree entry is some other object type.
	ErrNotDirectory = errors.New("gittree: path component is not a directory")
	// ErrSymlinkNotFile indicates a regular file was requested but the leaf
	// entry is a symbolic link.
	ErrSymlinkNotFile = errors.New("gittree: symbolic link cannot serve as a regular file")
	// ErrGitlink indicates a path enters a submodule gitlink, which cannot be
	// traversed through the superproject's object store.
	ErrGitlink = errors.New("gittree: submodule gitlink cannot be traversed")
	// ErrObjectType indicates an object's advertised type does not match the
	// type required by its tree entry mode.
	ErrObjectType = errors.New("gittree: object type mismatch")
	// ErrAlgorithmMismatch indicates proof objects mix SHA-1 and SHA-256 IDs.
	ErrAlgorithmMismatch = errors.New("gittree: mixed hash algorithms in proof")
)

// PathStep is one hop of a membership proof: within the tree identified by
// TreeID, an entry named Name carries the given Mode and object ID.
type PathStep struct {
	// Name is the exact path component in the parent tree.
	Name string
	// TreeID is the tree object containing this entry.
	TreeID ObjectID
}

// MembershipProof proves that one object exists under RootID along Path.
//
// Path holds one step per path component, from the root tree down to the
// leaf entry. The leaf's object ID and mode are read from the last step's
// parent tree, so the proof never trusts self-declared leaf metadata.
type MembershipProof struct {
	// RootID is the trusted tree (or commit-resolved tree) object ID.
	RootID ObjectID
	// Path lists the hops from RootID to the proven object.
	Path []PathStep
}

// validate checks the proof's own shape and the consistency of its IDs. It
// does not touch the object source and never mutates the caller's proof:
// the returned steps are a private, normalized copy.
func (p *MembershipProof) validate() (ObjectID, []PathStep, Algorithm, error) {
	root, algo, err := ParseID(string(p.RootID))
	if err != nil {
		return "", nil, 0, fmt.Errorf("%w: root id: %v", ErrInvalidProof, err)
	}
	if len(p.Path) == 0 {
		return "", nil, 0, fmt.Errorf("%w: empty path", ErrInvalidProof)
	}
	steps := make([]PathStep, len(p.Path))
	for i, step := range p.Path {
		if err := ValidateName(step.Name); err != nil {
			return "", nil, 0, fmt.Errorf("%w: step %d: %v", ErrInvalidProof, i, err)
		}
		treeID, stepAlgo, err := ParseID(string(step.TreeID))
		if err != nil {
			return "", nil, 0, fmt.Errorf("%w: step %d tree id: %v", ErrInvalidProof, i, err)
		}
		if stepAlgo != algo {
			return "", nil, 0, fmt.Errorf("%w: step %d", ErrAlgorithmMismatch, i)
		}
		steps[i] = PathStep{Name: step.Name, TreeID: treeID}
	}
	return root, steps, algo, nil
}

// EntryResult describes a successfully verified tree entry.
type EntryResult struct {
	// Name is the leaf path component.
	Name string
	// Mode is the mode recorded for the leaf entry.
	Mode Mode
	// ID is the verified content digest of the leaf object.
	ID ObjectID
	// Algo is the hash algorithm used throughout the proof.
	Algo Algorithm
	// Content is the verified raw content of a blob leaf. It is nil for
	// tree leaves.
	Content []byte
	// IsTree reports whether the leaf is itself a directory tree.
	IsTree bool
}

// Stats summarizes verifier cache activity.
type Stats struct {
	// TreesCached is the number of verified trees currently held.
	TreesCached int
	// TreeHits counts tree loads served from the cache.
	TreeHits uint64
	// TreeMisses counts tree loads that required fetching and verification.
	TreeMisses uint64
}

// Verifier checks membership proofs against an ObjectSource. It is safe for
// concurrent use: every cached tree has independently passed digest
// recomputation and structural validation before insertion, so a failed or
// corrupted proof can never populate the cache for another proof to reuse.
type Verifier struct {
	source ObjectSource

	mu    sync.RWMutex
	trees map[ObjectID]*Tree
	hits  uint64
	miss  uint64
}

// NewVerifier builds a Verifier backed by source.
func NewVerifier(source ObjectSource) *Verifier {
	return &Verifier{
		source: source,
		trees:  make(map[ObjectID]*Tree),
	}
}

// Stats returns a snapshot of cache counters.
func (v *Verifier) Stats() Stats {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return Stats{TreesCached: len(v.trees), TreeHits: v.hits, TreeMisses: v.miss}
}

// loadTree returns a verified tree, using the cache only after a fresh
// object has been re-hashed and fully parsed.
func (v *Verifier) loadTree(ctx context.Context, id ObjectID) (*Tree, error) {
	v.mu.RLock()
	cached, ok := v.trees[id]
	v.mu.RUnlock()
	if ok {
		v.mu.Lock()
		v.hits++
		v.mu.Unlock()
		return cached, nil
	}

	v.mu.Lock()
	v.miss++
	v.mu.Unlock()

	obj, err := v.source.Object(ctx, id)
	if err != nil {
		return nil, err
	}
	if obj.Type != TypeTree {
		return nil, fmt.Errorf("%w: %s advertised as %s", ErrObjectType, id, obj.Type)
	}
	// ParseTree recomputes the digest and rejects malformed or ambiguous
	// trees. Nothing is cached on any error path.
	tree, err := ParseTree(id, obj.Content)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	// Another goroutine may have verified the same tree concurrently.
	if existing, ok := v.trees[id]; ok {
		v.mu.Unlock()
		return existing, nil
	}
	v.trees[id] = tree
	v.mu.Unlock()
	return tree, nil
}

// loadBlob fetches a blob entry and proves its content digest. The result is
// not cached: blobs are leaf objects and caching them would share unverified
// state across proofs without any traversal benefit.
func (v *Verifier) loadBlob(ctx context.Context, id ObjectID) ([]byte, error) {
	obj, err := v.source.Object(ctx, id)
	if err != nil {
		return nil, err
	}
	if obj.Type != TypeBlob {
		return nil, fmt.Errorf("%w: %s advertised as %s", ErrObjectType, id, obj.Type)
	}
	if _, err := verifyDigest(id, TypeBlob, obj.Content); err != nil {
		return nil, err
	}
	return obj.Content, nil
}

// VerifyEntry proves the membership of the object addressed by proof and
// returns its verified name, mode and digest. It accepts any non-gitlink
// entry type, including symbolic links and subtrees; callers that require a
// regular file should use VerifyFile instead.
func (v *Verifier) VerifyEntry(ctx context.Context, proof *MembershipProof) (*EntryResult, error) {
	rootID, steps, algo, err := proof.validate()
	if err != nil {
		return nil, err
	}

	currentID := rootID
	last := len(steps) - 1
	for i, step := range steps {
		if step.TreeID != currentID {
			return nil, fmt.Errorf("%w: step %d references tree %s but chain is at %s",
				ErrInvalidProof, i, step.TreeID, currentID)
		}

		tree, err := v.loadTree(ctx, currentID)
		if err != nil {
			return nil, fmt.Errorf("loading tree %s: %w", currentID, err)
		}

		entry := findEntry(tree, step.Name)
		if entry == nil {
			return nil, fmt.Errorf("%w: %q in tree %s", ErrNameMismatch, step.Name, currentID)
		}
		entryAlgo, err := entry.ID.Algorithm()
		if err != nil {
			return nil, err
		}
		if entryAlgo != algo {
			return nil, fmt.Errorf("%w: entry %q", ErrAlgorithmMismatch, step.Name)
		}

		if i < last {
			// Intermediate components must be traversable directories.
			switch {
			case entry.Mode.IsGitlink():
				return nil, fmt.Errorf("%w: %q", ErrGitlink, step.Name)
			case entry.Mode == ModeSymlink:
				return nil, fmt.Errorf("%w: symlink %q used as directory", ErrNotDirectory, step.Name)
			case !entry.Mode.IsTree():
				return nil, fmt.Errorf("%w: %q mode %s", ErrNotDirectory, step.Name, entry.Mode)
			}
			// The next iteration verifies this object's digest and parses it.
			currentID = entry.ID
			continue
		}

		// Leaf: verify the object the tree actually points at.
		result := &EntryResult{
			Name:   entry.Name,
			Mode:   entry.Mode,
			ID:     entry.ID,
			Algo:   algo,
			IsTree: entry.Mode.IsTree(),
		}
		switch {
		case entry.Mode.IsGitlink():
			return nil, fmt.Errorf("%w: %q", ErrGitlink, step.Name)
		case entry.Mode.IsTree():
			// Prove the subtree object exists with the right digest and type.
			if _, err := v.loadTree(ctx, entry.ID); err != nil {
				return nil, fmt.Errorf("loading subtree %s: %w", entry.ID, err)
			}
		default:
			content, err := v.loadBlob(ctx, entry.ID)
			if err != nil {
				return nil, fmt.Errorf("loading blob %s: %w", entry.ID, err)
			}
			result.Content = content
		}
		return result, nil
	}
	return nil, fmt.Errorf("%w: empty path", ErrInvalidProof)
}

// VerifyFile proves membership and additionally requires the leaf to be a
// regular (non-symlink) file. A symbolic link, directory or gitlink at the
// leaf position is rejected even if the surrounding path verifies.
func (v *Verifier) VerifyFile(ctx context.Context, proof *MembershipProof) (*EntryResult, error) {
	result, err := v.VerifyEntry(ctx, proof)
	if err != nil {
		return nil, err
	}
	if !result.Mode.IsRegularFile() {
		switch {
		case result.Mode == ModeSymlink:
			return nil, fmt.Errorf("%w: %q is a symbolic link", ErrSymlinkNotFile, result.Name)
		case result.Mode.IsGitlink():
			return nil, fmt.Errorf("%w: %q is a gitlink", ErrGitlink, result.Name)
		default:
			return nil, fmt.Errorf("%w: %q mode %s is not a regular file", ErrSymlinkNotFile, result.Name, result.Mode)
		}
	}
	return result, nil
}

func findEntry(tree *Tree, name string) *Entry {
	for i := range tree.Entries {
		if tree.Entries[i].Name == name {
			return &tree.Entries[i]
		}
	}
	return nil
}
