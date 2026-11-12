package netsec

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind 是字段树节点的类型标签。
type Kind uint8

const (
	KindNull Kind = iota
	KindInt
	KindString
	KindBytes
	KindList
	KindMap
	// KindSig 是签名节点：Covers 声明覆盖路径，Bytes 存放 MAC。
	KindSig
)

// Node 是带类型字段树的节点。
type Node struct {
	Kind  Kind
	Num   int64
	Str   string
	Bytes []byte
	List  []*Node
	Map   map[string]*Node

	// 仅 KindSig 使用：声明被签名覆盖的路径。
	Covers []string
}

// Str 构造字符串叶子。
func Str(s string) *Node { return &Node{Kind: KindString, Str: s} }

// Int 构造整数叶子。
func Int(v int64) *Node { return &Node{Kind: KindInt, Num: v} }

// MapOf 构造映射节点。
func MapOf(kv map[string]*Node) *Node { return &Node{Kind: KindMap, Map: kv} }

// Clone 深拷贝整棵子树。
func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}
	c := *n
	if n.Bytes != nil {
		c.Bytes = append([]byte(nil), n.Bytes...)
	}
	if n.Covers != nil {
		c.Covers = append([]string(nil), n.Covers...)
	}
	if n.List != nil {
		c.List = make([]*Node, len(n.List))
		for i, ch := range n.List {
			c.List[i] = ch.Clone()
		}
	}
	if n.Map != nil {
		c.Map = make(map[string]*Node, len(n.Map))
		for k, ch := range n.Map {
			c.Map[k] = ch.Clone()
		}
	}
	return &c
}

// Canonical 返回确定性编码：类型标签 + 内容，映射键按字典序。
// 签名节点只编码其覆盖声明，不编码 MAC 本身，避免自引用。
func Canonical(n *Node) []byte {
	var buf []byte
	encodeNode(&buf, n)
	return buf
}

func encodeNode(buf *[]byte, n *Node) {
	*buf = append(*buf, byte(n.Kind))
	switch n.Kind {
	case KindInt:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n.Num))
		*buf = append(*buf, b[:]...)
	case KindString:
		writeRaw(buf, []byte(n.Str))
	case KindBytes:
		writeRaw(buf, n.Bytes)
	case KindList:
		writeLen(buf, len(n.List))
		for _, ch := range n.List {
			encodeNode(buf, ch)
		}
	case KindMap:
		keys := make([]string, 0, len(n.Map))
		for k := range n.Map {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		writeLen(buf, len(keys))
		for _, k := range keys {
			writeRaw(buf, []byte(k))
			encodeNode(buf, n.Map[k])
		}
	case KindSig:
		writeLen(buf, len(n.Covers))
		for _, p := range n.Covers {
			writeRaw(buf, []byte(p))
		}
	}
}

func writeRaw(buf *[]byte, b []byte) {
	writeLen(buf, len(b))
	*buf = append(*buf, b...)
}

func writeLen(buf *[]byte, l int) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(l))
	*buf = append(*buf, b[:]...)
}

// Equal 按规范编码比较两棵子树。
func Equal(a, b *Node) bool {
	return bytes.Equal(Canonical(a), Canonical(b))
}

// Get 按点分路径取子树，如 "body.to"；列表段用十进制下标。
func (n *Node) Get(path string) (*Node, error) {
	cur := n
	if path == "" {
		return cur, nil
	}
	for _, seg := range strings.Split(path, ".") {
		switch cur.Kind {
		case KindMap:
			ch, ok := cur.Map[seg]
			if !ok {
				return nil, fmt.Errorf("netsec: path %q: 缺少键 %q", path, seg)
			}
			cur = ch
		case KindList:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(cur.List) {
				return nil, fmt.Errorf("netsec: path %q: 下标 %q 越界", path, seg)
			}
			cur = cur.List[i]
		default:
			return nil, fmt.Errorf("netsec: path %q: 在叶子处中断于 %q", path, seg)
		}
	}
	return cur, nil
}

// Set 按点分路径替换子树（路径必须已存在）。
func (n *Node) Set(path string, v *Node) error {
	if path == "" {
		*n = *v
		return nil
	}
	segs := strings.Split(path, ".")
	parent, err := n.Get(strings.Join(segs[:len(segs)-1], "."))
	if err != nil {
		return err
	}
	last := segs[len(segs)-1]
	switch parent.Kind {
	case KindMap:
		if _, ok := parent.Map[last]; !ok {
			return fmt.Errorf("netsec: path %q: 缺少键 %q", path, last)
		}
		parent.Map[last] = v
	case KindList:
		i, err := strconv.Atoi(last)
		if err != nil || i < 0 || i >= len(parent.List) {
			return fmt.Errorf("netsec: path %q: 下标 %q 越界", path, last)
		}
		parent.List[i] = v
	default:
		return fmt.Errorf("netsec: path %q: 父节点不是容器", path)
	}
	return nil
}

// Paths 收集子树中所有路径（含内部节点），根为 ""。
func Paths(n *Node) []string {
	var out []string
	walkPaths(n, "", &out)
	return out
}

func walkPaths(n *Node, prefix string, out *[]string) {
	*out = append(*out, prefix)
	switch n.Kind {
	case KindMap:
		keys := make([]string, 0, len(n.Map))
		for k := range n.Map {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkPaths(n.Map[k], joinPath(prefix, k), out)
		}
	case KindList:
		for i, ch := range n.List {
			walkPaths(ch, joinPath(prefix, strconv.Itoa(i)), out)
		}
	}
}

func joinPath(prefix, seg string) string {
	if prefix == "" {
		return seg
	}
	return prefix + "." + seg
}
