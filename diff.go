package fsdiff

import (
	"fmt"
	"sort"
)

// OpKind classifies a diff operation.
type OpKind uint8

const (
	// OpCreate introduces a previously unseen inode at Path.
	OpCreate OpKind = iota
	// OpDelete removes Path; the inode vanishes once its last path is gone.
	OpDelete
	// OpMove renames an inode from From to To (identity preserved).
	OpMove
	// OpLink adds Path as an extra name for an inode that keeps at least
	// one of its old links (hard link added).
	OpLink
	// OpUnlink drops Path while the inode survives through other links
	// (hard link removed).
	OpUnlink
	// OpModify keeps the inode and its links but changes content/metadata.
	OpModify
)

func (k OpKind) String() string {
	switch k {
	case OpCreate:
		return "create"
	case OpDelete:
		return "delete"
	case OpMove:
		return "move"
	case OpLink:
		return "link"
	case OpUnlink:
		return "unlink"
	case OpModify:
		return "modify"
	}
	return "unknown"
}

// Op is a single diff operation. Parent IDs refer to directory inodes:
// Parent is the directory of Path (or To) in the state where that path
// exists, FromParent the directory of From. Carrying inode IDs makes
// replay immune to ancestor renames.
type Op struct {
	Kind       OpKind
	ID         ID
	Path       string // Create / Delete / Link / Unlink target
	From       string // Move source
	To         string // Move destination
	Parent     ID     // dir inode of Path / To
	FromParent ID     // dir inode of From
	Node       *Inode // new inode state for Create / Modify
}

func (op Op) String() string {
	switch op.Kind {
	case OpMove:
		return fmt.Sprintf("move %s -> %s", op.From, op.To)
	case OpModify:
		return fmt.Sprintf("modify %s", op.Node.Paths)
	default:
		return fmt.Sprintf("%s %s", op.Kind, op.Path)
	}
}

// link is one directory entry pointing at an inode: the structural
// identity of a path, independent of ancestor renames.
type link struct {
	parent ID
	name   string
}

// linksOf maps every non-root inode to its set of directory entries.
func (s *Snapshot) linksOf() map[ID][]link {
	m := make(map[ID][]link, len(s.Inodes))
	for dirID, dir := range s.Entries {
		for name, child := range dir {
			m[child] = append(m[child], link{dirID, name})
		}
	}
	for _, ls := range m {
		sort.Slice(ls, func(i, j int) bool {
			a, b := ls[i], ls[j]
			if a.parent != b.parent {
				return idLess(a.parent, b.parent)
			}
			return a.name < b.name
		})
	}
	return m
}

func idLess(a, b ID) bool {
	if a.Dev != b.Dev {
		return a.Dev < b.Dev
	}
	if a.Ino != b.Ino {
		return a.Ino < b.Ino
	}
	return a.Seq < b.Seq
}

// absPath renders a link as an absolute root-relative path using the
// parent's own path in this snapshot (directories have exactly one).
func (s *Snapshot) absPath(l link) string {
	if l.parent == s.Root {
		return l.name
	}
	return s.Inodes[l.parent].Paths[0] + "/" + l.name
}

// Diff computes the operations transforming old into new.
//
// Matching is purely by inode ID: identical content under a different
// inode yields Delete+Create, never Move. For inodes present in both
// snapshots the sets of directory entries (parent inode, name) — the
// hard links — are compared: entries only in old are unlinked, entries
// only in new are linked, and when no common entry remains the first
// pair is reported as a Move. Because entries are compared structurally,
// a moved directory emits a single Move for itself while its children
// keep their entries and are still matched individually — internal
// modifications are never hidden.
func Diff(old, new *Snapshot) []Op {
	var ops []Op
	oldLinks, newLinks := old.linksOf(), new.linksOf()

	for id, ino := range old.Inodes {
		if _, ok := new.Inodes[id]; ok {
			continue
		}
		for _, l := range oldLinks[id] {
			ops = append(ops, Op{Kind: OpDelete, ID: id, Path: old.absPath(l), Parent: l.parent})
		}
		_ = ino
	}

	for id := range new.Inodes {
		if _, ok := old.Inodes[id]; ok {
			continue
		}
		ino := new.Inodes[id]
		for _, l := range newLinks[id] {
			ops = append(ops, Op{Kind: OpCreate, ID: id, Path: new.absPath(l), Parent: l.parent, Node: ino})
		}
	}

	for id, oldIno := range old.Inodes {
		newIno, ok := new.Inodes[id]
		if !ok {
			continue
		}
		if oldIno.Kind != newIno.Kind || oldIno.Mode != newIno.Mode || oldIno.Digest != newIno.Digest {
			ops = append(ops, Op{Kind: OpModify, ID: id, Node: newIno})
		}
		removed, added := linkDiff(oldLinks[id], newLinks[id])
		if len(removed) > 0 && len(added) > 0 {
			ops = append(ops, Op{
				Kind:       OpMove,
				ID:         id,
				From:       old.absPath(removed[0]),
				To:         new.absPath(added[0]),
				FromParent: removed[0].parent,
				Parent:     added[0].parent,
			})
			removed, added = removed[1:], added[1:]
		}
		for _, l := range removed {
			ops = append(ops, Op{Kind: OpUnlink, ID: id, Path: old.absPath(l), Parent: l.parent})
		}
		for _, l := range added {
			ops = append(ops, Op{Kind: OpLink, ID: id, Path: new.absPath(l), Parent: l.parent})
		}
	}

	sort.Slice(ops, func(i, j int) bool {
		a, b := ops[i], ops[j]
		if a.Path+a.From != b.Path+b.From {
			return a.Path+a.From < b.Path+b.From
		}
		return a.Kind < b.Kind
	})
	return ops
}

// linkDiff returns the links only in a (removed) and only in b (added);
// both inputs must be sorted as produced by linksOf.
func linkDiff(a, b []link) (removed, added []link) {
	aset := make(map[link]bool, len(a))
	for _, l := range a {
		aset[l] = true
	}
	bset := make(map[link]bool, len(b))
	for _, l := range b {
		bset[l] = true
		if !aset[l] {
			added = append(added, l)
		}
	}
	for _, l := range a {
		if !bset[l] {
			removed = append(removed, l)
		}
	}
	return removed, added
}
