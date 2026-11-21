package diffdisclose

import "sort"

// Leaf 是非空叶子的完整披露内容：键、值与每叶独立的 32 字节盐。
type Leaf struct {
	Key   uint64
	Value []byte
	Salt  []byte
}

// Tree 是固定高度的稀疏二叉树，只保存非空叶子；空结构由预定义摘要表示。
// 内部摘要按访问惰性计算并记忆化，构造证明无需枚举 2^height 个键。
type Tree struct {
	height int
	leaves map[uint64]leafData
	sorted []uint64
	memo   map[memoKey]Digest
}

type leafData struct {
	value  []byte
	salt   []byte
	digest Digest
}

type memoKey struct {
	height    int
	prefixLen int
	prefix    uint64
}

// NewTree 用给定叶子构建高度为 height 的稀疏树。
// 键必须落在 2^height 空间内，盐必须为 32 字节，键不得重复。
func NewTree(height int, leaves []Leaf) (*Tree, error) {
	if height <= 0 || height > MaxHeight {
		return nil, errBadHeight
	}
	limit := uint64(1) << height
	t := &Tree{
		height: height,
		leaves: make(map[uint64]leafData, len(leaves)),
		memo:   make(map[memoKey]Digest),
	}
	for _, leaf := range leaves {
		if leaf.Key >= limit {
			return nil, errKeyOutOfRange
		}
		if len(leaf.Salt) != SaltSize {
			return nil, errBadSalt
		}
		if _, exists := t.leaves[leaf.Key]; exists {
			return nil, errDuplicateKey
		}
		value := append([]byte(nil), leaf.Value...)
		salt := append([]byte(nil), leaf.Salt...)
		t.leaves[leaf.Key] = leafData{
			value:  value,
			salt:   salt,
			digest: leafDigest(leaf.Key, value, salt),
		}
	}
	t.sorted = make([]uint64, 0, len(t.leaves))
	for key := range t.leaves {
		t.sorted = append(t.sorted, key)
	}
	sort.Slice(t.sorted, func(i, j int) bool { return t.sorted[i] < t.sorted[j] })
	return t, nil
}

// Height 返回树高。
func (t *Tree) Height() int { return t.height }

// Root 返回树根摘要。
func (t *Tree) Root() Digest {
	return t.digest(t.height, rootAddr())
}

// leaf 返回指定键的叶子数据与存在性。
func (t *Tree) leaf(key uint64) (leafData, bool) {
	d, ok := t.leaves[key]
	return d, ok
}

// digest 返回地址 addr、高度 height 子树的摘要，空树直接取预定义值。
func (t *Tree) digest(height int, address addr) Digest {
	if height == 0 {
		// 高度 0 即单个键位置；键由完整前缀唯一确定。
		key, _ := address.keyPrefix(0)
		if data, ok := t.leaves[key]; ok {
			return data.digest
		}
		return mustEmptyDigest(0)
	}
	start, span := address.keyPrefix(height)
	if !t.subtreeHasLeaf(start, span) {
		return mustEmptyDigest(height)
	}
	key := memoKey{height: height, prefixLen: address.len()}
	// 前缀位数不超过 48，可压缩进 uint64 作为记忆化键。
	for i, bit := range address.bits {
		if bit == 1 {
			key.prefix |= uint64(1) << uint(i)
		}
	}
	if cached, ok := t.memo[key]; ok {
		return cached
	}
	left := t.digest(height-1, address.left())
	right := t.digest(height-1, address.right())
	d := internalDigest(height, address, left, right)
	t.memo[key] = d
	return d
}

// subtreeHasLeaf 在线性叶子集合上判断 [start, start+span) 内是否存在键。
// 证明构造只沿授权与缓存相关的路径下降，调用次数与展开节点数同阶。
func (t *Tree) subtreeHasLeaf(start, span uint64) bool {
	idx := sort.Search(len(t.sorted), func(i int) bool { return t.sorted[i] >= start })
	return idx < len(t.sorted) && t.sorted[idx] < start+span
}

// countLeaves 返回非空叶子数量，主要用于测试统计。
func (t *Tree) countLeaves() int { return len(t.leaves) }
