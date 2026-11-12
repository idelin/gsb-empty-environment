package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
)

// Perm is a bitmask of access rights granted on an unveiled path.
type Perm uint8

const (
	PermRead Perm = 1 << iota
	PermWrite
	PermExec
	PermCreate
)

// String renders the permission set in rwxc order, "-" for absent bits.
func (p Perm) String() string {
	var b [4]byte
	b[0], b[1], b[2], b[3] = '-', '-', '-', '-'
	if p&PermRead != 0 {
		b[0] = 'r'
	}
	if p&PermWrite != 0 {
		b[1] = 'w'
	}
	if p&PermExec != 0 {
		b[2] = 'x'
	}
	if p&PermCreate != 0 {
		b[3] = 'c'
	}
	return string(b[:])
}

var (
	// ErrPromiseExpand is returned by Pledge when the requested promise
	// set is not a subset of the current one.
	ErrPromiseExpand = errors.New("netsec: pledge cannot regain dropped promises")
	// ErrUnveilLocked is returned by Unveil after UnveilLock was called.
	ErrUnveilLocked = errors.New("netsec: unveil list is locked")
)

// Resolver canonicalizes a path for access checks: it must resolve
// symlinks and ".." elements against the filesystem and return the
// final absolute path. Callers inject their own implementation so the
// sandbox never trusts lexical paths alone.
type Resolver interface {
	Resolve(path string) (string, error)
}

// ResolverFunc adapts a function to the Resolver interface.
type ResolverFunc func(path string) (string, error)

// Resolve implements Resolver.
func (f ResolverFunc) Resolve(path string) (string, error) { return f(path) }

// OSResolver resolves paths against the real operating system.
type OSResolver struct{}

// Resolve implements Resolver using filepath.Abs and filepath.EvalSymlinks.
func (OSResolver) Resolve(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// Sandbox models a pledge/unveil-style monotone restriction machine.
// Both promise and visibility state only ever shrink; the zero value
// is ready to use.
type Sandbox struct {
	mu         sync.Mutex
	promises   map[string]struct{}
	pledged    bool
	unveils    map[string]Perm
	unveilLock bool
	resolver   Resolver
}

// New returns a Sandbox that canonicalizes paths with r. A nil
// resolver falls back to purely lexical cleaning and is only suitable
// for tests that never touch symlinks.
func New(r Resolver) *Sandbox {
	return &Sandbox{resolver: r}
}

// Pledge narrows the active promise set. The first call establishes
// the set; every later call must request a subset of what remains.
// Calling Pledge with no promises drops them all.
func (s *Sandbox) Pledge(promises ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]struct{}, len(promises))
	for _, p := range promises {
		next[p] = struct{}{}
	}
	if s.pledged {
		for p := range next {
			if _, ok := s.promises[p]; !ok {
				return fmt.Errorf("%w: %q", ErrPromiseExpand, p)
			}
		}
	}
	s.promises = next
	s.pledged = true
	return nil
}

// Promises reports whether p is still in the active promise set.
func (s *Sandbox) Promises(p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.promises[p]
	return ok
}

// Unveil registers path with the given permissions. It must be called
// before UnveilLock; afterwards it fails with ErrUnveilLocked.
// Registering the same canonical path again intersects permissions
// with the existing entry, so visibility can only shrink.
func (s *Sandbox) Unveil(path string, perm Perm) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unveilLock {
		return ErrUnveilLocked
	}
	canon := s.canonicalLocked(path)
	if s.unveils == nil {
		s.unveils = make(map[string]Perm)
	}
	if old, ok := s.unveils[canon]; ok {
		perm &= old
	}
	s.unveils[canon] = perm
	return nil
}

// UnveilLock freezes the unveil list. It is idempotent.
func (s *Sandbox) UnveilLock() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unveilLock = true
}

// Check resolves path through the injected resolver and verifies the
// final path is covered by an unveil entry holding every bit in need.
// Paths outside all entries report fs.ErrNotExist (ENOENT) so the
// sandbox does not leak what exists; covered paths missing a
// permission report fs.ErrPermission (EACCES).
func (s *Sandbox) Check(path string, need Perm) error {
	resolved, err := s.resolve(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var best string
	var bestPerm Perm
	for entry, perm := range s.unveils {
		if covers(entry, resolved) && len(entry) >= len(best) {
			best, bestPerm = entry, perm
		}
	}
	if best == "" {
		return &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	if bestPerm&need != need {
		return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	return nil
}

// resolve canonicalizes an access-time path via the injected resolver.
func (s *Sandbox) resolve(path string) (string, error) {
	s.mu.Lock()
	r := s.resolver
	s.mu.Unlock()
	if r == nil {
		return lexicalClean(path), nil
	}
	return r.Resolve(path)
}

// canonicalLocked canonicalizes a registration-time path. Resolution
// failures (e.g. a create-only target that does not exist yet) fall
// back to lexical cleaning so the entry is still well-formed.
func (s *Sandbox) canonicalLocked(path string) string {
	if s.resolver != nil {
		if resolved, err := s.resolver.Resolve(path); err == nil {
			return resolved
		}
	}
	return lexicalClean(path)
}

func lexicalClean(path string) string {
	if !filepath.IsAbs(path) {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
	}
	return filepath.Clean(path)
}

// covers reports whether path equals entry or lives beneath it.
func covers(entry, path string) bool {
	if path == entry {
		return true
	}
	prefix := strings.TrimSuffix(entry, string(filepath.Separator)) + string(filepath.Separator)
	return strings.HasPrefix(path, prefix)
}
