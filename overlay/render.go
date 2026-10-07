package overlay

import (
	"fmt"
	"strings"
)

// Render 以缩进树形式输出最终树，逐节点标注来源层与 inode；
// 同层硬链接的第二次及以后出现渲染为 “-> &inode”。
func Render(root *Node) string {
	var b strings.Builder
	seen := map[uint64]string{}
	renderNode(&b, root.Name, root, 0, seen)
	return b.String()
}

// renderNode 以父目录中的键名 name 渲染节点：硬链接共享同一
// *Node，节点自身的 Name 只是首次命名，展示须用路径名。
func renderNode(b *strings.Builder, name string, n *Node, depth int, seen map[uint64]string) {
	indent := strings.Repeat("  ", depth)
	if n.Kind != KindDir {
		if first, ok := seen[n.Inode]; ok {
			fmt.Fprintf(b, "%s%s -> &%d (hardlink, 同 %s) [layer %d]\n",
				indent, name, n.Inode, first, n.Layer)
			return
		}
		seen[n.Inode] = name
	}
	switch n.Kind {
	case KindDir:
		fmt.Fprintf(b, "%s%s/ (dir) [layer %d]\n", indent, name, n.Layer)
		for _, childName := range sortedNames(n.Children) {
			renderNode(b, childName, n.Children[childName], depth+1, seen)
		}
	case KindSymlink:
		fmt.Fprintf(b, "%s%s (symlink -> %s) [layer %d] inode=%d\n",
			indent, name, n.Data, n.Layer, n.Inode)
	default:
		fmt.Fprintf(b, "%s%s (file) [layer %d] inode=%d %q\n",
			indent, name, n.Layer, n.Inode, n.Data)
	}
}
