package main

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
)

// 摘要树：按键空间划分的固定形状 Merkle 树。
// 键经 FNV-1a 散列后取前 TreeDepth 个十六进制位（nibble）作为路径，
// 即 16 叉、深度 4 的树，共 16^4 = 65536 个叶桶。
// 内部节点摘要 = 非空子节点 (序号, 摘要) 的散列；
// 叶桶摘要 = 桶内按键排序后全部记录的散列。
// 相同子树摘要必然相同，同步时整棵子树被剪枝，一个字节记录都不传。
const (
	TreeDepth  = 4
	TreeFanout = 16
)

// keyPath 返回键在摘要树中的路径（长度 TreeDepth，元素 0..15）。
func keyPath(key string) []int {
	h := fnv.New64a()
	h.Write([]byte(key))
	sum := h.Sum64()
	path := make([]int, TreeDepth)
	for i := 0; i < TreeDepth; i++ {
		shift := uint(64-4) - uint(4*i)
		path[i] = int(sum>>shift) & 0xf
	}
	return path
}

func hasPathPrefix(path, prefix []int) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, p := range prefix {
		if path[i] != p {
			return false
		}
	}
	return true
}

func appendPath(path []int, child int) []int {
	out := make([]int, len(path)+1)
	copy(out, path)
	out[len(path)] = child
	return out
}

func pathStr(path []int) string {
	if len(path) == 0 {
		return "/"
	}
	parts := make([]string, len(path))
	for i, p := range path {
		parts[i] = fmt.Sprintf("%x", p)
	}
	return "/" + strings.Join(parts, "/")
}

// digestAt 返回 path 处子树的摘要。
// ForceCollision 打开时所有节点返回同一常量——人为制造“摘要碰撞”，
// 用于验证复核路径（verify）在摘要完全不可信时仍能收敛。
func (n *Node) digestAt(path []int) uint64 {
	if n.sim.cfg.ForceCollision {
		return 0xC0111510C0111510
	}
	return n.computeDigest(path)
}

func (n *Node) computeDigest(path []int) uint64 {
	if len(path) == TreeDepth {
		recs := n.bucketRecords(path)
		if len(recs) == 0 {
			return 0
		}
		h := fnv.New64a()
		for _, r := range recs {
			writeRecordHash(h, r)
		}
		return h.Sum64()
	}
	// 只对含键的子节点递归；空子树摘要恒为 0。
	childDigests := map[int]uint64{}
	for key := range n.store.data {
		kp := keyPath(key)
		if !hasPathPrefix(kp, path) {
			continue
		}
		c := kp[len(path)]
		if _, ok := childDigests[c]; !ok {
			childDigests[c] = n.computeDigest(appendPath(path, c))
		}
	}
	if len(childDigests) == 0 {
		return 0
	}
	h := fnv.New64a()
	for c := 0; c < TreeFanout; c++ {
		d, ok := childDigests[c]
		if !ok {
			continue
		}
		var buf [9]byte
		buf[0] = byte(c)
		binary.LittleEndian.PutUint64(buf[1:], d)
		h.Write(buf[:])
	}
	return h.Sum64()
}

// bucketRecords 返回叶桶内全部记录（按键排序，保证摘要确定）。
func (n *Node) bucketRecords(path []int) []Record {
	var out []Record
	for _, r := range n.store.data {
		if hasPathPrefix(keyPath(r.Key), path) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func writeRecordHash(h interface{ Write([]byte) (int, error) }, r Record) {
	var vbuf [8]byte
	binary.LittleEndian.PutUint64(vbuf[:], r.Version)
	h.Write([]byte(r.Key))
	h.Write([]byte{0})
	h.Write([]byte(r.Value))
	h.Write([]byte{0})
	h.Write(vbuf[:])
	h.Write([]byte(r.Origin))
	h.Write([]byte{0})
	if r.Deleted {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
}
