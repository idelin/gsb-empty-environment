package manifest

import (
	"fmt"
	"sort"
	"strings"
)

// OpKind identifies a plan operation.
type OpKind int

const (
	// OpMoveAside renames a file or symlink that blocks a needed directory
	// into the quarantine area, preserving its content.
	OpMoveAside OpKind = iota
	// OpRemoveFile deletes a file or symlink, but only while it still
	// matches the expected old identity (type and digest).
	OpRemoveFile
	// OpRmdir removes a directory, but only while it is empty.
	OpRmdir
	// OpMkdir creates a directory (and any missing parents).
	OpMkdir
	// OpWriteFile writes file content, skipping when the digest already
	// matches.
	OpWriteFile
	// OpSymlink creates a symlink with the given target.
	OpSymlink
)

func (k OpKind) String() string {
	switch k {
	case OpMoveAside:
		return "move-aside"
	case OpRemoveFile:
		return "remove-file"
	case OpRmdir:
		return "rmdir"
	case OpMkdir:
		return "mkdir"
	case OpWriteFile:
		return "write-file"
	case OpSymlink:
		return "symlink"
	}
	return "unknown"
}

// Op is a single idempotent plan step. Applying an already-satisfied op is a
// no-op, so a plan can be replayed after an interruption.
type Op struct {
	Kind   OpKind
	Path   string // root-relative target path
	Dest   string // OpMoveAside: quarantine-relative destination
	Data   []byte // OpWriteFile: content to write
	Digest string // OpWriteFile: digest of Data; OpRemoveFile: expected old digest
	Link   string // OpSymlink: link target
	Expect EntryType
}

func (op Op) String() string {
	switch op.Kind {
	case OpMoveAside:
		return fmt.Sprintf("move-aside %s -> %s", op.Path, op.Dest)
	case OpRemoveFile:
		return fmt.Sprintf("remove-file %s (%s)", op.Path, op.Expect)
	case OpRmdir:
		return fmt.Sprintf("rmdir %s", op.Path)
	case OpMkdir:
		return fmt.Sprintf("mkdir %s", op.Path)
	case OpWriteFile:
		return fmt.Sprintf("write-file %s (%d bytes)", op.Path, len(op.Data))
	case OpSymlink:
		return fmt.Sprintf("symlink %s -> %s", op.Path, op.Link)
	}
	return "unknown-op"
}

// Plan is an ordered, replayable list of operations.
type Plan struct {
	Ops []Op
}

func (p Plan) String() string {
	var b strings.Builder
	for i, op := range p.Ops {
		fmt.Fprintf(&b, "%3d. %s\n", i, op)
	}
	return b.String()
}

// Options configures BuildPlan.
type Options struct {
	// Content supplies the content for files that must be (re)written. It is
	// called with the root-relative path. Required when the target manifest
	// introduces or changes any file.
	Content func(path string) ([]byte, error)
}

// BuildPlan computes the replayable plan that transforms a tree matching old
// into one matching want.
//
// Ordering guarantees:
//   - file/symlink nodes blocking a needed directory are moved aside into
//     quarantine before any directory is created (never deleted);
//   - when a directory becomes a file or symlink, its old descendants are
//     removed first, deepest path first;
//   - all removals precede all creates and writes.
//
// Every removal is guarded by the old identity, so replaying a partially
// applied plan never deletes content the plan itself already wrote.
func BuildPlan(old, want Manifest, opts Options) (Plan, error) {
	oldN, err := old.normalize()
	if err != nil {
		return Plan{}, err
	}
	wantN, err := want.normalize()
	if err != nil {
		return Plan{}, err
	}
	if err := oldN.checkConsistency(); err != nil {
		return Plan{}, err
	}
	if err := wantN.checkConsistency(); err != nil {
		return Plan{}, err
	}

	wantFull := wantN.withImplicitDirs()
	oldFull := oldN.withImplicitDirs()

	// covered marks old paths already handled by a structural op so the
	// plain-removal pass skips them.
	covered := map[string]bool{}
	var aside, removals, creates []Op

	// Phase A1: a file or symlink sits where the target needs a directory
	// (the entry itself or one of its descendants requires it). Move the
	// conflicting node aside into quarantine, preserving its content.
	for _, d := range sortedPaths(wantFull) {
		if wantFull[d].Type != Dir {
			continue
		}
		if e, ok := oldFull[d]; ok && e.Type != Dir && !covered[d] {
			aside = append(aside, Op{
				Kind:   OpMoveAside,
				Path:   d,
				Dest:   d,
				Expect: e.Type,
			})
			covered[d] = true
		}
	}

	// Phase A2: a directory becomes a file or symlink. Remove its old
	// descendants first (deepest first), then the directory itself.
	for _, p := range sortedPaths(wantN) {
		if wantN[p].Type == Dir {
			continue
		}
		prefix := p + "/"
		var under []string
		for q := range oldFull {
			if strings.HasPrefix(q, prefix) {
				under = append(under, q)
			}
		}
		// The directory itself goes away once its descendants are gone,
		// whether or not the old manifest listed it explicitly.
		if e, ok := oldFull[p]; (ok && e.Type == Dir) || len(under) > 0 {
			under = append(under, p)
		}
		if len(under) == 0 {
			continue
		}
		sort.Slice(under, func(i, j int) bool {
			di, dj := strings.Count(under[i], "/"), strings.Count(under[j], "/")
			if di != dj {
				return di > dj
			}
			return under[i] > under[j]
		})
		for _, q := range under {
			if covered[q] {
				continue
			}
			covered[q] = true
			removals = append(removals, removeOp(q, oldFull[q]))
		}
	}

	// Phase B: plain removals — old paths absent from the target — and
	// type changes between file and symlink at the same path.
	var rest []string
	for p, e := range oldFull {
		if covered[p] {
			continue
		}
		if _, ok := wantFull[p]; !ok {
			rest = append(rest, p)
			continue
		}
		w := wantFull[p]
		if e.Type != Dir && w.Type != Dir && e.Type != w.Type {
			rest = append(rest, p)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		di, dj := strings.Count(rest[i], "/"), strings.Count(rest[j], "/")
		if di != dj {
			return di > dj
		}
		return rest[i] > rest[j]
	})
	for _, p := range rest {
		removals = append(removals, removeOp(p, oldFull[p]))
	}

	// Phase C: creates and updates, shallowest first so parents exist
	// before their children.
	for _, p := range sortedPaths(wantFull) {
		w := wantFull[p]
		switch w.Type {
		case Dir:
			if o, ok := oldFull[p]; ok && o.Type == Dir {
				continue
			}
			creates = append(creates, Op{Kind: OpMkdir, Path: p})
		case File:
			if o, ok := oldFull[p]; ok && o.Type == File && o.Digest == w.Digest {
				continue
			}
			if opts.Content == nil {
				return Plan{}, fmt.Errorf("manifest: no Content source for new file %q", p)
			}
			data, err := opts.Content(p)
			if err != nil {
				return Plan{}, fmt.Errorf("manifest: content for %q: %w", p, err)
			}
			if w.Digest != "" && DigestBytes(data) != w.Digest {
				return Plan{}, fmt.Errorf("manifest: content for %q does not match target digest", p)
			}
			creates = append(creates, Op{Kind: OpWriteFile, Path: p, Data: data, Digest: DigestBytes(data)})
		case Symlink:
			if o, ok := oldFull[p]; ok && o.Type == Symlink && o.Link == w.Link {
				continue
			}
			creates = append(creates, Op{Kind: OpSymlink, Path: p, Link: w.Link})
		}
	}

	ops := make([]Op, 0, len(aside)+len(removals)+len(creates))
	ops = append(ops, aside...)
	ops = append(ops, removals...)
	ops = append(ops, creates...)
	return Plan{Ops: ops}, nil
}

// removeOp builds the guarded removal op for an old entry: files and
// symlinks are removed only while their identity still matches; directories
// are removed only while empty.
func removeOp(p string, e Entry) Op {
	if e.Type == Dir {
		return Op{Kind: OpRmdir, Path: p, Expect: Dir}
	}
	return Op{Kind: OpRemoveFile, Path: p, Digest: e.Digest, Expect: e.Type}
}
