// Package netsec validates the signed Git notes used by the release gate.
//
// A notes tree maps a target commit ID to a blob whose first line is an
// approval header.  The validation in this package treats both the tree and
// the commit signatures as untrusted input: a note is accepted only when the
// exact blob currently stored at the note path was introduced by a verified
// commit from a signer explicitly trusted by the caller.
package netsec

import (
	"bytes"
	"net/mail"
	"sort"
	"strings"
)

// Reasons reported for notes that are not accepted.
const (
	ReasonInvalidPath        = "notes path is not 40 lowercase hexadecimal digits"
	ReasonConflictingPaths   = "multiple notes paths refer to the same target commit"
	ReasonMissingBlob        = "notes path has no blob content"
	ReasonInvalidApproval    = "first line is not Approved-by: email"
	ReasonNoIntroduction     = "current blob was not introduced at this path"
	ReasonUnverifiedCommit   = "introducing commit signature was not verified"
	ReasonUnauthorizedSigner = "introducing commit signer is not authorized"
)

// Entry is one path/content pair in a notes tree.
type Entry struct {
	Path    string
	Content []byte
}

// Tree is a snapshot of a notes tree.  Paths is the authoritative path list
// when supplied.  Entries and Blobs are convenience alternatives; a path may
// be described by any of them.
type Tree struct {
	Paths   []string
	Blobs   map[string][]byte
	Entries []Entry
}

// Commit is one commit in the notes branch history.  Tree must contain the
// complete notes tree snapshot for that commit.  The history passed to
// Validate is ordered oldest first.
type Commit struct {
	ID       string
	Signer   string
	Verified bool
	Tree     Tree

	// Paths and Blobs are accepted as a shorthand for Tree when Tree itself is
	// empty.  They make it possible to pass commit snapshots without wrapping
	// them in a Tree value.
	Paths []string
	Blobs map[string][]byte
}

// Note is an accepted note.
type Note struct {
	Target     string
	Path       string
	Content    []byte
	ApprovedBy string
}

// Rejection describes a path that was not accepted.
type Rejection struct {
	Target string
	Path   string
	Reason string
}

// Conflict records all path spellings that were found for one target commit.
// No note for that target is accepted.
type Conflict struct {
	Target string
	Paths  []string
}

// Result is the outcome of validation.  Accepted contains only notes that
// passed every check.  Rejected contains individual path failures, while
// Conflicts records target-wide invalidations.
type Result struct {
	Accepted  []Note
	Rejected  []Rejection
	Conflicts []Conflict
}

// AcceptedNote returns the accepted note for target, if any.
func (r Result) AcceptedNote(target string) (Note, bool) {
	for _, note := range r.Accepted {
		if note.Target == target {
			return note, true
		}
	}
	return Note{}, false
}

// IsAccepted reports whether target has an accepted note.
func (r Result) IsAccepted(target string) bool {
	_, ok := r.AcceptedNote(target)
	return ok
}

// Validate checks the notes branch history against the current notes tree.
//
// history must be ordered oldest first.  latest is the tree of the newest
// commit.  If latest is empty, the last commit's tree is used instead.  A
// commit is trusted only when Verified is true and its Signer appears exactly
// in authorizedSigners.
func Validate(history []Commit, latest Tree, authorizedSigners []string) Result {
	authorized := make(map[string]struct{}, len(authorizedSigners))
	for _, signer := range authorizedSigners {
		if signer != "" {
			authorized[signer] = struct{}{}
		}
	}

	current := latest
	latestIsExplicit := treeHasContent(current)
	if !latestIsExplicit && len(history) != 0 {
		current = history[len(history)-1].snapshot()
	}

	paths := treePaths(current)
	sort.Strings(paths)

	var result Result
	byTarget := make(map[string][]candidate)

	for _, path := range paths {
		target, ok := TargetID(path)
		if !ok {
			result.Rejected = append(result.Rejected, Rejection{
				Path:   path,
				Reason: ReasonInvalidPath,
			})
			continue
		}
		byTarget[target] = append(byTarget[target], candidate{
			path:   path,
			target: target,
		})
	}

	targets := make([]string, 0, len(byTarget))
	for target := range byTarget {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	for _, target := range targets {
		candidates := byTarget[target]
		if len(candidates) > 1 {
			conflictPaths := make([]string, 0, len(candidates))
			for _, c := range candidates {
				conflictPaths = append(conflictPaths, c.path)
				result.Rejected = append(result.Rejected, Rejection{
					Target: target,
					Path:   c.path,
					Reason: ReasonConflictingPaths,
				})
			}
			sort.Strings(conflictPaths)
			result.Conflicts = append(result.Conflicts, Conflict{
				Target: target,
				Paths:  conflictPaths,
			})
			continue
		}

		c := candidates[0]
		content, ok := treeContent(current, c.path)
		if !ok {
			result.Rejected = append(result.Rejected, Rejection{
				Target: target,
				Path:   c.path,
				Reason: ReasonMissingBlob,
			})
			continue
		}

		approvedBy, ok := parseApproval(content)
		if !ok {
			result.Rejected = append(result.Rejected, Rejection{
				Target: target,
				Path:   c.path,
				Reason: ReasonInvalidApproval,
			})
			continue
		}

		introducing, ok := introducingCommit(history, current, latestIsExplicit, c.path, content)
		if !ok {
			result.Rejected = append(result.Rejected, Rejection{
				Target: target,
				Path:   c.path,
				Reason: ReasonNoIntroduction,
			})
			continue
		}
		if !introducing.Verified {
			result.Rejected = append(result.Rejected, Rejection{
				Target: target,
				Path:   c.path,
				Reason: ReasonUnverifiedCommit,
			})
			continue
		}
		if _, ok := authorized[introducing.Signer]; !ok {
			result.Rejected = append(result.Rejected, Rejection{
				Target: target,
				Path:   c.path,
				Reason: ReasonUnauthorizedSigner,
			})
			continue
		}

		result.Accepted = append(result.Accepted, Note{
			Target:     target,
			Path:       c.path,
			Content:    cloneBytes(content),
			ApprovedBy: approvedBy,
		})
	}

	sort.Slice(result.Accepted, func(i, j int) bool {
		if result.Accepted[i].Target != result.Accepted[j].Target {
			return result.Accepted[i].Target < result.Accepted[j].Target
		}
		return result.Accepted[i].Path < result.Accepted[j].Path
	})
	sort.Slice(result.Rejected, func(i, j int) bool {
		if result.Rejected[i].Target != result.Rejected[j].Target {
			return result.Rejected[i].Target < result.Rejected[j].Target
		}
		if result.Rejected[i].Path != result.Rejected[j].Path {
			return result.Rejected[i].Path < result.Rejected[j].Path
		}
		return result.Rejected[i].Reason < result.Rejected[j].Reason
	})

	return result
}

// ValidateChain is Validate using the last commit's tree as the current tree.
func ValidateChain(history []Commit, authorizedSigners []string) Result {
	return Validate(history, Tree{}, authorizedSigners)
}

// ValidateTree is like Validate, but takes the current tree as a path list and
// a path-to-blob map.
func ValidateTree(history []Commit, paths []string, blobs map[string][]byte, authorizedSigners []string) Result {
	return Validate(history, Tree{Paths: paths, Blobs: blobs}, authorizedSigners)
}

// ValidateNotes is an alias for Validate.
func ValidateNotes(history []Commit, latest Tree, authorizedSigners []string) Result {
	return Validate(history, latest, authorizedSigners)
}

// CheckNotes is an alias for Validate.
func CheckNotes(history []Commit, latest Tree, authorizedSigners []string) Result {
	return Validate(history, latest, authorizedSigners)
}

// TargetID returns the target commit ID encoded by a notes path.  All slashes
// are removed, and the remaining value must be exactly 40 lowercase
// hexadecimal digits.
func TargetID(path string) (string, bool) {
	flat := strings.ReplaceAll(path, "/", "")
	if len(flat) != 40 {
		return "", false
	}
	for i := 0; i < len(flat); i++ {
		if !isLowerHex(flat[i]) {
			return "", false
		}
	}
	return flat, true
}

// parseApproval requires the first line to be exactly
// "Approved-by: <mailbox>".
func parseApproval(content []byte) (string, bool) {
	if len(content) == 0 {
		return "", false
	}

	line := content
	if i := bytes.IndexByte(content, '\n'); i >= 0 {
		line = content[:i]
	}
	line = bytes.TrimSuffix(line, []byte{'\r'})

	const prefix = "Approved-by: "
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return "", false
	}

	value := string(line[len(prefix):])
	if value == "" || strings.TrimSpace(value) != value {
		return "", false
	}

	address, err := mail.ParseAddress(value)
	if err != nil || address.Name != "" || address.Address != value {
		return "", false
	}
	return value, true
}

type candidate struct {
	path   string
	target string
}

func introducingCommit(history []Commit, latest Tree, latestIsExplicit bool, path string, content []byte) (Commit, bool) {
	for i, commit := range history {
		snapshot := commit.snapshot()
		if latestIsExplicit && i == len(history)-1 {
			snapshot = latest
		}
		if candidateContent, ok := treeContent(snapshot, path); ok && bytes.Equal(candidateContent, content) {
			return commit, true
		}
	}
	return Commit{}, false
}

func (c Commit) snapshot() Tree {
	if treeHasContent(c.Tree) {
		return c.Tree
	}
	return Tree{Paths: c.Paths, Blobs: c.Blobs}
}

func treeHasContent(tree Tree) bool {
	return len(tree.Paths) != 0 || len(tree.Blobs) != 0 || len(tree.Entries) != 0
}

func treePaths(tree Tree) []string {
	seen := make(map[string]struct{})
	var paths []string

	add := func(path string) {
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	if len(tree.Paths) != 0 {
		for _, path := range tree.Paths {
			add(path)
		}
		return paths
	}
	if len(tree.Entries) != 0 {
		for _, entry := range tree.Entries {
			add(entry.Path)
		}
		return paths
	}
	for path := range tree.Blobs {
		add(path)
	}
	return paths
}

func treeContent(tree Tree, path string) ([]byte, bool) {
	if tree.Blobs != nil {
		if content, ok := tree.Blobs[path]; ok {
			return content, true
		}
	}
	for _, entry := range tree.Entries {
		if entry.Path == path {
			return entry.Content, true
		}
	}
	return nil, false
}

func isLowerHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	dst := make([]byte, len(b))
	copy(dst, b)
	return dst
}
