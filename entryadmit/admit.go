// Package entryadmit validates entry names inside an uploaded Go module
// zip before they are admitted into a module proxy cache.
//
// An uploaded module zip is trusted only if every entry name:
//   - starts with the "module@version/" prefix;
//   - has no empty, "." or ".." path segments after that prefix;
//   - contains no backslashes;
//   - does not differ from another entry only in letter case;
//   - is not inside a "vendor" directory;
//   - is not inside a subtree rooted at a nested go.mod file.
//
// In addition, the archive must contain exactly one go.mod file, located
// at the module root ("module@version/go.mod").
//
// Use Checker to validate entries while streaming a zip, or Check to
// validate a complete slice of entry names in one call.
package entryadmit

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors wrapped by Error. Use errors.Is to inspect them.
var (
	// ErrBadPrefix means an entry name does not start with the
	// "module@version/" prefix of the archive being checked.
	ErrBadPrefix = errors.New("entryadmit: missing module@version/ prefix")

	// ErrInvalidPath means an entry name contains a backslash, an empty
	// path segment, or a "." segment.
	ErrInvalidPath = errors.New("entryadmit: invalid entry path")

	// ErrTraversal means an entry name contains a ".." segment that would
	// escape the module directory when extracted.
	ErrTraversal = errors.New("entryadmit: path traversal segment")

	// ErrCaseConflict means an entry name differs from an earlier entry
	// only in letter case.
	ErrCaseConflict = errors.New("entryadmit: case-insensitive entry conflict")

	// ErrVendor means an entry lives inside a vendor directory.
	ErrVendor = errors.New("entryadmit: vendor directory entries are not allowed")

	// ErrNestedGoMod means an entry is a nested go.mod file or lives in
	// the subtree of one.
	ErrNestedGoMod = errors.New("entryadmit: nested go.mod subtrees are not allowed")

	// ErrRootGoMod means the root go.mod file is missing or appears more
	// than once.
	ErrRootGoMod = errors.New("entryadmit: archive must contain exactly one root go.mod")
)

// Error is returned for every rejected entry. It wraps one of the package
// sentinel errors, which can be inspected with errors.Is.
type Error struct {
	// Entry is the rejected entry name. It is empty for archive-level
	// failures such as a missing root go.mod.
	Entry string

	// Err is the wrapped sentinel error describing the violation.
	Err error
}

func (e *Error) Error() string {
	if e.Entry == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("entryadmit: entry %q rejected: %s", e.Entry, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func reject(entry string, err error) error {
	return &Error{Entry: entry, Err: err}
}

func baseSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Checker enforces the admission rules on a stream of entry names belonging
// to one module zip. A Checker is not safe for concurrent use.
type Checker struct {
	prefix     string
	seen       map[string]string // lower-cased node path -> original-case node path
	nestedMods map[string]bool   // module-relative dirs containing a nested go.mod
	entries    []string          // module-relative names already admitted, in order
	rootGoMod  bool
	err        error
}

// New returns a Checker for the given module path and version, which together
// define the mandatory "module@version/" entry prefix.
func New(module, version string) (*Checker, error) {
	if err := checkModulePath(module); err != nil {
		return nil, err
	}
	if err := checkVersion(version); err != nil {
		return nil, err
	}
	return &Checker{
		prefix:     module + "@" + version + "/",
		seen:       make(map[string]string),
		nestedMods: make(map[string]bool),
	}, nil
}

// Prefix returns the mandatory "module@version/" prefix.
func (c *Checker) Prefix() string { return c.prefix }

// Admit checks one entry name. It returns the first violation encountered;
// once a Checker has failed it keeps returning that same error so callers can
// stop processing early.
func (c *Checker) Admit(name string) error {
	if c.err != nil {
		return c.err
	}
	c.err = c.admit(name)
	return c.err
}

func (c *Checker) admit(name string) error {
	// Backslashes are never legal, including in traversal attempts such as
	// "..\..\secret", which must not reach an extractor on Windows.
	if strings.ContainsRune(name, '\\') {
		return reject(name, fmt.Errorf("%w: contains backslash", ErrInvalidPath))
	}

	if !strings.HasPrefix(name, c.prefix) {
		return reject(name, ErrBadPrefix)
	}
	rel := name[len(c.prefix):]
	if rel == "" {
		return reject(name, fmt.Errorf("%w: empty path after prefix", ErrInvalidPath))
	}

	segments := strings.Split(rel, "/")
	for _, seg := range segments {
		switch seg {
		case "":
			return reject(name, fmt.Errorf("%w: empty path segment", ErrInvalidPath))
		case ".":
			return reject(name, fmt.Errorf("%w: %q segment", ErrInvalidPath, seg))
		case "..":
			return reject(name, fmt.Errorf("%w: %q segment", ErrTraversal, seg))
		}
	}

	// A vendor directory at any depth is rejected wholesale.
	for _, seg := range segments {
		if seg == "vendor" {
			return reject(name, ErrVendor)
		}
	}

	// Case-insensitive uniqueness of every path segment within its parent
	// directory. Full-path comparison would miss directory-only conflicts
	// such as "Sub/a.go" vs "sUB/a.go"; per-segment scoping matches the
	// case-insensitive filesystem semantics Go module zips target.
	lowerPath := ""
	origPath := ""
	for i, seg := range segments {
		lowerPath += "/" + strings.ToLower(seg)
		origPath += "/" + seg
		existing, ok := c.seen[lowerPath]
		switch {
		case ok && existing != origPath:
			return reject(name, fmt.Errorf("%w: %q collides with %q", ErrCaseConflict, seg, baseSegment(existing)))
		case ok && i == len(segments)-1:
			return reject(name, fmt.Errorf("%w: duplicate entry", ErrCaseConflict))
		}
		c.seen[lowerPath] = origPath
	}

	// Reject entries sitting under a nested go.mod seen earlier in the stream.
	for dir := range c.nestedMods {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return reject(name, fmt.Errorf("%w: under %s/go.mod", ErrNestedGoMod, dir))
		}
	}

	// go.mod files: exactly one at the root; any other go.mod opens a
	// nested module whose whole subtree is forbidden. This runs after the
	// case-collision check, so a repeated root go.mod is reported as a
	// conflict rather than silently counted twice.
	if segments[len(segments)-1] == "go.mod" {
		if len(segments) == 1 {
			c.rootGoMod = true
		} else {
			dir := strings.Join(segments[:len(segments)-1], "/")
			c.nestedMods[dir] = true
			// A go.mod may arrive after files beneath it. Fail the archive
			// if any already-admitted entry lives in that subtree, so the
			// final admission result does not depend on entry ordering.
			for _, earlier := range c.entries {
				if earlier == dir || strings.HasPrefix(earlier, dir+"/") {
					return reject(name, fmt.Errorf("%w: subtree entry %q already admitted", ErrNestedGoMod, earlier))
				}
			}
			return reject(name, ErrNestedGoMod)
		}
	}

	c.entries = append(c.entries, rel)
	return nil
}

// Close must be called after every entry has been offered. It fails when the
// archive lacks a root go.mod or when an earlier entry was rejected.
func (c *Checker) Close() error {
	if c.err != nil {
		return c.err
	}
	if !c.rootGoMod {
		c.err = &Error{Err: ErrRootGoMod}
	}
	return c.err
}

// Check validates a complete list of entry names for one module zip. It is a
// convenience wrapper around Checker.
func Check(module, version string, names []string) error {
	checker, err := New(module, version)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := checker.Admit(name); err != nil {
			return err
		}
	}
	return checker.Close()
}

// CheckName applies the per-entry rules to a single name without tracking
// cross-entry state. Prefix, segment, backslash, vendor and nested go.mod
// violations are detected; case collisions and the exactly-one root go.mod
// requirement need a Checker or Check.
func CheckName(module, version, name string) error {
	checker, err := New(module, version)
	if err != nil {
		return err
	}
	return checker.Admit(name)
}

func checkModulePath(module string) error {
	if module == "" {
		return errors.New("entryadmit: empty module path")
	}
	if strings.HasPrefix(module, "/") || strings.HasSuffix(module, "/") {
		return fmt.Errorf("entryadmit: invalid module path %q", module)
	}
	for _, seg := range strings.Split(module, "/") {
		if seg == "" || seg == "." || seg == ".." ||
			strings.ContainsAny(seg, `\ "@`) {
			return fmt.Errorf("entryadmit: invalid module path %q", module)
		}
	}
	return nil
}

func checkVersion(version string) error {
	if version == "" {
		return errors.New("entryadmit: empty version")
	}
	if strings.ContainsAny(version, `/\ "@`) {
		return fmt.Errorf("entryadmit: invalid version %q", version)
	}
	return nil
}
