// Package cleaner implements a two-phase safe cleaner: it first freezes a
// deletion plan, then executes it item by item, re-validating each entry's
// identity, version and size before removal so that files changed, replaced
// or moved after planning are never deleted by their old path.
package cleaner

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"
)

// FileID uniquely identifies a file on a device (dev+ino on real systems).
type FileID struct {
	Dev uint64
	Ino uint64
}

// FileInfo is the identity snapshot the cleaner needs for one path.
type FileInfo struct {
	ID      FileID
	Size    int64
	ModTime time.Time // version: changes on every content mutation
	IsDir   bool
}

// FS abstracts the filesystem so the cleaner can run against a real or a
// virtual filesystem.
type FS interface {
	// Stat returns the current identity of path, or fs.ErrNotExist.
	Stat(path string) (FileInfo, error)
	// ReadDir returns the entry names of a directory.
	ReadDir(path string) ([]string, error)
	// RemoveFile removes a single non-directory file.
	RemoveFile(path string) error
	// RemoveDir removes a directory; it must fail if the directory is
	// not empty.
	RemoveDir(path string) error
}

// ItemKind distinguishes files from directories in a plan.
type ItemKind int

const (
	FileItem ItemKind = iota
	DirItem
)

// PlanItem is one frozen deletion entry with the identity observed at plan
// time.
type PlanItem struct {
	Path    string
	Kind    ItemKind
	ID      FileID
	Size    int64
	ModTime time.Time
}

// Plan is a frozen snapshot of everything matched for deletion. Directories
// always appear after all of their planned children.
type Plan struct {
	Root     string
	FrozenAt time.Time
	Items    []PlanItem
}

// SkipReason explains why a planned item was not removed.
type SkipReason string

const (
	ReasonMissing  SkipReason = "missing"   // path no longer exists
	ReasonChanged  SkipReason = "changed"   // identity, version or size differs
	ReasonNotEmpty SkipReason = "not-empty" // directory gained entries
	ReasonError    SkipReason = "error"     // removal failed
)

// SkippedItem records a planned entry that was left in place.
type SkippedItem struct {
	Path   string
	Reason SkipReason
	Detail string
}

// Report summarizes one Execute run.
type Report struct {
	Removed []string
	Skipped []SkippedItem
}

// BuildPlan walks root and freezes a deletion plan. Children are always
// ordered before their parent directories so execution processes directory
// contents before the directory itself.
func BuildPlan(fsys FS, root string) (Plan, error) {
	plan := Plan{Root: root, FrozenAt: time.Now()}
	var walk func(dir string) error
	walk = func(dir string) error {
		names, err := fsys.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("plan: read %s: %w", dir, err)
		}
		sort.Strings(names)
		for _, name := range names {
			path := join(dir, name)
			info, err := fsys.Stat(path)
			if err != nil {
				return fmt.Errorf("plan: stat %s: %w", path, err)
			}
			if info.IsDir {
				if err := walk(path); err != nil {
					return err
				}
				plan.Items = append(plan.Items, PlanItem{
					Path: path, Kind: DirItem,
					ID: info.ID, Size: info.Size, ModTime: info.ModTime,
				})
			} else {
				plan.Items = append(plan.Items, PlanItem{
					Path: path, Kind: FileItem,
					ID: info.ID, Size: info.Size, ModTime: info.ModTime,
				})
			}
		}
		return nil
	}
	info, err := fsys.Stat(root)
	if err != nil {
		return Plan{}, fmt.Errorf("plan: stat %s: %w", root, err)
	}
	if !info.IsDir {
		return Plan{}, fmt.Errorf("plan: %s is not a directory", root)
	}
	if err := walk(root); err != nil {
		return Plan{}, err
	}
	plan.Items = append(plan.Items, PlanItem{
		Path: root, Kind: DirItem,
		ID: info.ID, Size: info.Size, ModTime: info.ModTime,
	})
	return plan, nil
}

// Execute runs a frozen plan against the live filesystem. Every item is
// re-validated against its current on-disk identity before removal; anything
// that changed, vanished or fails to delete is skipped and reported, never
// deleted by stale path information.
func Execute(fsys FS, plan Plan) Report {
	var rep Report
	for _, item := range plan.Items {
		info, err := fsys.Stat(item.Path)
		if errors.Is(err, fs.ErrNotExist) {
			rep.Skipped = append(rep.Skipped, SkippedItem{
				Path: item.Path, Reason: ReasonMissing,
				Detail: "path no longer exists",
			})
			continue
		}
		if err != nil {
			rep.Skipped = append(rep.Skipped, SkippedItem{
				Path: item.Path, Reason: ReasonError, Detail: err.Error(),
			})
			continue
		}
		if !matches(item, info) {
			rep.Skipped = append(rep.Skipped, SkippedItem{
				Path: item.Path, Reason: ReasonChanged,
				Detail: "identity, version or size changed since plan",
			})
			continue
		}
		if item.Kind == DirItem {
			// Children were processed earlier in plan order; only
			// remove the directory if it is still empty now.
			names, err := fsys.ReadDir(item.Path)
			if err != nil {
				rep.Skipped = append(rep.Skipped, SkippedItem{
					Path: item.Path, Reason: ReasonError, Detail: err.Error(),
				})
				continue
			}
			if len(names) > 0 {
				rep.Skipped = append(rep.Skipped, SkippedItem{
					Path: item.Path, Reason: ReasonNotEmpty,
					Detail: "directory is not empty",
				})
				continue
			}
			err = fsys.RemoveDir(item.Path)
		} else {
			err = fsys.RemoveFile(item.Path)
		}
		if err != nil {
			rep.Skipped = append(rep.Skipped, SkippedItem{
				Path: item.Path, Reason: ReasonError, Detail: err.Error(),
			})
			continue
		}
		rep.Removed = append(rep.Removed, item.Path)
	}
	return rep
}

// matches reports whether the live identity still equals the frozen one.
func matches(item PlanItem, info FileInfo) bool {
	if item.Kind == DirItem {
		// A directory's mtime/size legitimately change as its planned
		// children are removed; identity plus the emptiness check in
		// Execute are what make directory removal safe.
		return info.ID == item.ID && info.IsDir
	}
	return info.ID == item.ID &&
		info.Size == item.Size &&
		info.ModTime.Equal(item.ModTime) &&
		!info.IsDir
}

func join(dir, name string) string {
	if dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}
