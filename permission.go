package netsec

import (
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
)

// Action is a FilePermission action. Only Read and Write are supported.
type Action = string

const (
	Read  Action = "read"
	Write Action = "write"

	// ActionRead and ActionWrite are aliases for callers that prefer explicit
	// action names.
	ActionRead  = Read
	ActionWrite = Write
)

var (
	ErrEmptyPath     = errors.New("netsec: empty file path")
	ErrInvalidPath   = errors.New("netsec: invalid file path")
	ErrNoActions     = errors.New("netsec: file permission has no actions")
	ErrInvalidAction = errors.New("netsec: invalid file permission action")
)

// FilePermission describes access to a file path. Path may be an exact path,
// a direct-directory wildcard ("/dir/*"), or a recursive wildcard
// ("/dir/-"). Actions are "read" and/or "write".
type FilePermission struct {
	Path    string
	Actions []Action
}

// NewFilePermission constructs a file permission.
func NewFilePermission(path string, actions ...Action) FilePermission {
	return FilePermission{Path: path, Actions: actions}
}

// Validate checks the permission syntax. A permission used as a request must
// contain at least one action.
func (p FilePermission) Validate() error {
	if p.Path == "" {
		return ErrEmptyPath
	}
	if strings.ContainsRune(p.Path, '\x00') {
		return fmt.Errorf("%w: contains NUL", ErrInvalidPath)
	}

	actions := permissionActions(p)
	if len(actions) == 0 {
		return ErrNoActions
	}
	for _, action := range actions {
		if !validAction(action) {
			return fmt.Errorf("%w: %q", ErrInvalidAction, action)
		}
	}
	return nil
}

// String returns the permission in a form suitable for diagnostics.
func (p FilePermission) String() string {
	return fmt.Sprintf("%s:%s", p.Path, strings.Join(permissionActions(p), ","))
}

func permissionActions(p FilePermission) []Action {
	var actions []Action
	for _, raw := range p.Actions {
		for _, action := range strings.Split(raw, ",") {
			action = strings.TrimSpace(action)
			if action != "" {
				actions = append(actions, action)
			}
		}
	}
	return actions
}

func validAction(action Action) bool {
	return action == Read || action == Write
}

func hasAction(p FilePermission, wanted Action) bool {
	for _, action := range permissionActions(p) {
		if action == wanted {
			return true
		}
	}
	return false
}

// ProtectionDomain is the set of grants assigned to one plugin or library.
type ProtectionDomain struct {
	Name   string
	Grants []FilePermission
}

// NewProtectionDomain constructs a protection domain.
func NewProtectionDomain(name string, grants ...FilePermission) ProtectionDomain {
	return ProtectionDomain{Name: name, Grants: grants}
}

// Allows reports whether this domain, by itself, grants the requested
// permission. A domain's grants are additive: different grants may satisfy
// different requested actions for the same path.
func (d ProtectionDomain) Allows(request FilePermission) bool {
	normalized, err := normalizeRequest(request)
	if err != nil {
		return false
	}
	return d.allowsNormalized(normalized)
}

func (d ProtectionDomain) allowsNormalized(request FilePermission) bool {
	for _, action := range permissionActions(request) {
		if !d.grantsAction(request.Path, action) {
			return false
		}
	}
	return true
}

func (d ProtectionDomain) grantsAction(filePath, action Action) bool {
	for _, grant := range d.Grants {
		if grant.Validate() != nil {
			continue
		}
		if hasAction(grant, action) && permissionMatches(grant.Path, filePath) {
			return true
		}
	}
	return false
}

func normalizeRequest(request FilePermission) (FilePermission, error) {
	if err := request.Validate(); err != nil {
		return FilePermission{}, err
	}

	clean, err := cleanFilePath(request.Path)
	if err != nil {
		return FilePermission{}, err
	}
	request.Path = clean
	return request, nil
}

func cleanFilePath(filePath string) (string, error) {
	if filePath == "" {
		return "", ErrEmptyPath
	}
	if strings.ContainsRune(filePath, '\x00') {
		return "", fmt.Errorf("%w: contains NUL", ErrInvalidPath)
	}

	clean := pathpkg.Clean(filePath)
	if clean == "" {
		return "", ErrInvalidPath
	}
	return clean, nil
}

// permissionMatches applies JVM FilePermission-style path semantics. The
// wildcard forms must be the final component:
//
//   - /dir/* matches every immediate child of /dir, but not /dir itself.
//   - /dir/- matches every descendant of /dir, at any depth, but not /dir.
//   - Neither wildcard matches a sibling such as /directory or /dir2.
//
// Matching is lexical. Paths are cleaned before comparison, so ".." cannot be
// used to make a path look like it belongs to a different directory.
func permissionMatches(pattern, filePath string) bool {
	cleanPattern, err := cleanFilePath(pattern)
	if err != nil {
		return false
	}

	switch {
	case strings.HasSuffix(cleanPattern, "/-"):
		base := strings.TrimSuffix(cleanPattern, "/-")
		if base == "" {
			base = "/"
		}
		remainder, ok := descendantRemainder(base, filePath)
		return ok && remainder != ""

	case strings.HasSuffix(cleanPattern, "/*"):
		base := strings.TrimSuffix(cleanPattern, "/*")
		if base == "" {
			base = "/"
		}
		remainder, ok := descendantRemainder(base, filePath)
		return ok && remainder != "" && !strings.Contains(remainder, "/")

	default:
		return cleanPattern == filePath
	}
}

func descendantRemainder(base, filePath string) (string, bool) {
	if base == "/" {
		if strings.HasPrefix(filePath, "/") && len(filePath) > 1 {
			return filePath[1:], true
		}
		return "", false
	}

	prefix := base + "/"
	if strings.HasPrefix(filePath, prefix) && len(filePath) > len(prefix) {
		return filePath[len(prefix):], true
	}
	return "", false
}
