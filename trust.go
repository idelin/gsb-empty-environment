// Package netsec decides whether a Git repository on disk is safe to read
// configuration from before any of its (potentially attacker-controlled)
// config files or hooks are opened.
//
// The rules mirror Git's safe.directory protection:
//
//   - A directory owned by another user is not trusted unless it matches an
//     entry from the system (/etc/gitconfig) or global (~/.gitconfig)
//     configuration. The repository's own local config is never consulted, so
//     an attacker cannot self-authorize by writing safe.directory inside the
//     repository.
//   - Entries match exactly, or with the literal "*" (trust every directory),
//     or with a pattern ending in "/*" which matches the given directory and
//     any directory beneath it.
//   - While walking parent directories to locate a .git directory, the walk
//     stops at the first directory owned by someone else.
//   - Under sudo the owning user is compared against SUDO_UID rather than
//     root's UID 0.
package netsec

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// Reason describes why a decision was reached.
type Reason string

const (
	// ReasonOwner means a .git directory was found inside a directory tree
	// owned by the effective user.
	ReasonOwner Reason = "owner"

	// ReasonSafeDirectory means a directory owned by another user matched a
	// safe.directory entry from the system or global configuration.
	ReasonSafeDirectory Reason = "safe.directory"

	// ReasonOtherOwner means a directory on the path is owned by another user
	// and was not listed in safe.directory.
	ReasonOtherOwner Reason = "other-owner"

	// ReasonNoRepository means the walk reached a filesystem root without
	// finding a .git directory.
	ReasonNoRepository Reason = "no-repository"
)

// Decision is the result of evaluating a candidate repository path.
type Decision struct {
	// Trusted reports whether the repository configuration may be read.
	Trusted bool

	// Reason is the rule that produced the decision.
	Reason Reason

	// RepoDir is the directory containing .git when Trusted is true, otherwise
	// it is empty.
	RepoDir string

	// SafeEntry is the safe.directory entry that matched, when Reason is
	// ReasonSafeDirectory.
	SafeEntry string
}

// OwnerFunc returns the numeric owner UID of a path. It is an indirection so
// the decision logic can be exercised with synthetic ownership in tests.
type OwnerFunc func(path string) (uid int, err error)

// Evaluator performs trust decisions with injectable ownership and
// configuration sources. The zero value is not usable; use NewEvaluator.
type Evaluator struct {
	owner        OwnerFunc
	systemConfig string
	globalConfig string
}

// NewEvaluator builds an Evaluator that reads system and global Git
// configuration from the conventional locations.
func NewEvaluator() *Evaluator {
	system, global := configFilePaths()
	return &Evaluator{
		owner:        osOwner,
		systemConfig: system,
		globalConfig: global,
	}
}

// IsTrusted reports whether the Git repository at or above path may have its
// local configuration and hooks read.
func IsTrusted(path string) (Decision, error) {
	return NewEvaluator().IsTrusted(path)
}

// IsTrusted reports whether the Git repository at or above path is trusted.
func (e *Evaluator) IsTrusted(path string) (Decision, error) {
	if e == nil {
		return Decision{}, errors.New("netsec: nil Evaluator")
	}
	if e.owner == nil {
		e.owner = osOwner
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return Decision{}, err
	}
	dir := filepath.Clean(abs)

	uid := effectiveUID()
	safe, err := e.loadSafeDirectories()
	if err != nil {
		return Decision{}, err
	}

	for {
		owner, err := e.owner(dir)
		if err != nil {
			return Decision{}, err
		}
		if owner != uid {
			if entry, ok := matchSafeDirectory(dir, safe); ok {
				return Decision{
					Trusted:   true,
					Reason:    ReasonSafeDirectory,
					RepoDir:   dir,
					SafeEntry: entry,
				}, nil
			}
			// Stop walking: directories above an untrusted directory must not
			// be inspected, and nothing inside it may be read.
			return Decision{Reason: ReasonOtherOwner}, nil
		}

		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return Decision{
				Trusted: true,
				Reason:  ReasonOwner,
				RepoDir: dir,
			}, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return Decision{}, err
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return Decision{Reason: ReasonNoRepository}, nil
		}
		dir = parent
	}
}

// effectiveUID returns the UID ownership is compared against. Under sudo the
// invoking user's SUDO_UID takes precedence over root's UID 0.
func effectiveUID() int {
	if raw := os.Getenv("SUDO_UID"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			return parsed
		}
	}
	return os.Getuid()
}

// matchSafeDirectory reports whether path matches any safe.directory entry.
// Supported entry shapes are the exact path, the literal "*" wildcard, and a
// prefix of the form "/some/dir/*" matching that directory and everything
// below it.
func matchSafeDirectory(path string, entries []string) (string, bool) {
	for _, entry := range entries {
		switch {
		case entry == "*":
			return entry, true
		case entry == path:
			return entry, true
		case len(entry) >= 2 && entry[len(entry)-2:] == "/*":
			base := entry[:len(entry)-1] // keep the trailing slash
			if path == base[:len(base)-1] || (len(path) > len(base) && path[:len(base)] == base) {
				return entry, true
			}
		}
	}
	return "", false
}
