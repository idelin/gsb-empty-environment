package main

import "sort"

// Node 维护本地成员表与增量哈希环 Digest。
// 每个环区间维护一个 XOR 折叠的 4 字节摘要，
// 成员增删改只需 O(1) 地从对应区间摘要中异或移除旧值、异或加入新值。
type Node struct {
	ID      string
	members map[string]Member
	buckets [NumBuckets]uint32
}

func NewNode(id string) *Node {
	return &Node{ID: id, members: make(map[string]Member)}
}

// Upsert 按 last-writer-wins 合并一条成员记录，返回本地状态是否发生变化。
func (n *Node) Upsert(m Member) bool {
	old, ok := n.members[m.ID]
	if ok && old.Version >= m.Version {
		return false
	}
	b := bucketOf(m.ID)
	if ok {
		n.buckets[b] ^= memberDigest(old)
	}
	n.buckets[b] ^= memberDigest(m)
	n.members[m.ID] = m
	return true
}

// Digest 返回全环 4 字节 CRC 摘要（各区间摘要的 XOR 折叠）。
func (n *Node) Digest() uint32 {
	var d uint32
	for _, b := range n.buckets {
		d ^= b
	}
	return d
}

// BucketDigests 返回各环区间的摘要，用于失配后的区间对账。
func (n *Node) BucketDigests() [NumBuckets]uint32 {
	return n.buckets
}

// MembersInBucket 返回指定环区间内的成员记录（按 ID 排序，保证传输确定性）。
func (n *Node) MembersInBucket(b int) []Member {
	var out []Member
	for _, m := range n.members {
		if bucketOf(m.ID) == b {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Members 返回全部成员记录的快照（按 ID 排序）。
func (n *Node) Members() []Member {
	out := make([]Member, 0, len(n.members))
	for _, m := range n.members {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
