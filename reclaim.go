package netsec

import (
	"errors"
	"fmt"
	"io/fs"
)

// DefaultMaxDepth bounds recursion when Options.MaxDepth is unset.
const DefaultMaxDepth = 64

// Options tunes a reclamation round.
type Options struct {
	// MaxDepth bounds directory recursion. Subtrees deeper than the
	// limit are left untouched for the next round. Zero uses
	// DefaultMaxDepth.
	MaxDepth int
}

// Report summarizes one reclamation round.
type Report struct {
	// FilesRemoved counts unlinked non-directory entries, symlinks
	// included.
	FilesRemoved int
	// DirsRemoved counts removed (emptied) directories.
	DirsRemoved int
	// StaleSkipped counts entries that vanished or whose generation
	// changed between confirmation and removal; they are left for the
	// next round.
	StaleSkipped int
	// DepthLimited counts subtrees skipped because MaxDepth was
	// reached.
	DepthLimited int
}

// Reclaim empties the already-open directory dir. dir itself is not
// removed and stays open; the caller keeps ownership of the capability.
//
// The traversal works from per-directory snapshots: entries created
// while a round runs are invisible to it and left for the next round,
// so an attacker growing the tree cannot keep a round alive. Removal
// only touches the exact object generation confirmed during the
// traversal; an entry replaced between confirmation and removal is
// skipped. Symlinks are never followed: a link, including one forming
// a loop or pointing outside dir, is unlinked as a plain name.
//
// A failure on one entry does not abort the round; remaining entries
// are still processed and all failures are returned as a joined error.
func Reclaim(dir Dir, opts *Options) (Report, error) {
	r := newReclaimer(opts)
	r.walk(dir, 0, ".")
	return r.report, errors.Join(r.errs...)
}

// ReclaimChild reclaims the entry name below the already-open parent
// and then removes the entry itself. A directory child is emptied and
// removed; any other entry, a symlink included, is unlinked. The
// semantics of Reclaim apply to the traversal below name.
func ReclaimChild(parent Dir, name string, opts *Options) (Report, error) {
	r := newReclaimer(opts)
	info, err := parent.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return r.report, nil
	case err != nil:
		r.errs = append(r.errs, fmt.Errorf("lstat %s: %w", name, err))
		return r.report, errors.Join(r.errs...)
	}
	if info.Type == TypeDir {
		r.reclaimDir(parent, name, info, 0, name)
	} else {
		r.unlink(parent, name, info, name)
	}
	return r.report, errors.Join(r.errs...)
}

type reclaimer struct {
	maxDepth int
	report   Report
	errs     []error
}

func newReclaimer(opts *Options) *reclaimer {
	depth := DefaultMaxDepth
	if opts != nil && opts.MaxDepth > 0 {
		depth = opts.MaxDepth
	}
	return &reclaimer{maxDepth: depth}
}

func (r *reclaimer) walk(dir Dir, depth int, path string) {
	if depth > r.maxDepth {
		r.report.DepthLimited++
		r.errs = append(r.errs, fmt.Errorf("reclaim %s: depth limit %d reached, subtree left for next round", path, r.maxDepth))
		return
	}
	entries, err := dir.ReadDir()
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("readdir %s: %w", path, err))
		return
	}
	for _, entry := range entries {
		r.entry(dir, entry.Name, depth, joinPath(path, entry.Name))
	}
}

func (r *reclaimer) entry(dir Dir, name string, depth int, path string) {
	info, err := dir.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.report.StaleSkipped++
		return
	case err != nil:
		r.errs = append(r.errs, fmt.Errorf("lstat %s: %w", path, err))
		return
	}
	if info.Type == TypeDir {
		r.reclaimDir(dir, name, info, depth, path)
		return
	}
	// Files, symlinks and everything else: unlink only, never follow.
	r.unlink(dir, name, info, path)
}

func (r *reclaimer) reclaimDir(dir Dir, name string, want Info, depth int, path string) {
	sub, err := dir.OpenDir(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.report.StaleSkipped++
		return
	case err != nil:
		r.errs = append(r.errs, fmt.Errorf("opendir %s: %w", path, err))
		return
	}
	defer sub.Close()
	self, err := sub.Stat()
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("stat %s: %w", path, err))
		return
	}
	if self.ID != want.ID || self.Type != TypeDir {
		// Replaced between Lstat and OpenDir: leave the new
		// generation for the next round.
		r.report.StaleSkipped++
		return
	}
	r.walk(sub, depth+1, path)
	// Confirm the entry still names the generation just emptied
	// before removing it.
	cur, err := dir.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.report.StaleSkipped++
		return
	case err != nil:
		r.errs = append(r.errs, fmt.Errorf("lstat %s: %w", path, err))
		return
	}
	if cur.ID != want.ID || cur.Type != TypeDir {
		// Replaced while the subtree was reclaimed: do not remove
		// someone else's object.
		r.report.StaleSkipped++
		return
	}
	if err := dir.Rmdir(name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.report.StaleSkipped++
			return
		}
		r.errs = append(r.errs, fmt.Errorf("rmdir %s: %w", path, err))
		return
	}
	r.report.DirsRemoved++
}

func (r *reclaimer) unlink(dir Dir, name string, want Info, path string) {
	cur, err := dir.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.report.StaleSkipped++
		return
	case err != nil:
		r.errs = append(r.errs, fmt.Errorf("lstat %s: %w", path, err))
		return
	}
	if cur.ID != want.ID || cur.Type == TypeDir {
		// Replaced since confirmation: leave the new generation for
		// the next round.
		r.report.StaleSkipped++
		return
	}
	if err := dir.Unlink(name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.report.StaleSkipped++
			return
		}
		r.errs = append(r.errs, fmt.Errorf("unlink %s: %w", path, err))
		return
	}
	r.report.FilesRemoved++
}

func joinPath(parent, name string) string {
	if parent == "" || parent == "." {
		return name
	}
	return parent + "/" + name
}
