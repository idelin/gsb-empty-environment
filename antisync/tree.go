package antisync

import (
	"encoding/binary"
	"hash/fnv"
	"math"
)

const (
	// MaxDepth 限制树的最大深度，到达后不再分裂，叶节点可超量容纳记录。
	MaxDepth = 32
	// SplitThreshold 是叶节点记录数上限，超过即按区间中点分裂。
	SplitThreshold = 4
)

// node 是固定范围摘要树的节点。键空间固定为 [0, math.MaxUint64]，
// 区间按中点二分，分裂规则只取决于键集合本身，与插入顺序无关。
type node struct {
	lo, hi   uint64 // 闭区间 [lo, hi]
	depth    int
	leaf     bool
	records  map[uint64]Record // 仅叶节点持有
	children [2]*node          // 仅内部节点持有
	count    int               // 子树记录总数
	hash     uint64            // 子树摘要
}

func newNode(lo, hi uint64, depth int) *node {
	n := &node{lo: lo, hi: hi, depth: depth, leaf: true, records: make(map[uint64]Record)}
	n.rehash()
	return n
}

// insert 将记录放入对应叶节点，必要时触发分裂。
func (n *node) insert(rec Record) {
	if n.leaf {
		n.records[rec.Key] = rec
		if len(n.records) > SplitThreshold && n.depth < MaxDepth {
			n.split()
		}
		n.rehash()
		return
	}
	n.childFor(rec.Key).insert(rec)
	n.rehash()
}

func (n *node) childFor(key uint64) *node {
	if key <= n.children[0].hi {
		return n.children[0]
	}
	return n.children[1]
}

// split 按区间中点把叶节点一分为二；若某一侧仍然超量则递归分裂，
// 保证最终树形只由键集合决定。
func (n *node) split() {
	mid := n.lo + (n.hi-n.lo)/2
	left := newNode(n.lo, mid, n.depth+1)
	right := newNode(mid+1, n.hi, n.depth+1)
	for _, r := range n.records {
		if r.Key <= mid {
			left.records[r.Key] = r
		} else {
			right.records[r.Key] = r
		}
	}
	for _, c := range [2]*node{left, right} {
		if len(c.records) > SplitThreshold && c.depth < MaxDepth {
			c.split()
		}
		c.rehash()
	}
	n.children = [2]*node{left, right}
	n.records = nil
	n.leaf = false
}

// rehash 自底向上重算节点摘要。叶摘要 = 区间 + 记录数 + 记录哈希异或；
// 内部摘要 = 区间 + 记录数 + 两个子节点摘要。异或使叶摘要与遍历顺序无关。
func (n *node) rehash() {
	h := fnv.New64a()
	var buf [24]byte
	binary.BigEndian.PutUint64(buf[0:8], n.lo)
	binary.BigEndian.PutUint64(buf[8:16], n.hi)
	if n.leaf {
		n.count = len(n.records)
		var xor uint64
		for _, r := range n.records {
			xor ^= r.hash()
		}
		binary.BigEndian.PutUint64(buf[16:24], uint64(n.count))
		h.Write(buf[:])
		h.Write([]byte{0})
		var xb [8]byte
		binary.BigEndian.PutUint64(xb[:], xor)
		h.Write(xb[:])
	} else {
		n.count = n.children[0].count + n.children[1].count
		binary.BigEndian.PutUint64(buf[16:24], uint64(n.count))
		h.Write(buf[:])
		h.Write([]byte{1})
		var cb [8]byte
		binary.BigEndian.PutUint64(cb[:], n.children[0].hash)
		h.Write(cb[:])
		binary.BigEndian.PutUint64(cb[:], n.children[1].hash)
		h.Write(cb[:])
	}
	n.hash = h.Sum64()
}

// find 按键查找记录，墓碑也会返回（ok 为 true 且 Deleted 为 true）。
func (n *node) find(key uint64) (Record, bool) {
	if n.leaf {
		r, ok := n.records[key]
		return r, ok
	}
	return n.childFor(key).find(key)
}

// collect 收集子树内全部记录（含墓碑）。
func (n *node) collect(out *[]Record) {
	if n.leaf {
		for _, r := range n.records {
			*out = append(*out, r)
		}
		return
	}
	n.children[0].collect(out)
	n.children[1].collect(out)
}

// Replica 表示一个副本：一棵覆盖完整键空间的摘要树即其存储。
type Replica struct {
	root *node
}

func NewReplica() *Replica {
	return &Replica{root: newNode(0, math.MaxUint64, 0)}
}

// Apply 按冲突规则合并一条记录（用于本地写入与同步合并）。
func (r *Replica) Apply(rec Record) {
	if existing, ok := r.root.find(rec.Key); ok && !wins(rec, existing) {
		return
	}
	r.root.insert(rec)
}

// Put 写入一个版本化的键值对。
func (r *Replica) Put(key uint64, value []byte, version uint64) {
	r.Apply(Record{Key: key, Value: value, Version: version})
}

// Delete 写入删除墓碑，墓碑与正常记录一样进入摘要树。
func (r *Replica) Delete(key uint64, version uint64) {
	r.Apply(Record{Key: key, Version: version, Deleted: true})
}

// Get 读取键值；键不存在或已被删除（墓碑）时 ok 为 false。
func (r *Replica) Get(key uint64) ([]byte, bool) {
	rec, ok := r.root.find(key)
	if !ok || rec.Deleted {
		return nil, false
	}
	return rec.Value, true
}

// LookupRaw 返回键的原始记录（含墓碑），供测试与调试使用。
func (r *Replica) LookupRaw(key uint64) (Record, bool) {
	return r.root.find(key)
}

// RootHash 返回整棵树的根摘要。
func (r *Replica) RootHash() uint64 {
	return r.root.hash
}

// Len 返回副本中的记录总数（含墓碑）。
func (r *Replica) Len() int {
	return r.root.count
}
