package kustomization

import (
	"path"
	"strings"
)

// fileIndex provides lookup over the repository file listing.
type fileIndex struct {
	entries map[string]FileEntry
	rootDir string
}

func newFileIndex(files []FileEntry, rootDir string) *fileIndex {
	idx := &fileIndex{
		entries: make(map[string]FileEntry, len(files)),
		rootDir: rootDir,
	}
	for _, f := range files {
		idx.entries[normalizeRepoPath(f.Path)] = f
	}
	return idx
}

// normalizeRepoPath turns a slash-separated repository-relative path into its
// clean lexical form without a leading "./". The empty path stays empty.
func normalizeRepoPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return strings.TrimPrefix(p, "./")
}

// withinRoot reports whether cleanRepoPath is the root or nested inside it.
func withinRoot(cleanRepoPath string) bool {
	return cleanRepoPath == "" || cleanRepoPath == "/" ||
		(!strings.HasPrefix(cleanRepoPath, "../") && cleanRepoPath != "..")
}

// resolveLocal lexicalizes ref relative to baseDir and then follows every
// symlink encountered. The returned path is always the final repository-root
// relative location, even when intermediate links were traversed.
func (idx *fileIndex) resolveLocal(baseDir, ref, field string) (string, []Violation) {
	if ref == "" {
		return "", []Violation{{Field: field, Value: ref, Reason: "empty local reference"}}
	}
	if strings.HasPrefix(ref, "/") {
		return "", []Violation{{Field: field, Value: ref, Reason: "absolute paths are not allowed; references must stay inside the repository root"}}
	}

	clean := normalizeRepoPath(path.Join(baseDir, ref))
	if !withinRoot(clean) {
		return "", []Violation{{Field: field, Value: ref, Reason: "reference escapes the repository root"}}
	}

	seen := map[string]bool{}
	current := clean
	for {
		entry, known := idx.entries[current]
		if !known || !entry.IsSymlink {
			return current, nil
		}
		if seen[current] {
			return "", []Violation{{Field: field, Value: ref, Reason: "symlink loop detected at " + current}}
		}
		seen[current] = true

		target := strings.TrimSpace(entry.LinkTarget)
		if target == "" {
			return "", []Violation{{Field: field, Value: ref, Reason: "dangling symlink at " + current}}
		}
		if strings.HasPrefix(target, "/") {
			if idx.rootDir == "" {
				return "", []Violation{{Field: field, Value: ref, Reason: "absolute symlink target at " + current + " cannot be validated without a repository root directory"}}
			}
			root := strings.ReplaceAll(idx.rootDir, "\\", "/")
			abs := strings.ReplaceAll(target, "\\", "/")
			rel, ok := stripRoot(root, abs)
			if !ok {
				return "", []Violation{{Field: field, Value: ref, Reason: "symlink at " + current + " points outside the repository root"}}
			}
			current = normalizeRepoPath(rel)
		} else {
			current = normalizeRepoPath(path.Join(path.Dir(current), target))
		}
		if !withinRoot(current) {
			return "", []Violation{{Field: field, Value: ref, Reason: "symlink resolution escapes the repository root"}}
		}
	}
}

// stripRoot returns the path inside root when abs is root itself or nested
// beneath it. Both arguments use forward slashes.
func stripRoot(root, abs string) (string, bool) {
	root = strings.TrimSuffix(root, "/")
	if abs == root {
		return "", true
	}
	prefix := root + "/"
	if strings.HasPrefix(abs, prefix) {
		return strings.TrimPrefix(abs, prefix), true
	}
	return "", false
}
