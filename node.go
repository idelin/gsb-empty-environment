package trie

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
)

// NodeID 是磁盘节点的内容寻址引用：节点编码内容的 SHA-256。
// 引用即承诺：加载时重新计算哈希即可验证引用是否有效。
// 零值表示空树（不存在的节点）。
type NodeID struct {
	h [32]byte
}

const idHexLen = 64

var zeroID NodeID

// IsZero 报告该引用是否为空树根。
func (n NodeID) IsZero() bool { return n == zeroID }

// Bytes 返回哈希的拷贝。
func (n NodeID) Bytes() []byte {
	b := make([]byte, sha256.Size)
	copy(b, n.h[:])
	return b
}

// Hex 返回十六进制形式，用作节点文件名。
func (n NodeID) Hex() string {
	if n.IsZero() {
		return ""
	}
	return hex.EncodeToString(n.h[:])
}

func (n NodeID) String() string {
	if n.IsZero() {
		return "NodeID(empty)"
	}
	hx := n.Hex()
	return "NodeID(" + hx[:12] + "…)"
}

// ParseNodeID 解析十六进制引用。
func ParseNodeID(s string) (NodeID, error) {
	if s == "" {
		return zeroID, nil
	}
	raw, err := hex.DecodeString(s)
	if err != nil {
		return zeroID, fmt.Errorf("trie: 节点引用不是合法十六进制: %v", err)
	}
	if len(raw) != sha256.Size {
		return zeroID, fmt.Errorf("trie: 节点引用长度应为 %d 字节, 实际 %d", sha256.Size, len(raw))
	}
	var id NodeID
	copy(id.h[:], raw)
	return id, nil
}

func idFromBytes(b []byte) NodeID {
	var id NodeID
	copy(id.h[:], b)
	return id
}

// maxValueLen / maxChildCount 限制单个节点大小，防止损坏数据耗尽内存。
const (
	nodeVersion  = 1
	nodeMagic    = "IMTN"
	maxValueLen  = 1 << 24 // 16 MiB
	maxChildCnt  = 256
	maxNodeBytes = 1 << 28 // 256 MiB 编码上限的防御性检查
)

// node 是内存中的前缀树节点。
//   - 分支不是单字节边标签：边标签隐含在父节点 children 的键中。
//   - hasVal 为 true 时 value 是该节点自身（终止于该前缀）的有效载荷。
//   - 空键 "" 的值存放在根节点的 value 中。
//
// 磁盘节点是不可变的：任何修改都生成新节点（路径复制），旧引用仍然有效。
type node struct {
	hasVal bool
	value  []byte
	// children: 下一个路径字节 -> 子节点内容引用。
	// 稀疏存储（map）本身就是删除压缩：被清空的边不占用磁盘空间。
	children map[byte]NodeID
}

func cloneNode(n *node) *node {
	c := &node{hasVal: n.hasVal, value: n.value}
	if len(n.children) > 0 {
		c.children = make(map[byte]NodeID, len(n.children))
		for b, id := range n.children {
			c.children[b] = id
		}
	}
	return c
}

/*
磁盘节点格式（小端）：

	magic   [4]byte = "IMTN"
	version uint8   = 1
	flags   uint8   = bit0: hasVal
	valLen  uint32
	value   [valLen]byte
	nChild  uint16
	entry*  nChild 个，按字节标签升序：
	    tag    uint8
	    id     [32]byte
*/

func (n *node) encode() []byte {
	nChild := len(n.children)
	tags := make([]byte, 0, nChild)
	for b := range n.children {
		tags = append(tags, b)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })

	size := len(nodeMagic) + 2 + 4 + len(n.value) + 2 + nChild*(1+sha256.Size)
	buf := make([]byte, 0, size)
	buf = append(buf, nodeMagic...)
	buf = append(buf, nodeVersion)
	var flags byte
	if n.hasVal {
		flags |= 1
	}
	buf = append(buf, flags)
	var l [4]byte
	binary.LittleEndian.PutUint32(l[:], uint32(len(n.value)))
	buf = append(buf, l[:]...)
	buf = append(buf, n.value...)
	var c [2]byte
	binary.LittleEndian.PutUint16(c[:], uint16(nChild))
	buf = append(buf, c[:]...)
	for _, b := range tags {
		buf = append(buf, b)
		id := n.children[b]
		buf = append(buf, id.h[:]...)
	}
	return buf
}

func decodeNode(raw []byte) (*node, error) {
	if len(raw) > maxNodeBytes {
		return nil, fmt.Errorf("trie: 节点编码过大: %d 字节", len(raw))
	}
	r := bytes.NewReader(raw)
	magic := make([]byte, len(nodeMagic))
	if _, err := r.Read(magic); err != nil {
		return nil, errors.New("trie: 节点过短, 缺少魔数")
	}
	if string(magic) != nodeMagic {
		return nil, errors.New("trie: 节点魔数不匹配")
	}
	ver, err := r.ReadByte()
	if err != nil {
		return nil, errors.New("trie: 节点过短, 缺少版本")
	}
	if ver != nodeVersion {
		return nil, fmt.Errorf("trie: 不支持的节点版本 %d", ver)
	}
	flags, err := r.ReadByte()
	if err != nil {
		return nil, errors.New("trie: 节点过短, 缺少标志位")
	}
	if flags&^1 != 0 {
		return nil, fmt.Errorf("trie: 节点含未知标志位 0x%02x", flags)
	}
	var lb [4]byte
	if _, err := io.ReadFull(r, lb[:]); err != nil {
		return nil, errors.New("trie: 节点过短, 缺少值长度")
	}
	valLen := binary.LittleEndian.Uint32(lb[:])
	if int(valLen) > maxValueLen {
		return nil, fmt.Errorf("trie: 值长度超限: %d", valLen)
	}
	val := make([]byte, valLen)
	if _, err := io.ReadFull(r, val); err != nil {
		return nil, errors.New("trie: 节点过短, 值被截断")
	}
	var cb [2]byte
	if _, err := io.ReadFull(r, cb[:]); err != nil {
		return nil, errors.New("trie: 节点过短, 缺少子节点计数")
	}
	nChild := int(binary.LittleEndian.Uint16(cb[:]))
	if nChild > maxChildCnt {
		return nil, fmt.Errorf("trie: 子节点计数超限: %d", nChild)
	}
	children := make(map[byte]NodeID, nChild)
	for i := 0; i < nChild; i++ {
		tag, err := r.ReadByte()
		if err != nil {
			return nil, errors.New("trie: 节点过短, 子条目被截断")
		}
		var id [sha256.Size]byte
		if _, err := io.ReadFull(r, id[:]); err != nil {
			return nil, errors.New("trie: 节点过短, 子引用被截断")
		}
		var nid NodeID
		copy(nid.h[:], id[:])
		children[tag] = nid
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("trie: 节点编码含 %d 字节尾部多余数据", r.Len())
	}
	out := &node{hasVal: flags&1 != 0, value: val}
	if nChild > 0 {
		out.children = children
	}
	return out, nil
}

// hashNode 计算节点的内容寻址引用。
func hashNode(enc []byte) NodeID {
	sum := sha256.Sum256(enc)
	return idFromBytes(sum[:])
}
