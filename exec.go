package manifest

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Node is a snapshot of one tree node as seen by an FS.
type Node struct {
	Type   EntryType
	Digest string
	Link   string
}

// FS is the filesystem surface the executor needs. Paths are absolute,
// slash-separated, and already cleaned.
type FS interface {
	// Stat returns the node at path, or ok == false when absent.
	Stat(path string) (n Node, ok bool)
	// MkdirAll creates path and any missing parents as directories.
	MkdirAll(path string) error
	// Write atomically replaces the file at path, creating it if needed.
	// The parent directory must exist.
	Write(path string, data []byte) error
	// Symlink creates a symlink at path pointing to target. The parent
	// directory must exist and path must not exist.
	Symlink(target, path string) error
	// Rename moves src to dst. The parent of dst must exist.
	Rename(src, dst string) error
	// Remove deletes a file or symlink.
	Remove(path string) error
	// Rmdir deletes a directory, failing when it is not empty.
	Rmdir(path string) error
	// Children lists the names directly under a directory.
	Children(path string) (names []string, ok bool)
}

// Executor applies plans to an FS. Root is where the managed tree lives;
// Quarantine is where moved-aside content is preserved. Both are absolute
// paths inside the FS and Quarantine must be outside Root.
type Executor struct {
	FS         FS
	Root       string
	Quarantine string
}

// OpError reports the op that failed during Execute.
type OpError struct {
	Index int
	Op    Op
	Err   error
}

func (e *OpError) Error() string {
	return fmt.Sprintf("manifest: op %d (%s): %v", e.Index, e.Op, e.Err)
}

func (e Executor) resolve(p string) string { return path.Join(e.Root, p) }

func (e Executor) resolveQuarantine(p string) string { return path.Join(e.Quarantine, p) }

// Execute applies every op in order. It stops at the first failure; the plan
// can simply be executed again to resume, because every op is idempotent.
func (e Executor) Execute(p Plan) error {
	for i, op := range p.Ops {
		if err := e.Apply(op); err != nil {
			return &OpError{Index: i, Op: op, Err: err}
		}
	}
	return nil
}

// Apply executes a single op, skipping it when its effect is already
// present. Guards never touch content that does not match the op's
// expectation, so replaying a plan after a crash never deletes content the
// plan itself already wrote.
func (e Executor) Apply(op Op) error {
	p := e.resolve(op.Path)
	switch op.Kind {
	case OpMoveAside:
		dst := e.resolveQuarantine(op.Dest)
		n, ok := e.FS.Stat(p)
		if !ok || n.Type == Dir {
			// Already moved aside, or the path has since been replaced
			// by the directory the plan is about to create.
			return nil
		}
		if n.Type != op.Expect {
			return fmt.Errorf("manifest: %s: expected %s, found %s", p, op.Expect, n.Type)
		}
		if _, exists := e.FS.Stat(dst); exists {
			return nil // already preserved in quarantine
		}
		if err := e.FS.MkdirAll(path.Dir(dst)); err != nil {
			return err
		}
		return e.FS.Rename(p, dst)

	case OpRemoveFile:
		n, ok := e.FS.Stat(p)
		if !ok {
			return nil
		}
		if n.Type != op.Expect {
			return nil // replaced by newer content; leave it alone
		}
		if n.Type == File && op.Digest != "" && n.Digest != op.Digest {
			return nil // content changed since the plan was built
		}
		return e.FS.Remove(p)

	case OpRmdir:
		n, ok := e.FS.Stat(p)
		if !ok || n.Type != Dir {
			return nil
		}
		return e.FS.Rmdir(p)

	case OpMkdir:
		if n, ok := e.FS.Stat(p); ok {
			if n.Type == Dir {
				return nil
			}
			return fmt.Errorf("manifest: %s: %s blocks directory creation", p, n.Type)
		}
		return e.FS.MkdirAll(p)

	case OpWriteFile:
		if n, ok := e.FS.Stat(p); ok {
			if n.Type == Dir {
				return fmt.Errorf("manifest: %s: directory blocks file write", p)
			}
			if n.Type == File && n.Digest == op.Digest {
				return nil
			}
		}
		return e.FS.Write(p, op.Data)

	case OpSymlink:
		if n, ok := e.FS.Stat(p); ok {
			if n.Type == Symlink && n.Link == op.Link {
				return nil
			}
			if n.Type != Symlink {
				return fmt.Errorf("manifest: %s: %s blocks symlink creation", p, n.Type)
			}
			if err := e.FS.Remove(p); err != nil {
				return err
			}
		}
		return e.FS.Symlink(op.Link, p)
	}
	return fmt.Errorf("manifest: unknown op kind %d", op.Kind)
}

// Snapshot walks the tree at root and returns its manifest, with directories
// listed explicitly. Paths are relative to root.
func Snapshot(fs FS, root string) (Manifest, error) {
	m := Manifest{}
	var walk func(abs, rel string) error
	walk = func(abs, rel string) error {
		names, ok := fs.Children(abs)
		if !ok {
			return fmt.Errorf("manifest: cannot list %s", abs)
		}
		for _, name := range names {
			childAbs := path.Join(abs, name)
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			n, ok := fs.Stat(childAbs)
			if !ok {
				return fmt.Errorf("manifest: %s vanished during scan", childAbs)
			}
			m[childRel] = Entry{Type: n.Type, Digest: n.Digest, Link: n.Link}
			if n.Type == Dir {
				if err := walk(childAbs, childRel); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, ""); err != nil {
		return nil, err
	}
	return m, nil
}

// Verify checks that the tree at root matches want exactly: no missing, no
// extra, and no mismatched entries. Directories implied by want's paths do
// not need to be listed explicitly.
func Verify(fs FS, root string, want Manifest) error {
	wantN, err := want.normalize()
	if err != nil {
		return err
	}
	if err := wantN.checkConsistency(); err != nil {
		return err
	}
	got, err := Snapshot(fs, root)
	if err != nil {
		return err
	}
	wantFull := wantN.withImplicitDirs()

	var problems []string
	for _, p := range sortedPaths(wantFull) {
		g, ok := got[p]
		if !ok {
			problems = append(problems, fmt.Sprintf("missing %s", p))
			continue
		}
		if g != wantFull[p] {
			problems = append(problems, fmt.Sprintf("mismatch %s: want %+v, got %+v", p, wantFull[p], g))
		}
	}
	var extra []string
	for p := range got {
		if _, ok := wantFull[p]; !ok {
			extra = append(extra, p)
		}
	}
	sort.Strings(extra)
	for _, p := range extra {
		problems = append(problems, fmt.Sprintf("unexpected %s", p))
	}
	if len(problems) > 0 {
		return fmt.Errorf("manifest: tree does not match target:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}
