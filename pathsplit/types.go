package pathsplit

import (
	"errors"
	"path"
	"strings"
)

// EntryKind describes the type of a virtual file-system entry.
type EntryKind int

const (
	// KindFile is a regular file.
	KindFile EntryKind = iota
	// KindDir is a directory.
	KindDir
	// KindSymlink is a symbolic link. Entry.Target is its link target.
	KindSymlink
)

// Entry is a single node in the virtual site-root listing.
//
// For KindSymlink entries, Target is interpreted like a POSIX symlink: a
// root-relative absolute target such as "/realdir/script.php" is resolved from
// the site root, while a relative target such as "../index.php" is resolved
// against the directory that contains the symlink.
type Entry struct {
	Kind   EntryKind
	Target string
}

// FS is the virtual read-only view of the site root. Lookup receives a
// root-relative, cleaned path (for example "/app/index.php") and reports the
// entry and whether it exists.
type FS interface {
	Lookup(virtualPath string) (Entry, bool)
}

// MapFS is a simple FS backed by a map keyed by root-relative virtual paths.
type MapFS map[string]Entry

// Lookup implements FS.
func (m MapFS) Lookup(virtualPath string) (Entry, bool) {
	entry, ok := m[normalizeKey(virtualPath)]
	return entry, ok
}

func normalizeKey(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

// Config holds the inputs used by Split.
type Config struct {
	// FS is the virtual listing of the site root, including symlinks.
	FS FS
	// ScriptDirs lists root-relative directories that are allowed to contain
	// executable PHP scripts.
	ScriptDirs []string
	// UploadDirs lists root-relative directories whose contents must never be
	// executed as scripts, even when a file name ends in ".php".
	UploadDirs []string
}

// Result describes a successful split.
type Result struct {
	// ScriptFilename is the root-relative virtual path of the chosen script.
	// A gateway typically joins its physical document root with ResolvedPath to
	// build the concrete SCRIPT_FILENAME passed to PHP-FPM.
	ScriptFilename string
	// ScriptName is the URL path prefix that identified the script. It equals
	// ScriptFilename.
	ScriptName string
	// PathInfo is the decoded, leading-slash portion following the script, or
	// empty when there is none.
	PathInfo string
	// ResolvedPath is the root-relative path after recursively resolving every
	// intermediate and trailing symlink.
	ResolvedPath string
}

// Sentinel errors returned by Split. Callers should use errors.Is.
var (
	// ErrMalformedPath is returned for percent-decoding failures or unsafe
	// request paths containing NUL bytes (%00), ".." segments or backslashes.
	ErrMalformedPath = errors.New("pathsplit: malformed or unsafe request path")
	// ErrUploadDenied is returned when a candidate lives in an upload
	// directory or resolves into one.
	ErrUploadDenied = errors.New("pathsplit: execution inside an upload directory is forbidden")
	// ErrSymlinkLoop is returned when symlink resolution visits the same
	// symlink more than once.
	ErrSymlinkLoop = errors.New("pathsplit: symlink loop detected")
	// ErrOutsideRoot is returned when symlink resolution escapes the site root.
	ErrOutsideRoot = errors.New("pathsplit: resolved path escapes the site root")
)

// errMissing is an internal marker for an unresolvable (missing or dangling)
// candidate; such candidates are skipped rather than treated as hard errors.
var errMissing = errors.New("pathsplit: virtual path does not resolve to an entry")
