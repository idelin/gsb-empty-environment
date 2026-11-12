package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// EntryType describes the kind of a tree entry.
type EntryType int

const (
	EntryFile EntryType = iota
	EntryDir
	EntrySymlink
)

// Entry is a single tree entry to be checked out.
type Entry struct {
	Path   string // slash-separated path relative to the checkout root
	Name   string // base name of the entry
	Type   EntryType
	Data   []byte // file content (EntryFile only)
	Target string // link target (EntrySymlink only)
}

// Submodule describes one submodule record from .gitmodules.
type Submodule struct {
	Name string
	Path string
	URL  string
}

// isGitAlias reports whether seg is an alias of ".git" on any supported
// filesystem: case variants, NTFS 8.3 short names (GIT~1), and names with
// trailing dots or spaces which Windows strips.
func isGitAlias(seg string) bool {
	trimmed := strings.TrimRight(seg, ". ")
	lower := strings.ToLower(trimmed)
	if lower == ".git" {
		return true
	}
	if rest, ok := strings.CutPrefix(lower, "git~"); ok && rest != "" && isDigits(rest) {
		return true
	}
	return false
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateSegment(seg string) error {
	if seg == "" {
		return errors.New("empty path segment")
	}
	if seg == "." || seg == ".." {
		return fmt.Errorf("forbidden path segment %q", seg)
	}
	if strings.ContainsRune(seg, 0) {
		return errors.New("path segment contains NUL")
	}
	if isGitAlias(seg) {
		return fmt.Errorf("path segment %q is a .git alias", seg)
	}
	return nil
}

func looksAbsolute(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	// Windows drive-absolute or drive-relative path, e.g. C:\x or C:x.
	if len(p) >= 2 && p[1] == ':' {
		c := p[0]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

// ValidatePath checks that p is a safe relative tree path: no ".."
// segments, not absolute, no backslashes, and no ".git" aliases.
func ValidatePath(p string) error {
	if p == "" {
		return errors.New("empty path")
	}
	if looksAbsolute(p) {
		return fmt.Errorf("absolute path %q not allowed", p)
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("path %q contains backslash", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if err := validateSegment(seg); err != nil {
			return fmt.Errorf("path %q: %w", p, err)
		}
	}
	return nil
}

// ValidateName checks that name is a safe single path segment.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("name %q contains a path separator", name)
	}
	if err := validateSegment(name); err != nil {
		return fmt.Errorf("name %q: %w", name, err)
	}
	return nil
}

// foldPath normalizes p for collision detection on case-insensitive and
// Windows-like filesystems: lowercased, trailing dots/spaces stripped.
func foldPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = strings.ToLower(strings.TrimRight(s, ". "))
	}
	return strings.Join(segs, "/")
}

// ValidateEntries validates a complete set of tree entries against each
// other. Every path and name must be individually safe, no entry may have
// a symlink as a parent, and no two entries may collide on a
// case-insensitive filesystem.
func ValidateEntries(entries []Entry) error {
	byPath := make(map[string]Entry, len(entries))
	folded := make(map[string]string, len(entries))
	for _, e := range entries {
		if err := ValidatePath(e.Path); err != nil {
			return err
		}
		if err := ValidateName(e.Name); err != nil {
			return err
		}
		if base := e.Path[strings.LastIndex(e.Path, "/")+1:]; base != e.Name {
			return fmt.Errorf("entry %q: name %q does not match path base", e.Path, e.Name)
		}
		if _, dup := byPath[e.Path]; dup {
			return fmt.Errorf("duplicate entry %q", e.Path)
		}
		fold := foldPath(e.Path)
		if prev, clash := folded[fold]; clash {
			return fmt.Errorf("entry %q collides with %q on case-insensitive filesystems", e.Path, prev)
		}
		byPath[e.Path] = e
		folded[fold] = e.Path
	}
	for _, e := range entries {
		if err := checkSymlinkParents(byPath, e.Path); err != nil {
			return err
		}
	}
	return nil
}

// checkSymlinkParents rejects p if any ancestor of p is a symlink entry.
func checkSymlinkParents(byPath map[string]Entry, p string) error {
	rest := p
	for {
		idx := strings.LastIndex(rest, "/")
		if idx < 0 {
			return nil
		}
		rest = rest[:idx]
		if parent, ok := byPath[rest]; ok && parent.Type == EntrySymlink {
			return fmt.Errorf("entry %q has symlink parent %q", p, rest)
		}
	}
}

// ValidateURL rejects submodule URLs that git would treat as a
// command-line option ("-...") or that use the dangerous ext:: transport.
func ValidateURL(rawurl string) error {
	if rawurl == "" {
		return errors.New("empty url")
	}
	if strings.HasPrefix(rawurl, "-") {
		return fmt.Errorf("url %q looks like a command-line option", rawurl)
	}
	if strings.HasPrefix(strings.ToLower(rawurl), "ext::") {
		return fmt.Errorf("url %q uses forbidden ext:: transport", rawurl)
	}
	return nil
}

// ValidateSubmodule checks one submodule record against the tree entries.
func ValidateSubmodule(sub Submodule, entries []Entry) error {
	if err := ValidateName(sub.Name); err != nil {
		return fmt.Errorf("submodule name: %w", err)
	}
	if err := ValidatePath(sub.Path); err != nil {
		return fmt.Errorf("submodule path: %w", err)
	}
	if err := ValidateURL(sub.URL); err != nil {
		return fmt.Errorf("submodule url: %w", err)
	}
	byPath := make(map[string]Entry, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}
	if err := checkSymlinkParents(byPath, sub.Path); err != nil {
		return fmt.Errorf("submodule %q: %w", sub.Name, err)
	}
	return nil
}
