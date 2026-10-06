package fsdiff

import (
	"sort"
	"strings"
)

// OpKind classifies a diff operation.
type OpKind int

const (
	OpRemove     OpKind = iota // inode vanished; emitted per removed path
	OpLinkRemove               // one hard-link path removed, inode survives
	OpMove                     // single-path inode renamed OldPath -> Path
	OpModify                   // same inode, content hash changed
	OpLinkAdd                  // new hard-link path for an existing inode
	OpAdd                      // new inode appeared; emitted per path
)

func (k OpKind) String() string {
	switch k {
	case OpRemove:
		return "remove"
	case OpLinkRemove:
		return "link-remove"
	case OpMove:
		return "move"
	case OpModify:
		return "modify"
	case OpLinkAdd:
		return "link-add"
	case OpAdd:
		return "add"
	}
	return "unknown"
}

// Op is a single difference between two snapshots.
type Op struct {
	Kind    OpKind
	Inode   uint64
	Path    string // affected path (destination for Move/LinkAdd/Add)
	OldPath string // source path for Move
	NewHash string // content hash for Add/Modify
	IsDir   bool
}

// kindOrder sequences ops so Apply never sees a destination still occupied
// by a stale entry: removals first, then moves, then additions.
var kindOrder = map[OpKind]int{
	OpRemove:     0,
	OpLinkRemove: 1,
	OpMove:       2,
	OpModify:     3,
	OpLinkAdd:    4,
	OpAdd:        5,
}

func subtract(a, b []string) []string {
	inB := map[string]bool{}
	for _, s := range b {
		inB[s] = true
	}
	var out []string
	for _, s := range a {
		if !inB[s] {
			out = append(out, s)
		}
	}
	return out
}

// Diff computes the operations transforming snapshot a into snapshot b.
// Identity is inode-based: matching content under a different inode is
// reported as remove+add, never as a move.
func Diff(a, b *Snapshot) []Op {
	var ops []Op

	inodes := map[uint64]bool{}
	for ino := range a.Inodes {
		inodes[ino] = true
	}
	for ino := range b.Inodes {
		inodes[ino] = true
	}
	sorted := make([]uint64, 0, len(inodes))
	for ino := range inodes {
		sorted = append(sorted, ino)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	for _, ino := range sorted {
		aPaths, inA := a.Inodes[ino]
		bPaths, inB := b.Inodes[ino]
		switch {
		case inA && !inB:
			for _, p := range aPaths {
				ops = append(ops, Op{Kind: OpRemove, Inode: ino, Path: p, IsDir: a.Entries[p].IsDir})
			}
		case !inA && inB:
			for _, p := range bPaths {
				e := b.Entries[p]
				ops = append(ops, Op{Kind: OpAdd, Inode: ino, Path: p, NewHash: e.Hash, IsDir: e.IsDir})
			}
		default:
			removed := subtract(aPaths, bPaths)
			added := subtract(bPaths, aPaths)
			if len(aPaths) == 1 && len(bPaths) == 1 && len(removed) == 1 && len(added) == 1 {
				ops = append(ops, Op{Kind: OpMove, Inode: ino, OldPath: removed[0], Path: added[0], IsDir: b.Entries[added[0]].IsDir})
			} else {
				for _, p := range removed {
					ops = append(ops, Op{Kind: OpLinkRemove, Inode: ino, Path: p, IsDir: a.Entries[p].IsDir})
				}
				for _, p := range added {
					ops = append(ops, Op{Kind: OpLinkAdd, Inode: ino, Path: p, IsDir: b.Entries[p].IsDir})
				}
			}
			oldEnt := a.Entries[aPaths[0]]
			newEnt := b.Entries[bPaths[0]]
			if !newEnt.IsDir && oldEnt.Hash != newEnt.Hash {
				ops = append(ops, Op{Kind: OpModify, Inode: ino, Path: bPaths[0], NewHash: newEnt.Hash})
			}
		}
	}

	ops = suppressImpliedMoves(ops)

	sort.SliceStable(ops, func(i, j int) bool {
		if kindOrder[ops[i].Kind] != kindOrder[ops[j].Kind] {
			return kindOrder[ops[i].Kind] < kindOrder[ops[j].Kind]
		}
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].OldPath < ops[j].OldPath
	})
	return ops
}

// suppressImpliedMoves drops move ops whose path change is fully explained by
// an ancestor directory move. A modify op for the same inode is kept, so a
// directory move never hides content changes beneath it.
func suppressImpliedMoves(ops []Op) []Op {
	dirMoves := map[string]string{}
	for _, op := range ops {
		if op.Kind == OpMove && op.IsDir {
			dirMoves[op.OldPath] = op.Path
		}
	}
	if len(dirMoves) == 0 {
		return ops
	}
	out := ops[:0]
	for _, op := range ops {
		if op.Kind == OpMove && !op.IsDir && impliedPath(op.OldPath, dirMoves) == op.Path {
			continue
		}
		out = append(out, op)
	}
	return out
}

// impliedPath rewrites oldPath through directory moves (longest prefix first,
// repeated to fixpoint) yielding the path it would have if it moved only
// because its ancestors did.
func impliedPath(oldPath string, dirMoves map[string]string) string {
	p := oldPath
	for i := 0; i < len(dirMoves)+1; i++ {
		best := ""
		for old := range dirMoves {
			if (p == old || strings.HasPrefix(p, old+"/")) && len(old) > len(best) {
				best = old
			}
		}
		if best == "" {
			return p
		}
		p = dirMoves[best] + p[len(best):]
	}
	return p
}
