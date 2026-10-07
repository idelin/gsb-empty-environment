package manifestdiff

import (
	"fmt"
	"sort"
	"strings"
)

// OpKind identifies a plan operation.
type OpKind int

const (
	// OpMoveAside renames an old file/symlink to a backup path so a
	// different kind can take its place without destroying the content.
	OpMoveAside OpKind = iota
	// OpRemove deletes a path, but only if it still matches Old.
	OpRemove
	// OpMkdir creates a directory.
	OpMkdir
	// OpWriteFile writes file content addressed by Digest.
	OpWriteFile
	// OpSymlink creates a symlink.
	OpSymlink
	// OpCleanupBackup removes a backup created by OpMoveAside, but only
	// if its content still matches Old.
	OpCleanupBackup
)

// Op is a single guarded, replayable plan step. Old records the expected
// pre-change state used by destructive ops to avoid touching content that
// has changed since the plan was created.
type Op struct {
	Kind   OpKind
	Path   string
	Backup string // OpMoveAside: destination; otherwise unused
	Old    Entry  // expected old state (guard for destructive ops)
	Digest string // OpWriteFile: digest of the content to write
	Target string // OpSymlink: link target to create
}

func (o Op) String() string {
	switch o.Kind {
	case OpMoveAside:
		return fmt.Sprintf("move-aside %s -> %s (was %s)", o.Path, o.Backup, o.Old.Kind)
	case OpRemove:
		return fmt.Sprintf("remove %s (%s)", o.Path, o.Old.Kind)
	case OpMkdir:
		return fmt.Sprintf("mkdir %s", o.Path)
	case OpWriteFile:
		return fmt.Sprintf("write-file %s digest=%s", o.Path, o.Digest)
	case OpSymlink:
		return fmt.Sprintf("symlink %s -> %s", o.Path, o.Target)
	case OpCleanupBackup:
		return fmt.Sprintf("cleanup-backup %s", o.Path)
	}
	return fmt.Sprintf("op(%d) %s", int(o.Kind), o.Path)
}

// Plan is an ordered, replayable list of operations.
type Plan struct {
	Ops []Op
}

func (p Plan) String() string {
	var b strings.Builder
	for i, op := range p.Ops {
		fmt.Fprintf(&b, "%3d: %s\n", i, op)
	}
	return b.String()
}

// Diff computes a plan that transforms a tree matching oldM into one
// matching newM. The plan is ordered in four phases:
//
//  1. move-aside: files/symlinks whose path must host a different kind are
//     renamed to unique backup paths (conflicting ancestors included);
//  2. remove: old-only paths and directories being replaced, deepest first
//     so children go before their parent;
//  3. create/update: mkdirs, file writes and symlinks, parents first;
//  4. cleanup: backups are deleted, guarded by the old digest.
//
// Every op is individually idempotent and guarded, so the whole plan can be
// replayed after a partial failure without deleting content it did not
// itself move aside.
func Diff(oldM, newM *Manifest) Plan {
	var moves, removes, creates, cleanups []Op
	handled := make(map[string]bool, len(oldM.Entries))

	used := make(map[string]bool, len(oldM.Entries)+len(newM.Entries))
	for p := range oldM.Entries {
		used[p] = true
	}
	for p := range newM.Entries {
		used[p] = true
	}
	backupFor := func(path string) string {
		parent, base := Parent(path), path
		if i := strings.LastIndex(path, "/"); i >= 0 {
			base = path[i+1:]
		}
		for i := 0; ; i++ {
			name := fmt.Sprintf(".%s.gsb-backup-%d", base, i)
			cand := name
			if parent != "" {
				cand = parent + "/" + name
			}
			if !used[cand] {
				used[cand] = true
				return cand
			}
		}
	}

	createOp := func(e Entry) Op {
		switch e.Kind {
		case KindDir:
			return Op{Kind: OpMkdir, Path: e.Path}
		case KindFile:
			return Op{Kind: OpWriteFile, Path: e.Path, Digest: e.Digest}
		case KindSymlink:
			return Op{Kind: OpSymlink, Path: e.Path, Target: e.Target}
		}
		panic("unreachable")
	}
	moveAside := func(old Entry) {
		b := backupFor(old.Path)
		moves = append(moves, Op{Kind: OpMoveAside, Path: old.Path, Backup: b, Old: old})
		cleanups = append(cleanups, Op{Kind: OpCleanupBackup, Path: b, Old: old})
	}

	for _, e := range newM.Sorted() {
		old, ok := oldM.Entries[e.Path]
		if !ok {
			creates = append(creates, createOp(e))
			continue
		}
		handled[e.Path] = true
		if old.Kind == e.Kind {
			switch e.Kind {
			case KindFile:
				if old.Digest != e.Digest {
					creates = append(creates, Op{Kind: OpWriteFile, Path: e.Path, Digest: e.Digest, Old: old})
				}
			case KindSymlink:
				if old.Target != e.Target {
					creates = append(creates, Op{Kind: OpSymlink, Path: e.Path, Target: e.Target, Old: old})
				}
			}
			continue
		}
		switch {
		case old.Kind == KindDir:
			// Directory becomes file/symlink: delete descendants first,
			// deepest first, then the directory itself.
			for _, d := range descendants(oldM, e.Path) {
				removes = append(removes, Op{Kind: OpRemove, Path: d.Path, Old: d})
				handled[d.Path] = true
			}
			removes = append(removes, Op{Kind: OpRemove, Path: e.Path, Old: old})
			creates = append(creates, createOp(e))
		default:
			// File/symlink becomes something else (including a directory
			// that may be an ancestor of other new entries): move the old
			// content aside before creating the replacement.
			moveAside(old)
			creates = append(creates, createOp(e))
		}
	}

	var remaining []Entry
	for _, e := range oldM.Entries {
		if !handled[e.Path] {
			remaining = append(remaining, e)
		}
	}
	sort.Slice(remaining, func(i, j int) bool {
		di, dj := depth(remaining[i].Path), depth(remaining[j].Path)
		if di != dj {
			return di > dj
		}
		return remaining[i].Path < remaining[j].Path
	})
	for _, e := range remaining {
		removes = append(removes, Op{Kind: OpRemove, Path: e.Path, Old: e})
	}

	var ops []Op
	ops = append(ops, moves...)
	ops = append(ops, removes...)
	ops = append(ops, creates...)
	ops = append(ops, cleanups...)
	return Plan{Ops: ops}
}

// descendants returns all entries under dir, deepest first.
func descendants(m *Manifest, dir string) []Entry {
	prefix := dir + "/"
	var out []Entry
	for _, e := range m.Entries {
		if strings.HasPrefix(e.Path, prefix) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := depth(out[i].Path), depth(out[j].Path)
		if di != dj {
			return di > dj
		}
		return out[i].Path < out[j].Path
	})
	return out
}
