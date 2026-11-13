// Package sudoedit provides an embeddable authorization decision for the
// sudoedit editor-environment splitting and sudoers file-set authorization.
//
// The decision mirrors the security-relevant parts of sudoedit: the caller
// controlled editor variables (SUDO_EDITOR, VISUAL, EDITOR, in that order)
// are split on whitespace, the first field must be an absolute executable
// present in the editor allowlist (PATH is never consulted), and every file
// reachable through the resulting command line must be part of the sudoers
// authorized set and must not be writable by the caller or reachable through
// a symbolic link.
package sudoedit

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Reason identifies why a Request was denied.
type Reason string

const (
	// ReasonNoEditor means none of SUDO_EDITOR, VISUAL or EDITOR was set to
	// a non-empty value, or the selected value split to no fields.
	ReasonNoEditor Reason = "no_editor"

	// ReasonEditorNotAbsolute means the editor executable is a relative name
	// that would require PATH resolution. PATH lookup is never performed.
	ReasonEditorNotAbsolute Reason = "editor_not_absolute"

	// ReasonEditorNotAllowed means the absolute editor executable is not a
	// member of the caller-supplied editor allowlist.
	ReasonEditorNotAllowed Reason = "editor_not_allowed"

	// ReasonNoTargets means no file would be edited at all.
	ReasonNoTargets Reason = "no_targets"

	// ReasonPathNotAbsolute means an editor-supplied extra file is not an
	// absolute path and therefore cannot be matched against the sudoers
	// authorized set unambiguously.
	ReasonPathNotAbsolute Reason = "path_not_absolute"

	// ReasonPathTraversal means a file path contains a ".." segment. Such
	// paths are rejected before lexical normalization.
	ReasonPathTraversal Reason = "path_traversal"

	// ReasonUnauthorizedFile means a target or editor-supplied file is not
	// covered by the sudoers authorized set.
	ReasonUnauthorizedFile Reason = "unauthorized_file"

	// ReasonUnsafePath means a file or one of its parent directories is
	// writable by the caller or is a symbolic link.
	ReasonUnsafePath Reason = "unsafe_path"
)

// Request is the input to an authorization decision.
type Request struct {
	// Env carries the caller-controlled environment. Only SUDO_EDITOR,
	// VISUAL and EDITOR are consulted, in that order.
	Env map[string]string

	// Targets are the files named on the sudoedit command line.
	Targets []string

	// Authorized is the sudoers-authorized file set. An entry ending in "/"
	// is a directory prefix and matches the directory itself and anything
	// beneath it; any other entry matches one exact, normalized path.
	Authorized []string

	// EditorAllowlist is the caller-supplied set of permitted editor
	// executables, keyed by absolute path.
	EditorAllowlist map[string]bool

	// Inspector performs the writable/symbolic-link checks. When nil, an
	// OSInspector backed by the real filesystem and the current process is
	// used.
	Inspector PathInspector
}

// Decision is the result of evaluating a Request.
type Decision struct {
	// Allowed reports whether the sudoedit invocation is authorized.
	Allowed bool

	// Reason is the denial reason; it is empty when Allowed is true.
	Reason Reason

	// Err carries a DenialError when Allowed is false.
	Err error

	// Editor is the resolved absolute editor executable.
	Editor string

	// Files is the ordered, normalized list of every file that would be
	// edited, comprising the command-line targets followed by any extra file
	// smuggled through the editor variable.
	Files []string

	// DeniedPath is the first path that caused the denial, when applicable.
	DeniedPath string
}

// DenialError is returned in Decision.Err for a rejected request.
type DenialError struct {
	Reason Reason
	Path   string
	msg    string
}

func (e *DenialError) Error() string {
	return e.msg
}

// Authorize evaluates a Request and returns its Decision.
//
// The editor variable is selected and split into fields before any file
// authorization is considered, so a malicious editor command line can never
// ride along on an otherwise authorized target list.
func Authorize(req Request) Decision {
	rawEditor, ok := selectEditor(req.Env)
	if !ok {
		return deny(ReasonNoEditor, "", "sudoedit: no SUDO_EDITOR, VISUAL or EDITOR variable is set")
	}

	fields := strings.Fields(rawEditor)
	if len(fields) == 0 {
		return deny(ReasonNoEditor, "", "sudoedit: selected editor variable splits to no fields")
	}

	editor := fields[0]
	if !filepath.IsAbs(editor) {
		return deny(ReasonEditorNotAbsolute, editor,
			"sudoedit: editor %q is not an absolute path; PATH lookup is not permitted", editor)
	}
	if !req.EditorAllowlist[editor] {
		return deny(ReasonEditorNotAllowed, editor,
			"sudoedit: editor %q is not in the editor allowlist", editor)
	}

	extraFiles := extractFiles(fields[1:])

	allFiles := make([]string, 0, len(req.Targets)+len(extraFiles))
	allFiles = append(allFiles, req.Targets...)
	allFiles = append(allFiles, extraFiles...)
	if len(allFiles) == 0 {
		return deny(ReasonNoTargets, "", "sudoedit: no files would be edited")
	}

	normalized := make([]string, 0, len(allFiles))
	for _, file := range allFiles {
		clean, reason := normalizePath(file)
		if reason != "" {
			return deny(reason, file, "sudoedit: rejected file path %q: %s", file, reason)
		}
		if !isAuthorized(clean, req.Authorized) {
			return deny(ReasonUnauthorizedFile, clean,
				"sudoedit: file %q is not covered by the sudoers authorized set", clean)
		}
		normalized = append(normalized, clean)
	}

	inspector := req.Inspector
	if inspector == nil {
		inspector = OSInspector{}
	}
	for _, file := range normalized {
		if unsafe := firstUnsafeComponent(inspector, file); unsafe != "" {
			return deny(ReasonUnsafePath, unsafe,
				"sudoedit: %q is writable by the caller or is a symbolic link", unsafe)
		}
	}

	return Decision{
		Allowed: true,
		Editor:  editor,
		Files:   normalized,
	}
}

// selectEditor returns the first non-empty value among SUDO_EDITOR, VISUAL
// and EDITOR, in that order of precedence.
func selectEditor(env map[string]string) (string, bool) {
	for _, key := range []string{"SUDO_EDITOR", "VISUAL", "EDITOR"} {
		if value, ok := env[key]; ok && value != "" {
			return value, true
		}
	}
	return "", false
}

// extractFiles pulls extra file arguments out of the editor field list.
// A "--" marker ends option parsing; every token after it, and every token
// before it that does not start with "-", is treated as an extra file.
func extractFiles(args []string) []string {
	var files []string
	afterDoubleDash := false
	for _, arg := range args {
		switch {
		case arg == "--":
			afterDoubleDash = true
		case afterDoubleDash || !strings.HasPrefix(arg, "-"):
			files = append(files, arg)
		}
	}
	return files
}

// normalizePath rejects non-absolute paths and paths containing a ".."
// segment, then applies lexical normalization.
func normalizePath(path string) (string, Reason) {
	if !filepath.IsAbs(path) {
		return "", ReasonPathNotAbsolute
	}
	for _, segment := range strings.Split(path, string(filepath.Separator)) {
		if segment == ".." {
			return "", ReasonPathTraversal
		}
	}
	return filepath.Clean(path), ""
}

// isAuthorized compares a normalized path against the sudoers authorized
// set. Entries ending in "/" denote directory prefixes.
func isAuthorized(path string, authorized []string) bool {
	for _, entry := range authorized {
		if strings.HasSuffix(entry, "/") {
			base := strings.TrimSuffix(entry, "/")
			if base == "" {
				return true
			}
			dir := filepath.Clean(base)
			if path == dir || strings.HasPrefix(path, dir+"/") {
				return true
			}
			continue
		}
		if path == filepath.Clean(entry) {
			return true
		}
	}
	return false
}

// firstUnsafeComponent walks the file and every parent directory up to the
// filesystem root, returning the first component that is a symbolic link or
// writable by the caller.
func firstUnsafeComponent(inspector PathInspector, path string) string {
	for current := path; ; current = filepath.Dir(current) {
		if inspector.IsSymlink(current) || inspector.IsWritable(current) {
			return current
		}
		if current == string(filepath.Separator) || current == "." {
			return ""
		}
	}
}

func deny(reason Reason, path string, format string, args ...any) Decision {
	return Decision{
		Allowed:    false,
		Reason:     reason,
		DeniedPath: path,
		Err: &DenialError{
			Reason: reason,
			Path:   path,
			msg:    fmt.Sprintf(format, args...),
		},
	}
}
