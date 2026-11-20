package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"syscall"
)

// ErrLocked is returned by Pledge and Unveil once the Sandbox is locked.
var ErrLocked = errors.New("netsec: sandbox is locked")

// ErrWiden is returned by Pledge when the requested promise set is not
// a subset of the current set. The current set is left unchanged.
var ErrWiden = errors.New("netsec: pledge cannot widen promises")

// errLoop reports too many symbolic link hops during resolution.
var errLoop = errors.New("netsec: too many levels of symbolic links")

// maxSymlinkHops bounds symlink resolution, like MAXSYMLINKS.
const maxSymlinkHops = 40

// Sandbox models pledge(2) and unveil(2): a promise set that can only
// shrink, and a visibility table of path -> permissions that can only
// narrow, both frozen by Lock.
type Sandbox struct {
	mu       sync.Mutex
	fs       FileSystem
	locked   bool
	promises map[string]struct{}
	unveils  map[string]Perm // normalized absolute path -> permissions
}

// New creates a Sandbox backed by filesystem (OSFS when nil) holding
// the given initial promises.
func New(filesystem FileSystem, initialPromises ...string) *Sandbox {
	if filesystem == nil {
		filesystem = OSFS{}
	}
	s := &Sandbox{
		fs:       filesystem,
		promises: make(map[string]struct{}, len(initialPromises)),
		unveils:  make(map[string]Perm),
	}
	for _, p := range initialPromises {
		s.promises[p] = struct{}{}
	}
	return s
}

// Lock freezes the sandbox: any subsequent Pledge or Unveil call fails
// with ErrLocked and leaves state unchanged. Access checks keep
// working after Lock.
func (s *Sandbox) Lock() {
	s.mu.Lock()
	s.locked = true
	s.mu.Unlock()
}

// Locked reports whether the sandbox has been locked.
func (s *Sandbox) Locked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locked
}

// Pledge replaces the promise set with the given promises, which must
// be a subset of the current set: promises can only be dropped, never
// gained. After Lock, every call fails with ErrLocked. On any failure
// the current set is left unchanged.
func (s *Sandbox) Pledge(promises ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return ErrLocked
	}
	next := make(map[string]struct{}, len(promises))
	for _, p := range promises {
		if _, ok := s.promises[p]; !ok {
			return fmt.Errorf("%w: %q", ErrWiden, p)
		}
		next[p] = struct{}{}
	}
	s.promises = next
	return nil
}

// Promises returns the current promise set in sorted order.
func (s *Sandbox) Promises() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.promises))
	for p := range s.promises {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Unveil registers (path, perms) in the visibility table. Registering
// the same path again keeps only the intersection of the existing and
// new permissions: permissions can never be widened. After Lock, every
// call fails with ErrLocked and the table is left unchanged.
func (s *Sandbox) Unveil(path string, perms string) error {
	p, err := ParsePerm(perms)
	if err != nil {
		return err
	}
	np := normalizePath(path)
	if !isAbs(np) {
		return fmt.Errorf("netsec: unveil path %q is not absolute", path)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locked {
		return ErrLocked
	}
	if old, ok := s.unveils[np]; ok {
		p &= old // intersection only: never widen
	}
	s.unveils[np] = p
	return nil
}

// UnveilPerms reports the permissions registered for path.
func (s *Sandbox) UnveilPerms(path string) (Perm, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.unveils[normalizePath(path)]
	return p, ok
}

// Unveils returns a snapshot of the visibility table.
func (s *Sandbox) Unveils() map[string]Perm {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Perm, len(s.unveils))
	for k, v := range s.unveils {
		out[k] = v
	}
	return out
}

// Check resolves path against the injected filesystem — segment by
// segment, following symlinks and ".." — and verifies that the final
// path falls under a registered unveil entry granting every permission
// in need. A symlink may point into any registered entry but must not
// resolve outside the unveiled set.
//
// Paths outside the unveiled set yield an error matching
// errors.Is(err, syscall.ENOENT); insufficient permissions on an
// unveiled path yield syscall.EACCES.
func (s *Sandbox) Check(path string, need Perm) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	resolved, err := s.resolveLocked(path)
	if err != nil {
		return err
	}
	_, perms, ok := s.bestMatchLocked(resolved)
	if !ok {
		return &fs.PathError{Op: "check", Path: path, Err: syscall.ENOENT}
	}
	if perms&need != need {
		return &fs.PathError{Op: "check", Path: path, Err: syscall.EACCES}
	}
	return nil
}

// CheckPerm is Check with a permission string such as "rw".
func (s *Sandbox) CheckPerm(path string, need string) error {
	p, err := ParsePerm(need)
	if err != nil {
		return err
	}
	return s.Check(path, p)
}

// bestMatchLocked returns the longest registered entry covering path.
func (s *Sandbox) bestMatchLocked(path string) (string, Perm, bool) {
	best := ""
	var perms Perm
	for entry, p := range s.unveils {
		if covers(entry, path) && len(entry) > len(best) {
			best, perms = entry, p
		}
	}
	return best, perms, best != ""
}

// coveredLocked reports whether path falls under any registered entry.
func (s *Sandbox) coveredLocked(path string) bool {
	_, _, ok := s.bestMatchLocked(path)
	return ok
}

// covers reports whether the registered entry covers path, i.e. entry
// is path itself or an ancestor directory of path.
func covers(entry, path string) bool {
	if entry == "/" {
		return true
	}
	return path == entry || strings.HasPrefix(path, entry+"/")
}

// resolveLocked resolves path segment by segment against the injected
// filesystem. ".." pops the already-resolved (symlink-free) prefix;
// symlink targets are resolved recursively and must stay within the
// unveiled set. Missing trailing components are appended lexically so
// that checks for not-yet-created files still work.
func (s *Sandbox) resolveLocked(path string) (string, error) {
	hops := 0
	return s.resolveFrom(normalizePath(path), &hops)
}

func (s *Sandbox) resolveFrom(path string, hops *int) (string, error) {
	if !isAbs(path) {
		return "", fmt.Errorf("netsec: path %q is not absolute", path)
	}
	resolved := "/"
	comps := splitComps(path)
	for i := 0; i < len(comps); i++ {
		c := comps[i]
		switch c {
		case "", ".":
			continue
		case "..":
			resolved = parentOf(resolved)
			continue
		}
		cur := joinPath(resolved, c)
		info, err := s.fs.Lstat(cur)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Remaining components cannot contain further
				// symlinks; fold them in lexically.
				resolved = cur
				for _, r := range comps[i+1:] {
					switch r {
					case "", ".":
					case "..":
						resolved = parentOf(resolved)
					default:
						resolved = joinPath(resolved, r)
					}
				}
				return resolved, nil
			}
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			*hops++
			if *hops > maxSymlinkHops {
				return "", errLoop
			}
			target, err := s.fs.Readlink(cur)
			if err != nil {
				return "", err
			}
			t := normalizePath(target)
			if !isAbs(t) {
				t = normalizePath(joinPath(resolved, t))
			}
			rt, err := s.resolveFrom(t, hops)
			if err != nil {
				return "", err
			}
			// A symlink may point into a registered entry but must
			// not escape the unveiled set.
			if !s.coveredLocked(rt) {
				return "", &fs.PathError{Op: "resolve", Path: cur, Err: syscall.ENOENT}
			}
			resolved = rt
			continue
		}
		resolved = cur
	}
	return resolved, nil
}
