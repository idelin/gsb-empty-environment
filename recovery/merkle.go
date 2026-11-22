package recovery

import (
	"bytes"
	"errors"
)

// 日志树与预密钥承诺树均采用 RFC 6962 的 MTH 构造，仅叶子域标签不同。

func logLeafHash(e Event) Hash {
	return domainHash("mth-leaf/directory/v1", eventHash(e))
}

func treeNodeHash(tag string, left, right Hash) Hash {
	return domainHash(tag, left, right)
}

// treeRoot 按 RFC 6962 从叶子列表计算 MTH。
func treeRoot(tag string, leaves []Hash) Hash {
	switch len(leaves) {
	case 0:
		return domainHash(tag + "/empty")
	case 1:
		return leaves[0]
	}
	k := largestPowerOfTwo(len(leaves))
	left := treeRoot(tag, leaves[:k])
	right := treeRoot(tag, leaves[k:])
	return treeNodeHash(tag, left, right)
}

func largestPowerOfTwo(n int) int {
	if n < 2 {
		panic("largestPowerOfTwo requires n >= 2")
	}
	k := 1
	for k<<1 < n {
		k <<= 1
	}
	return k
}

// inclusionPath 返回 RFC 6962 风格包含证明所需的兄弟节点列表。
func inclusionPath(leaves []Hash, index int) []Hash {
	return logPath(leaves, index, 0, len(leaves))
}

func logPath(leaves []Hash, index, lo, hi int) []Hash {
	if hi-lo == 1 {
		return nil
	}
	mid := lo + largestPowerOfTwo(hi-lo)
	if index < mid {
		return append([]Hash{subtreeHash("mth-leaf/directory/v1", leaves, mid, hi)},
			logPath(leaves, index, lo, mid)...)
	}
	return append([]Hash{subtreeHash("mth-leaf/directory/v1", leaves, lo, mid)},
		logPath(leaves, index, mid, hi)...)
}

func subtreeHash(tag string, leaves []Hash, lo, hi int) Hash {
	if hi-lo == 1 {
		return leaves[lo]
	}
	mid := lo + largestPowerOfTwo(hi-lo)
	return treeNodeHash(tag, subtreeHash(tag, leaves, lo, mid), subtreeHash(tag, leaves, mid, hi))
}

// verifyInclusion 校验包含证明能否把叶子重建成指定树根。
func verifyInclusion(leaf Hash, p InclusionProof, root Hash) error {
	if p.Index >= p.Size {
		return errors.New("inclusion index out of range")
	}
	got, hashes, err := verifyLogRange(leaf, int(p.Index), 0, int(p.Size), p.Hashes)
	if err != nil {
		return err
	}
	if len(hashes) != 0 {
		return errors.New("inclusion proof too long")
	}
	if !equalHash(got, root) {
		return errors.New("inclusion root mismatch")
	}
	return nil
}

func verifyLogRange(leaf Hash, index, lo, hi int, hashes []Hash) (Hash, []Hash, error) {
	if hi-lo == 1 {
		if lo != index {
			return nil, nil, errors.New("inclusion index mismatch")
		}
		return leaf, hashes, nil
	}
	if len(hashes) == 0 {
		return nil, nil, errors.New("inclusion proof too short")
	}
	sibling, rest := hashes[0], hashes[1:]
	mid := lo + largestPowerOfTwo(hi-lo)
	if index < mid {
		sub, rest, err := verifyLogRange(leaf, index, lo, mid, rest)
		if err != nil {
			return nil, nil, err
		}
		return treeNodeHash("mth-leaf/directory/v1", sub, sibling), rest, nil
	}
	sub, rest, err := verifyLogRange(leaf, index, mid, hi, rest)
	if err != nil {
		return nil, nil, err
	}
	return treeNodeHash("mth-leaf/directory/v1", sibling, sub), rest, nil
}

// prekeyInclusionPath 生成预密钥承诺树的包含路径。
func prekeyInclusionPath(leaves []Hash, index int) []Hash {
	return prekeyPath(leaves, index, 0, len(leaves))
}

func prekeyPath(leaves []Hash, index, lo, hi int) []Hash {
	if hi-lo == 1 {
		return nil
	}
	mid := lo + largestPowerOfTwo(hi-lo)
	if index < mid {
		return append([]Hash{prekeySubtree(leaves, mid, hi)},
			prekeyPath(leaves, index, lo, mid)...)
	}
	return append([]Hash{prekeySubtree(leaves, lo, mid)},
		prekeyPath(leaves, index, mid, hi)...)
}

func prekeySubtree(leaves []Hash, lo, hi int) Hash {
	if hi-lo == 1 {
		return leaves[lo]
	}
	mid := lo + largestPowerOfTwo(hi-lo)
	return treeNodeHash("mth-leaf/prekey/v1", prekeySubtree(leaves, lo, mid), prekeySubtree(leaves, mid, hi))
}

// verifyPrekeyInclusion 校验一次性承诺包含证明。
func verifyPrekeyInclusion(pp PrekeyProof, root Hash) error {
	if pp.Index >= pp.Size {
		return errors.New("prekey index out of range")
	}
	got, hashes, err := verifyPrekeyRange(pp.Leaf, int(pp.Index), 0, int(pp.Size), pp.Hashes)
	if err != nil {
		return err
	}
	if len(hashes) != 0 {
		return errors.New("prekey proof too long")
	}
	if !equalHash(got, root) {
		return errors.New("prekey root mismatch")
	}
	return nil
}

func verifyPrekeyRange(leaf Hash, index, lo, hi int, hashes []Hash) (Hash, []Hash, error) {
	if hi-lo == 1 {
		if lo != index {
			return nil, nil, errors.New("prekey index mismatch")
		}
		return leaf, hashes, nil
	}
	if len(hashes) == 0 {
		return nil, nil, errors.New("prekey proof too short")
	}
	sibling, rest := hashes[0], hashes[1:]
	mid := lo + largestPowerOfTwo(hi-lo)
	if index < mid {
		sub, rest, err := verifyPrekeyRange(leaf, index, lo, mid, rest)
		if err != nil {
			return nil, nil, err
		}
		return treeNodeHash("mth-leaf/prekey/v1", sub, sibling), rest, nil
	}
	sub, rest, err := verifyPrekeyRange(leaf, index, mid, hi, rest)
	if err != nil {
		return nil, nil, err
	}
	return treeNodeHash("mth-leaf/prekey/v1", sibling, sub), rest, nil
}

// 稀疏默克尔树（固定 256 层）用于见证记录“会话摘要 -> 回执集合”。

var smtEmptyHash = domainHash("smt/empty/v1")

func smtNodeHash(left, right Hash) Hash {
	return domainHash("smt/node/v1", left, right)
}

func smtLeafHash(key, value Hash) Hash {
	return domainHash("smt/leaf/v1", key, value)
}

var smtZeroCache [257]Hash

func init() {
	smtZeroCache[0] = smtEmptyHash
	for i := 1; i <= 256; i++ {
		smtZeroCache[i] = smtNodeHash(smtZeroCache[i-1], smtZeroCache[i-1])
	}
}

// smtRoot 从“键 -> 非空值”映射计算稀疏树根。
func smtRoot(entries map[string]Hash) Hash {
	return smtBuild(entries, 0)
}

func smtBuild(entries map[string]Hash, depth int) Hash {
	if depth == 256 {
		if len(entries) == 0 {
			return smtEmptyHash
		}
		for k, v := range entries {
			return smtLeafHash(Hash(k), v)
		}
	}
	var left, right map[string]Hash
	for k, v := range entries {
		if bitAt([]byte(k), depth) == 0 {
			if left == nil {
				left = make(map[string]Hash)
			}
			left[k] = v
		} else {
			if right == nil {
				right = make(map[string]Hash)
			}
			right[k] = v
		}
	}
	lh := smtZeroCache[256-depth-1]
	rh := lh
	if left != nil {
		lh = smtBuild(left, depth+1)
	}
	if right != nil {
		rh = smtBuild(right, depth+1)
	}
	return smtNodeHash(lh, rh)
}

func bitAt(key []byte, depth int) byte {
	return (key[depth/8] >> (7 - uint(depth%8))) & 1
}

// smtProof 为某个键生成包含/非包含证明。
func smtProof(entries map[string]Hash, key Hash) SparseProof {
	keyString := string(key)
	value, exists := entries[keyString]
	p := SparseProof{Key: copyHash(key), Exists: exists, Value: copyHash(value)}
	var walk func(map[string]Hash, int)
	walk = func(nodes map[string]Hash, depth int) {
		if depth == 256 {
			return
		}
		var sibling map[string]Hash
		bit := bitAt(key, depth)
		var same map[string]Hash
		for k, v := range nodes {
			if bitAt([]byte(k), depth) == bit {
				if same == nil {
					same = make(map[string]Hash)
				}
				same[k] = v
			} else {
				if sibling == nil {
					sibling = make(map[string]Hash)
				}
				sibling[k] = v
			}
		}
		if sibling == nil {
			p.Siblings[depth] = copyHash(smtZeroCache[256-depth-1])
		} else {
			p.Siblings[depth] = smtBuild(sibling, depth+1)
		}
		if same == nil {
			for d := depth + 1; d < 256; d++ {
				p.Siblings[d] = copyHash(smtZeroCache[256-d-1])
			}
			return
		}
		walk(same, depth+1)
	}
	walk(entries, 0)
	return p
}

// verifySparseProof 校验稀疏证明并重算树根。
func verifySparseProof(p SparseProof) (Hash, error) {
	if len(p.Key) != 32 {
		return nil, errors.New("sparse key must be 32 bytes")
	}
	var cur Hash
	if p.Exists {
		cur = smtLeafHash(p.Key, p.Value)
	} else {
		if !bytes.Equal(p.Value, smtEmptyHash) {
			return nil, errors.New("non-inclusion proof must carry empty marker")
		}
		cur = smtEmptyHash
	}
	for depth := 255; depth >= 0; depth-- {
		sib := p.Siblings[depth]
		if sib == nil {
			return nil, errors.New("missing sparse sibling")
		}
		if bitAt(p.Key, depth) == 0 {
			cur = smtNodeHash(cur, sib)
		} else {
			cur = smtNodeHash(sib, cur)
		}
	}
	return cur, nil
}

// receiptSetHash 把同一 session 下的一组回执规范化为稀疏树叶子值。
func receiptSetHash(receipts []Receipt) Hash {
	if len(receipts) == 0 {
		return smtEmptyHash
	}
	parts := make([][]byte, 0, len(receipts))
	for _, r := range receipts {
		parts = append(parts, receiptHash(r))
	}
	return domainHash("receipt-set/v1", parts...)
}

// receiptHash 是单条回执在回执集合中的规范化摘要。
func receiptHash(r Receipt) Hash {
	return domainHash("receipt/v1",
		r.SessionDigest,
		[]byte(r.Device),
		r.IdentityPK,
		r.Commitment,
		u64Bytes(r.SendTreeSize),
	)
}
