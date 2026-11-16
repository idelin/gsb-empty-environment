// Package sudoedit embeds a sudoedit authorization decision into a
// privilege escalation audit component.
//
// sudoedit selects its editor from SUDO_EDITOR, VISUAL or EDITOR and splits
// the selected value on whitespace. A crafted value such as
// "vim -- /etc/shadow" can therefore smuggle an extra, unauthorized file into
// the editor invocation. Authorize reproduces sudoedit's selection and
// splitting rules first, and only then verifies every resulting file against
// the sudoers authorization set and the caller's filesystem permissions.
package sudoedit

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Reason is the machine-readable cause of a denied authorization decision.
// The zero value means the request was allowed.
type Reason string

const (
	// ReasonNoEditor means none of SUDO_EDITOR, VISUAL or EDITOR is set.
	ReasonNoEditor Reason = "no_editor"
	// ReasonEditorNotAbsolute means the editor executable is a relative name,
	// which would require a PATH lookup.
	ReasonEditorNotAbsolute Reason = "editor_not_absolute"
	// ReasonEditorNotWhitelisted means the absolute editor is not in the
	// caller-supplied whitelist.
	ReasonEditorNotWhitelisted Reason = "editor_not_whitelisted"
	// ReasonPathNotAbsolute means a target file was given as a relative path.
	ReasonPathNotAbsolute Reason = "path_not_absolute"
	// ReasonPathTraversal means a path contains a ".." segment before
	// normalization.
	ReasonPathTraversal Reason = "path_traversal"
	// ReasonUnauthorizedFile means a file is not covered by the sudoers
	// authorization set.
	ReasonUnauthorizedFile Reason = "unauthorized_file"
	// ReasonSymlink means the file or one of its parent directories is a
	// symbolic link.
	ReasonSymlink Reason = "symlink"
	// ReasonWritable means the file or one of its parent directories is
	// writable by the calling user.
	ReasonWritable Reason = "writable"
	// ReasonParentMissing means a parent directory required for the check
	// does not exist.
	ReasonParentMissing Reason = "parent_missing"
	// ReasonStatError means an unexpected filesystem error occurred.
	ReasonStatError Reason = "stat_error"
	// ReasonNoTargetFiles means neither arguments nor editor parameters name
	// any file to edit.
	ReasonNoTargetFiles Reason = "no_target_files"
)

// Caller identifies the unprivileged user invoking sudoedit.
type Caller struct {
	UID    int
	GID    int
	Groups []int
}

// CurrentCaller builds a Caller from the running process credentials.
func CurrentCaller() Caller {
	caller := Caller{UID: os.Getuid(), GID: os.Getgid()}
	if groups, err := os.Getgroups(); err == nil {
		caller.Groups = groups
	}
	return caller
}

// Entry is the subset of file metadata the decision relies on.
type Entry struct {
	Mode     fs.FileMode
	OwnerUID int
	OwnerGID int
}

// FS abstracts lstat so the authorization logic can be tested against an
// in-memory filesystem. OSFS is the production implementation.
type FS interface {
	Lstat(path string) (Entry, error)
}

// OSFS performs real lstat calls against the host filesystem.
type OSFS struct{}

// Lstat implements FS using os.Lstat and the platform stat structure.
func (OSFS) Lstat(path string) (Entry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Entry{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return Entry{}, fmt.Errorf("sudoedit: unsupported stat type for %q", path)
	}
	return Entry{
		Mode:     info.Mode(),
		OwnerUID: int(stat.Uid),
		OwnerGID: int(stat.Gid),
	}, nil
}

// Request is a fully described sudoedit invocation.
type Request struct {
	// Env holds the caller environment, typically keyed by SUDO_EDITOR,
	// VISUAL and EDITOR. EnvFromList converts an os.Environ-style slice.
	Env map[string]string
	// Targets are the file paths passed as sudoedit arguments.
	Targets []string
	// Authorized is the sudoers file set. An entry ending with "/" is a
	// directory prefix; any other entry is an exact normalized file path.
	Authorized []string
	// EditorWhitelist lists the only editor executables permitted. Entries
	// must be absolute paths and are compared after normalization.
	EditorWhitelist []string
	// Caller is the unprivileged user the writability checks run as.
	Caller Caller
	// FS defaults to OSFS when nil.
	FS FS
}

// Decision is the authorization result. When Allow is false, Reason and
// Detail explain the rejection; File is the path that triggered it.
type Decision struct {
	Allow      bool
	Reason     Reason
	Detail     string
	File       string
	Editor     string
	EditorArgs []string
	Files      []string
}

// EnvFromList converts an os.Environ-style "KEY=VALUE" slice into the map
// expected by Request. Values may themselves contain "=".
func EnvFromList(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, item := range environ {
		name, value, ok := strings.Cut(item, "=")
		if ok {
			env[name] = value
		}
	}
	return env
}

// Authorize decides whether the described sudoedit invocation is permitted.
//
// The editor variable is selected and whitespace-split before any
// authorization comparison, matching sudoedit's own order of operations.
func Authorize(req Request) Decision {
	fsys := req.FS
	if fsys == nil {
		fsys = OSFS{}
	}

	// Step 1: select and split the editor before touching authorization.
	rawEditor := firstNonEmpty(req.Env["SUDO_EDITOR"], req.Env["VISUAL"], req.Env["EDITOR"])
	if rawEditor == "" {
		return deny(ReasonNoEditor, "SUDO_EDITOR, VISUAL and EDITOR are all empty")
	}
	fields := strings.Fields(rawEditor)
	if len(fields) == 0 {
		return deny(ReasonNoEditor, "the selected editor variable contains no fields")
	}
	editor, rejected := validateEditor(fields[0])
	if rejected.Reason != "" {
		return rejected
	}
	if !isWhitelisted(editor, req.EditorWhitelist) {
		return deny(ReasonEditorNotWhitelisted, "editor %q is not in the caller-supplied whitelist", editor)
	}
	editorArgs := fields[1:]
	extraFiles := extractExtraFiles(editorArgs)

	// Step 2: authorization and filesystem safety for every edited file.
	seen := make(map[string]bool)
	files := make([]string, 0, len(req.Targets)+len(extraFiles))
	checkFile := func(rawPath string) Decision {
		path, rejected := normalizePath(rawPath)
		if rejected.Reason != "" {
			rejected.File = rawPath
			return rejected
		}
		if seen[path] {
			return Decision{}
		}
		if !isAuthorized(path, req.Authorized) {
			d := deny(ReasonUnauthorizedFile, "file %q is not covered by the sudoers authorization set", path)
			d.File = path
			return d
		}
		if rejected := checkSafety(fsys, path, req.Caller); rejected.Reason != "" {
			rejected.File = path
			return rejected
		}
		seen[path] = true
		files = append(files, path)
		return Decision{}
	}

	for _, target := range req.Targets {
		if rejected := checkFile(target); rejected.Reason != "" {
			return rejected
		}
	}
	for _, extra := range extraFiles {
		if rejected := checkFile(extra); rejected.Reason != "" {
			return rejected
		}
	}
	if len(files) == 0 {
		return deny(ReasonNoTargetFiles, "no files were supplied to sudoedit")
	}

	return Decision{
		Allow:      true,
		Editor:     editor,
		EditorArgs: editorArgs,
		Files:      files,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// validateEditor requires an absolute editor path with no ".." segment.
// Relative names would force a PATH lookup and are refused outright.
func validateEditor(rawPath string) (string, Decision) {
	if !filepath.IsAbs(rawPath) {
		return "", deny(ReasonEditorNotAbsolute, "editor %q must be an absolute path; PATH lookup is not allowed", rawPath)
	}
	for _, segment := range strings.Split(filepath.ToSlash(rawPath), "/") {
		if segment == ".." {
			return "", deny(ReasonPathTraversal, "editor path %q contains a '..' segment", rawPath)
		}
	}
	return filepath.Clean(rawPath), Decision{}
}

// normalizePath requires an absolute path without any ".." segment and
// returns its cleaned form.
func normalizePath(rawPath string) (string, Decision) {
	if !filepath.IsAbs(rawPath) {
		return "", deny(ReasonPathNotAbsolute, "path %q must be absolute; PATH lookup is not allowed", rawPath)
	}
	for _, segment := range strings.Split(filepath.ToSlash(rawPath), "/") {
		if segment == ".." {
			return "", deny(ReasonPathTraversal, "path %q contains a '..' segment", rawPath)
		}
	}
	return filepath.Clean(rawPath), Decision{}
}

func isWhitelisted(editor string, whitelist []string) bool {
	for _, allowed := range whitelist {
		if !filepath.IsAbs(allowed) {
			continue
		}
		if filepath.Clean(allowed) == editor {
			return true
		}
	}
	return false
}

// extractExtraFiles mirrors sudoedit's editor argument handling: a bare "--"
// ends option parsing, so every following argument is a file; before it, any
// argument not starting with "-" is also treated as a file.
func extractExtraFiles(args []string) []string {
	var files []string
	afterDashDash := false
	for _, arg := range args {
		switch {
		case afterDashDash:
			files = append(files, arg)
		case arg == "--":
			afterDashDash = true
		case !strings.HasPrefix(arg, "-"):
			files = append(files, arg)
		}
	}
	return files
}

func isAuthorized(path string, rules []string) bool {
	for _, rule := range rules {
		if strings.HasSuffix(rule, "/") {
			prefix := filepath.Clean(rule)
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return true
			}
			continue
		}
		if filepath.Clean(rule) == path {
			return true
		}
	}
	return false
}

// checkSafety rejects symlinks and caller-writable entries for the file itself
// and every parent directory up to the filesystem root.
func checkSafety(fsys FS, path string, caller Caller) Decision {
	if info, err := fsys.Lstat(path); err == nil {
		if rejected := inspectEntry(path, info, caller); rejected.Reason != "" {
			return rejected
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return deny(ReasonStatError, "lstat %q: %v", path, err)
	}

	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		info, err := fsys.Lstat(parent)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return deny(ReasonParentMissing, "parent directory %q does not exist", parent)
			}
			return deny(ReasonStatError, "lstat %q: %v", parent, err)
		}
		if rejected := inspectEntry(parent, info, caller); rejected.Reason != "" {
			return rejected
		}
		if parent == "/" {
			break
		}
	}
	return Decision{}
}

func inspectEntry(path string, info Entry, caller Caller) Decision {
	if info.Mode&fs.ModeSymlink != 0 {
		return deny(ReasonSymlink, "%q is a symbolic link", path)
	}
	if entryWritable(info, caller) {
		return deny(ReasonWritable, "%q is writable by the caller", path)
	}
	return Decision{}
}

func entryWritable(info Entry, caller Caller) bool {
	// Root bypasses file permissions, so fail closed for a root caller.
	if caller.UID == 0 {
		return true
	}
	perm := info.Mode.Perm()
	if perm&0o002 != 0 {
		return true
	}
	if info.OwnerUID == caller.UID && perm&0o200 != 0 {
		return true
	}
	if perm&0o020 != 0 && (info.OwnerGID == caller.GID || containsID(caller.Groups, info.OwnerGID)) {
		return true
	}
	return false
}

func containsID(ids []int, want int) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func deny(reason Reason, format string, args ...any) Decision {
	return Decision{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}
