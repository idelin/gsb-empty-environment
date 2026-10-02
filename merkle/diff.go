package merkle

import "sort"

// ChangeKind 表示一条叶子差异的类型。
type ChangeKind int

const (
	// Added 表示该下标的记录只存在于新版本中。
	Added ChangeKind = iota
	// Removed 表示该下标的记录只存在于旧版本中。
	Removed
	// Modified 表示该下标的记录内容在两个版本间被修改。
	Modified
)

func (k ChangeKind) String() string {
	switch k {
	case Added:
		return "ADDED"
	case Removed:
		return "REMOVED"
	case Modified:
		return "MODIFIED"
	}
	return "UNKNOWN"
}

// Change 描述一条变动叶子。
type Change struct {
	Index uint64
	Kind  ChangeKind
	Old   Hash // 旧版本叶子哈希；Added 时为 Zero
	New   Hash // 新版本叶子哈希；Removed 时为 Zero
}

// Stats 记录一次比对的工作量，用于验证“只展开少量节点”。
type Stats struct {
	RootsCompared int // 根哈希比较次数（恒为 1）
	NodesExpanded int // 被展开（下载其子节点）的内部节点数
	HashesFetched int // 展开过程中下载的子节点哈希数
	LeavesEmitted int // 输出的变动叶子数
}

// Result 是差异比对的结果。
type Result struct {
	Changes []Change // 按下标升序排列的变动叶子列表
	Stats   Stats
}

// Diff 自顶向下比较两棵默克尔树，返回两版本间的全部变动叶子。
//
// 若两根相同，直接判定全等，不展开任何节点；
// 否则沿哈希不一致的分支逐层二分下钻，每层只展开哈希不同的节点，
// 哈希相同的子树被整体剪枝，绝不触碰其下的叶子。
func Diff(oldTree, newTree *Tree) *Result {
	res := &Result{}
	res.Stats.RootsCompared = 1
	if oldTree.Root() == newTree.Root() {
		return res // 根相同 → 秒级判定全等
	}

	type frame struct{ level, idx int }
	top := oldTree.Height()
	if newTree.Height() > top {
		top = newTree.Height()
	}
	stack := []frame{{level: top - 1, idx: 0}}

	emit := func(idx int) {
		oh := oldTree.effectiveHash(0, idx)
		nh := newTree.effectiveHash(0, idx)
		var kind ChangeKind
		switch {
		case oh == Zero:
			kind = Added
		case nh == Zero:
			kind = Removed
		default:
			kind = Modified
		}
		res.Changes = append(res.Changes, Change{
			Index: uint64(idx), Kind: kind, Old: oh, New: nh,
		})
	}

	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if f.level == 0 {
			emit(f.idx) // 根即叶子（树高为 1）的退化情形
			continue
		}
		res.Stats.NodesExpanded++
		for j := 0; j < Fanout; j++ {
			child := f.idx*Fanout + j
			oh := oldTree.effectiveHash(f.level-1, child)
			nh := newTree.effectiveHash(f.level-1, child)
			res.Stats.HashesFetched += 2
			if oh == nh {
				continue // 子树全等，剪枝
			}
			if f.level-1 == 0 {
				emit(child)
			} else {
				stack = append(stack, frame{level: f.level - 1, idx: child})
			}
		}
	}

	sort.Slice(res.Changes, func(i, j int) bool {
		return res.Changes[i].Index < res.Changes[j].Index
	})
	res.Stats.LeavesEmitted = len(res.Changes)
	return res
}
