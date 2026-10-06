// Package cleaner implements a two-phase, race-safe filesystem cleaner.
//
// Phase one freezes a plan: every targeted file and directory is snapshotted
// with its identity, version and size. Phase two executes the plan entry by
// entry, re-checking each object against its snapshot before deleting it.
// Anything that changed, vanished or was replaced since the freeze is
// skipped and reported, so a new file is never deleted just because it
// happens to live at a stale planned path. Directories are processed only
// after all their planned children and are removed only while still empty.
package cleaner

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// Kind classifies a planned entry.
type Kind int

const (
	// File is a regular file entry.
	File Kind = iota
	// Dir is a directory entry.
	Dir
)

func (k Kind) String() string {
	if k == Dir {
		return "dir"
	}
	return "file"
}

// PlanEntry is one frozen snapshot: the object that existed at Path when
// the plan was built.
type PlanEntry struct {
	Path    string
	Kind    Kind
	ID      uint64
	Version uint64
	Size    int64
}

// Plan is a frozen, immutable deletion plan. Entries are ordered so that
// children always come before their parents.
type Plan struct {
	Entries []PlanEntry
}

// Preview renders the frozen plan for human review before execution.
func (p *Plan) Preview() string {
	var b strings.Builder
	for _, e := range p.Entries {
		fmt.Fprintf(&b, "%-4s %s (id=%d ver=%d size=%d)\n", e.Kind, e.Path, e.ID, e.Version, e.Size)
	}
	return b.String()
}

// Planner builds frozen plans from an FS.
type Planner struct {
	FS FS
}

// BuildPlan walks the given roots and freezes a deletion plan covering the
// roots themselves and everything beneath them.
func (p Planner) BuildPlan(roots ...string) (*Plan, error) {
	var entries []PlanEntry
	seen := make(map[string]bool)
	for _, root := range roots {
		if err := p.walk(root, &entries, seen); err != nil {
			return nil, err
		}
	}
	// Children before parents: deeper paths first, then by name.
	sort.Slice(entries, func(i, j int) bool {
		di, dj := depth(entries[i].Path), depth(entries[j].Path)
		if di != dj {
			return di > dj
		}
		return entries[i].Path < entries[j].Path
	})
	return &Plan{Entries: entries}, nil
}

func (p Planner) walk(path string, entries *[]PlanEntry, seen map[string]bool) error {
	info, err := p.FS.Stat(path)
	if err != nil {
		return err
	}
	kind := File
	if info.IsDir {
		kind = Dir
	}
	if !seen[path] {
		seen[path] = true
		*entries = append(*entries, PlanEntry{
			Path:    path,
			Kind:    kind,
			ID:      info.ID,
			Version: info.Version,
			Size:    info.Size,
		})
	}
	if !info.IsDir {
		return nil
	}
	names, err := p.FS.ReadDir(path)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := p.walk(p.FS.Join(path, name), entries, seen); err != nil {
			return err
		}
	}
	return nil
}

func depth(path string) int { return strings.Count(path, "/") }

// SkipReason explains why a planned entry was not deleted.
type SkipReason string

const (
	// ReasonGone means the object no longer exists at the planned path.
	ReasonGone SkipReason = "gone"
	// ReasonChanged means the object at the planned path no longer matches
	// the frozen snapshot (identity, kind, version or size), so it must
	// not be deleted.
	ReasonChanged SkipReason = "changed"
	// ReasonNotEmpty means a directory still holds children (unplanned new
	// files, or planned children that were skipped or failed).
	ReasonNotEmpty SkipReason = "not-empty"
)

// Skip records a planned entry that was left in place.
type Skip struct {
	Path   string
	Reason SkipReason
}

// Failure records a planned entry whose deletion was attempted but failed.
type Failure struct {
	Path string
	Err  error
}

// Report summarizes one plan execution.
type Report struct {
	Deleted []string
	Skipped []Skip
	Failed  []Failure
}

// OK reports whether every planned entry was deleted.
func (r *Report) OK() bool { return len(r.Skipped) == 0 && len(r.Failed) == 0 }

func (r *Report) String() string {
	var b strings.Builder
	for _, p := range r.Deleted {
		fmt.Fprintf(&b, "deleted  %s\n", p)
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(&b, "skipped  %s (%s)\n", s.Path, s.Reason)
	}
	for _, f := range r.Failed {
		fmt.Fprintf(&b, "failed   %s (%v)\n", f.Path, f.Err)
	}
	return b.String()
}

// Executor runs frozen plans against an FS.
type Executor struct {
	FS FS
}

// Execute runs the plan entry by entry. Each entry is re-validated against
// the live filesystem immediately before its deletion; mismatches are
// skipped and reported, never deleted.
func (e Executor) Execute(plan *Plan) *Report {
	rep := &Report{}
	for _, entry := range plan.Entries {
		e.execEntry(entry, rep)
	}
	return rep
}

func (e Executor) execEntry(entry PlanEntry, rep *Report) {
	info, err := e.FS.Stat(entry.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			rep.Skipped = append(rep.Skipped, Skip{Path: entry.Path, Reason: ReasonGone})
		} else {
			rep.Failed = append(rep.Failed, Failure{Path: entry.Path, Err: err})
		}
		return
	}
	// Identity and kind must match the frozen snapshot; otherwise a
	// different object now lives at this path and must be left alone.
	if info.ID != entry.ID || info.IsDir != (entry.Kind == Dir) {
		rep.Skipped = append(rep.Skipped, Skip{Path: entry.Path, Reason: ReasonChanged})
		return
	}
	if entry.Kind == File {
		// Files are deleted only when version and size are untouched.
		if info.Version != entry.Version || info.Size != entry.Size {
			rep.Skipped = append(rep.Skipped, Skip{Path: entry.Path, Reason: ReasonChanged})
			return
		}
	} else {
		// Directories are processed after all planned children (plan
		// ordering guarantees this) and removed only while still empty.
		names, err := e.FS.ReadDir(entry.Path)
		if err != nil {
			rep.Failed = append(rep.Failed, Failure{Path: entry.Path, Err: err})
			return
		}
		if len(names) > 0 {
			rep.Skipped = append(rep.Skipped, Skip{Path: entry.Path, Reason: ReasonNotEmpty})
			return
		}
	}
	if err := e.FS.Remove(entry.Path); err != nil {
		rep.Failed = append(rep.Failed, Failure{Path: entry.Path, Err: err})
		return
	}
	rep.Deleted = append(rep.Deleted, entry.Path)
}
