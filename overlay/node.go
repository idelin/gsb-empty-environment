// Package overlay 实现 overlayfs 风格的虚拟层合并：
// 多层目录树按顺序叠加，白化项删除下层同名节点，不透明目录屏蔽
// 下层子项，硬链接仅在层内解析。
package overlay

import (
	"fmt"
	"sort"
	"strings"
)

// Kind 是节点类型。
type Kind int

const (
	KindFile Kind = iota
	KindDir
	KindSymlink
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindDir:
		return "dir"
	case KindSymlink:
		return "symlink"
	}
	return "unknown"
}

// Node 是虚拟树中的一个节点。同一层内硬链接共享同一个 *Node
// （同一 Inode），合并后共享关系保持不变。
type Node struct {
	Name     string
	Kind     Kind
	Data     string // 文件内容或符号链接目标
	Inode    uint64 // 层内 inode 标识
	Children map[string]*Node

	// Whiteout 为 true 表示该节点是白化项：合并时删除下层同名节点，
	// 自身不出现在结果中（对应 overlayfs 的 .wh.<name>）。
	Whiteout bool
	// Opaque 仅对目录有效：屏蔽下层同名目录的全部子项，但保留本层
	// 子项（对应 overlayfs 的 .wh..wh..opq）。
	Opaque bool

	// Layer 是来源层序号，由 Merge 填写；-1 表示未合并。
	Layer int
}

// Layer 是一层目录树，硬链接目标仅在本层 inode 表内解析。
type Layer struct {
	Root   *Node
	inodes map[uint64]*Node
	next   uint64
}

// NewLayer 创建空层。
func NewLayer() *Layer {
	l := &Layer{inodes: map[uint64]*Node{}}
	l.Root = &Node{Name: "/", Kind: KindDir, Children: map[string]*Node{}, Layer: -1}
	l.Root.Inode = l.alloc(l.Root)
	return l
}

func (l *Layer) alloc(n *Node) uint64 {
	l.next++
	n.Inode = l.next
	l.inodes[n.Inode] = n
	return n.Inode
}

func split(path string) []string {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	return parts
}

// Mkdir 创建路径上的所有目录并返回末级目录。
func (l *Layer) Mkdir(path string) *Node {
	cur := l.Root
	for _, p := range split(path) {
		child, ok := cur.Children[p]
		if !ok || child.Kind != KindDir {
			child = &Node{Name: p, Kind: KindDir, Children: map[string]*Node{}, Layer: -1}
			l.alloc(child)
			cur.Children[p] = child
		}
		cur = child
	}
	return cur
}

func (l *Layer) parent(path string) (*Node, string, error) {
	parts := split(path)
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("overlay: 路径 %q 无父目录", path)
	}
	dir := l.Mkdir(strings.Join(parts[:len(parts)-1], "/"))
	return dir, parts[len(parts)-1], nil
}

// File 在 path 放置普通文件（分配新 inode）。
func (l *Layer) File(path, data string) *Node {
	dir, name, err := l.parent(path)
	if err != nil {
		panic(err)
	}
	n := &Node{Name: name, Kind: KindFile, Data: data, Layer: -1}
	l.alloc(n)
	dir.Children[name] = n
	return n
}

// Symlink 在 path 放置符号链接。
func (l *Layer) Symlink(path, target string) *Node {
	dir, name, err := l.parent(path)
	if err != nil {
		panic(err)
	}
	n := &Node{Name: name, Kind: KindSymlink, Data: target, Layer: -1}
	l.alloc(n)
	dir.Children[name] = n
	return n
}

// Link 在 path 放置指向 target 的硬链接。target 必须属于同一层：
// 硬链接仅在层内解析，跨层引用在本模型中不可构造。
func (l *Layer) Link(path string, target *Node) error {
	if target.Kind == KindDir {
		return fmt.Errorf("overlay: 不允许对目录建立硬链接")
	}
	if l.inodes[target.Inode] != target {
		return fmt.Errorf("overlay: inode %d 不在本层，硬链接仅层内解析", target.Inode)
	}
	dir, name, err := l.parent(path)
	if err != nil {
		return err
	}
	dir.Children[name] = target
	return nil
}

// LinkInode 按 inode 号建立硬链接，演示层内解析：inode 必须在
// 本层 inode 表中，否则返回错误（绝不回退到下层）。
func (l *Layer) LinkInode(path string, inode uint64) error {
	target, ok := l.inodes[inode]
	if !ok {
		return fmt.Errorf("overlay: inode %d 在本层不存在，无法解析硬链接", inode)
	}
	return l.Link(path, target)
}

// Whiteout 在 path 放置白化项，合并时删除下层同名节点。
func (l *Layer) Whiteout(path string) {
	dir, name, err := l.parent(path)
	if err != nil {
		panic(err)
	}
	n := &Node{Name: name, Kind: KindFile, Whiteout: true, Layer: -1}
	l.alloc(n)
	dir.Children[name] = n
}

// MarkOpaque 将 path 处的目录标记为不透明。
func (l *Layer) MarkOpaque(path string) {
	l.Mkdir(path).Opaque = true
}

// Lookup 返回层内 path 处的节点，不存在返回 nil。
func (l *Layer) Lookup(path string) *Node {
	cur := l.Root
	for _, p := range split(path) {
		child, ok := cur.Children[p]
		if !ok {
			return nil
		}
		cur = child
	}
	return cur
}

// sortedNames 返回排序后的子节点名，保证渲染与测试确定性。
func sortedNames(m map[string]*Node) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
