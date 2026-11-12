package netsec

import (
	"fmt"
	"sort"
)

// MaxEnumeratedPaths 限制单次全遍历枚举的简单路径数，防止大图上指数爆炸。
// 本包的小图全遍历对照面向可控规模的备份图；超限返回 ErrPathLimit。
const MaxEnumeratedPaths = 200_000

// ErrPathLimit 表示全遍历枚举路径数超过 MaxEnumeratedPaths。
var ErrPathLimit = fmt.Errorf("netsec: enumerated path limit %d exceeded", MaxEnumeratedPaths)

// ValidateDAG 校验备份图的结构不变量：
//   - 块键与块 ID 一致且无重复；
//   - 每条 Parent 边都指向存在的块；
//   - 全图无环（Parent 边构成严格 DAG）；
//   - 每条分支的 Head 指向存在的块，分支名唯一。
func ValidateDAG(dag *DAG) error {
	if dag == nil {
		return fmt.Errorf("%w: nil DAG", ErrDAGInvalid)
	}
	for key, block := range dag.Blocks {
		if key != block.ID {
			return fmt.Errorf("%w: map key %q does not match block ID %q", ErrDAGInvalid, key, block.ID)
		}
		seen := make(map[BlockID]struct{}, len(block.Parents))
		for _, parent := range block.Parents {
			if _, ok := dag.Blocks[parent]; !ok {
				return fmt.Errorf("%w: block %q references missing parent %q", ErrDAGInvalid, block.ID, parent)
			}
			if _, dup := seen[parent]; dup {
				return fmt.Errorf("%w: block %q lists duplicate parent %q", ErrDAGInvalid, block.ID, parent)
			}
			seen[parent] = struct{}{}
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[BlockID]int, len(dag.Blocks))
	var visit func(id BlockID) error
	visit = func(id BlockID) error {
		switch color[id] {
		case gray:
			return fmt.Errorf("%w: cycle detected at block %q", ErrDAGInvalid, id)
		case black:
			return nil
		}
		color[id] = gray
		for _, parent := range dag.Blocks[id].Parents {
			if err := visit(parent); err != nil {
				return err
			}
		}
		color[id] = black
		return nil
	}
	for _, id := range sortedBlockKeys(dag.Blocks) {
		if err := visit(id); err != nil {
			return err
		}
	}

	branchNames := make(map[string]struct{}, len(dag.Branches))
	for _, branch := range dag.Branches {
		if branch.Name == "" {
			return fmt.Errorf("%w: branch with empty name", ErrDAGInvalid)
		}
		if _, dup := branchNames[branch.Name]; dup {
			return fmt.Errorf("%w: duplicate branch name %q", ErrDAGInvalid, branch.Name)
		}
		branchNames[branch.Name] = struct{}{}
		if _, ok := dag.Blocks[branch.Head]; !ok {
			return fmt.Errorf("%w: branch %q head %q not found", ErrDAGInvalid, branch.Name, branch.Head)
		}
	}
	return nil
}

// EnumeratePaths 对单条分支做小图全遍历，返回从分支 Head 到 targets 中
// 任一目标块的全部简单路径。同一目标可经由多条路径（菱形/共享块）到达，
// 每条路径都单独返回；目标块自身也可作为路径中间块继续向下枚举。
func EnumeratePaths(dag *DAG, branch Branch, targets BlockSet) ([]Path, error) {
	if err := ValidateDAG(dag); err != nil {
		return nil, err
	}
	if _, ok := dag.Blocks[branch.Head]; !ok {
		return nil, fmt.Errorf("%w: branch %q head %q not found", ErrDAGInvalid, branch.Name, branch.Head)
	}
	var paths []Path
	onStack := map[BlockID]struct{}{branch.Head: {}}
	stack := []BlockID{branch.Head}

	var walk func(id BlockID) error
	walk = func(id BlockID) error {
		if targets.Contains(id) {
			paths = append(paths, Path{Branch: branch.Name, Blocks: cloneStack(stack)})
			if len(paths) > MaxEnumeratedPaths {
				return ErrPathLimit
			}
		}
		for _, parent := range dag.Blocks[id].Parents {
			if _, onPath := onStack[parent]; onPath {
				continue
			}
			onStack[parent] = struct{}{}
			stack = append(stack, parent)
			if err := walk(parent); err != nil {
				return err
			}
			stack = stack[:len(stack)-1]
			delete(onStack, parent)
		}
		return nil
	}
	if err := walk(branch.Head); err != nil {
		return nil, err
	}
	sortPaths(paths)
	return paths, nil
}

// ReachableBlocks 返回从分支 Head 经 Parent 边可达的全部块，含 Head 自身。
func ReachableBlocks(dag *DAG, head BlockID) (map[BlockID]struct{}, error) {
	if err := ValidateDAG(dag); err != nil {
		return nil, err
	}
	if _, ok := dag.Blocks[head]; !ok {
		return nil, fmt.Errorf("%w: head %q not found", ErrDAGInvalid, head)
	}
	reachable := make(map[BlockID]struct{})
	var walk func(id BlockID)
	walk = func(id BlockID) {
		if _, seen := reachable[id]; seen {
			return
		}
		reachable[id] = struct{}{}
		for _, parent := range dag.Blocks[id].Parents {
			walk(parent)
		}
	}
	walk(head)
	return reachable, nil
}

func sortedBlockKeys(blocks map[BlockID]Block) []BlockID {
	ids := make([]BlockID, 0, len(blocks))
	for id := range blocks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func cloneStack(stack []BlockID) []BlockID {
	out := make([]BlockID, len(stack))
	copy(out, stack)
	return out
}

func sortPaths(paths []Path) {
	sort.Slice(paths, func(i, j int) bool {
		a, b := paths[i], paths[j]
		if a.Branch != b.Branch {
			return a.Branch < b.Branch
		}
		n := len(a.Blocks)
		if len(b.Blocks) < n {
			n = len(b.Blocks)
		}
		for k := 0; k < n; k++ {
			if a.Blocks[k] != b.Blocks[k] {
				return a.Blocks[k] < b.Blocks[k]
			}
		}
		return len(a.Blocks) < len(b.Blocks)
	})
}
