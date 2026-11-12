// Package fidauth provides fid-level authorization for 9P2000.L servers.
//
// A server exports one or more directories as named attach points. This
// package enforces, per fid, that:
//   - Tattach names (aname) resolve only to caller-registered exports;
//   - every fid stays bound to the root of the export it attached to;
//   - Twalk elements are processed one at a time, may not contain '/' or
//     NUL, and ".." at the export root clamps to the root itself;
//   - a single Twalk carries at most MaxWalkElements elements;
//   - after symlink resolution via the caller-injected FileSystem, the
//     resulting real path remains inside the export root;
//   - newfid must be free, unless it equals fid;
//   - Tclunk invalidates a fid immediately, and reuse requires a fresh
//     attach or walk.
//
// Any error returned here should be answered by the server with Rlerror.
package fidauth

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// MaxWalkElements is the maximum number of name elements accepted in a
// single Twalk request.
const MaxWalkElements = 16

var (
	ErrUnknownExport = errors.New("fidauth: unknown export name")
	ErrFidInUse      = errors.New("fidauth: fid already in use")
	ErrFidInvalid    = errors.New("fidauth: fid unknown or clunked")
	ErrTooManyElems  = errors.New("fidauth: too many walk elements")
	ErrBadElement    = errors.New("fidauth: illegal walk element")
	ErrEscape        = errors.New("fidauth: path escapes export root")
)

// FileSystem resolves symlinks. The caller injects an implementation so
// authorization decisions are made against the same view of the file
// system the server itself uses.
type FileSystem interface {
	// EvalSymlinks returns the canonical path of p after resolving all
	// symbolic links. It must fail if p does not exist.
	EvalSymlinks(p string) (string, error)
}

type osFS struct{}

func (osFS) EvalSymlinks(p string) (string, error) { return filepath.EvalSymlinks(p) }

type fidState struct {
	root string // canonical export root this fid is bound to
	path string // canonical current path, always within root
}

// Auth tracks registered exports and live fids. It is safe for
// concurrent use.
type Auth struct {
	mu      sync.Mutex
	fs      FileSystem
	exports map[string]string
	fids    map[uint32]*fidState
}

// New returns an Auth using fs for symlink resolution. A nil fs uses the
// operating system's filepath.EvalSymlinks.
func New(fs FileSystem) *Auth {
	if fs == nil {
		fs = osFS{}
	}
	return &Auth{
		fs:      fs,
		exports: make(map[string]string),
		fids:    make(map[uint32]*fidState),
	}
}

// RegisterExport registers root under the export name used in Tattach
// aname fields. The root is canonicalized once, here, so later walks
// compare against a stable anchor.
func (a *Auth) RegisterExport(name, root string) error {
	real, err := a.fs.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("fidauth: register export %q: %w", name, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.exports[name] = filepath.Clean(real)
	return nil
}

// Attach authorizes a Tattach. The aname must be a registered export
// name; anything else (including raw paths) fails with ErrUnknownExport.
// On success the fid is bound to the export root and the root path is
// returned.
func (a *Auth) Attach(fid uint32, aname string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	root, ok := a.exports[aname]
	if !ok {
		return "", ErrUnknownExport
	}
	if _, busy := a.fids[fid]; busy {
		return "", ErrFidInUse
	}
	a.fids[fid] = &fidState{root: root, path: root}
	return root, nil
}

// Walk authorizes a Twalk of names from fid to newfid and returns the
// resulting canonical path. Elements are resolved one at a time; ".." at
// the export root stays at the root. On any error no fid state changes.
// A zero-length names clones fid onto newfid.
func (a *Auth) Walk(fid, newfid uint32, names []string) (string, error) {
	if len(names) > MaxWalkElements {
		return "", ErrTooManyElems
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.fids[fid]
	if !ok {
		return "", ErrFidInvalid
	}
	if newfid != fid {
		if _, busy := a.fids[newfid]; busy {
			return "", ErrFidInUse
		}
	}
	cur := st.path
	for _, el := range names {
		if el == "" || strings.ContainsRune(el, '/') || strings.ContainsRune(el, 0) {
			return "", ErrBadElement
		}
		next := cur
		switch el {
		case ".":
		case "..":
			if cur != st.root {
				next = filepath.Dir(cur)
			}
		default:
			next = filepath.Join(cur, el)
		}
		real, err := a.fs.EvalSymlinks(next)
		if err != nil {
			return "", fmt.Errorf("fidauth: walk %q: %w", el, err)
		}
		real = filepath.Clean(real)
		if !withinRoot(st.root, real) {
			return "", ErrEscape
		}
		cur = real
	}
	if newfid == fid {
		st.path = cur
	} else {
		a.fids[newfid] = &fidState{root: st.root, path: cur}
	}
	return cur, nil
}

// Clunk invalidates fid immediately. Subsequent use of the fid fails
// with ErrFidInvalid until it is re-established by Attach or Walk.
func (a *Auth) Clunk(fid uint32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fids, fid)
}

// Path returns the authorized canonical path currently bound to fid, for
// use by other operations (Topen, Tcreate, ...). It fails with
// ErrFidInvalid if the fid is not live.
func (a *Auth) Path(fid uint32) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.fids[fid]
	if !ok {
		return "", ErrFidInvalid
	}
	return st.path, nil
}

func withinRoot(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}
