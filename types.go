package netsec

import (
	"bytes"
	"fmt"
	"sort"
)

// BlockID 是不可变备份 DAG 中块的内容寻址标识。
type BlockID string

// Block 表示备份 DAG 中的一个不可变块。
// Parents 指向该块引用的底层块；备份分支头的祖先经 Parent 边可达。
type Block struct {
	ID      BlockID
	Parents []BlockID
}

// Branch 表示一条备份分支，Head 为该分支当前不可变快照的根块。
type Branch struct {
	Name string
	Head BlockID
}

// DAG 是备份宿主提供的不可变备份图：块经 Parent 边构成有向无环图，
// 每条分支从其 Head 出发可达一个祖先闭包。
type DAG struct {
	Blocks   map[BlockID]Block
	Branches []Branch
}

// BlockSet 是块标识的集合，描述隐私删除作业的目标数据块引用集合。
type BlockSet map[BlockID]struct{}

// NewBlockSet 用给定块标识构造目标引用集合。
func NewBlockSet(ids ...BlockID) BlockSet {
	set := make(BlockSet, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// Contains 报告集合是否包含指定块。
func (s BlockSet) Contains(id BlockID) bool {
	_, ok := s[id]
	return ok
}

// Add 将块加入集合。
func (s BlockSet) Add(id BlockID) {
	s[id] = struct{}{}
}

// Sorted 返回按字典序排列的块标识，保证报告输出确定。
func (s BlockSet) Sorted() []BlockID {
	ids := make([]BlockID, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sortIDs(ids)
	return ids
}

// String 输出确定性的集合文本表示。
func (s BlockSet) String() string {
	return fmt.Sprintf("%v", s.Sorted())
}

// Equal 判断两个块集合是否相等。
func (s BlockSet) Equal(other BlockSet) bool {
	if len(s) != len(other) {
		return false
	}
	for id := range s {
		if !other.Contains(id) {
			return false
		}
	}
	return true
}

// Path 是从某条备份分支头到某个目标块的一条具体可达引用路径。
type Path struct {
	Branch string
	Blocks []BlockID
}

// Target 返回路径终点，即被引用的目标数据块。
func (p Path) Target() BlockID {
	return p.Blocks[len(p.Blocks)-1]
}

// String 输出 branch: a->b->c 形式的确定性路径表示。
func (p Path) String() string {
	var buf bytes.Buffer
	buf.WriteString(p.Branch)
	buf.WriteString(": ")
	for i, id := range p.Blocks {
		if i > 0 {
			buf.WriteString("->")
		}
		buf.WriteString(string(id))
	}
	return buf.String()
}

func sortIDs(ids []BlockID) {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
}
