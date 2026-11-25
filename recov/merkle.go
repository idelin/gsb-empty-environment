package recov

import "crypto/sha256"

// 本文件实现 RFC 6962 风格的二进制 Merkle 树：
//   - 叶子节点：0x00 || leaf_data
//   - 内部节点：0x01 || left || right
//
// leaf_data 由 EventHash / ReceiptHash 等带域分隔的哈希给出。

func merkleLeaf(leafData []byte) []byte {
	h := sha256.New()
	h.Write([]byte{leafPrefixMTH})
	h.Write(leafData)
	sum := h.Sum(nil)
	return sum[:]
}

func merkleNode(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(left)
	h.Write(right)
	sum := h.Sum(nil)
	return sum[:]
}

func emptyRoot() []byte {
	z := sha256.Sum256(nil)
	return z[:]
}

// largestPowerOfTwo 返回严格小于 n 的最大 2 的幂（n>=2）。
func largestPowerOfTwo(n int) int {
	k := 1
	for k*2 < n {
		k *= 2
	}
	return k
}

// MTH 严格按 RFC 6962 第 2.1 节递归定义计算树根。
func MTH(leafData [][]byte) []byte {
	n := len(leafData)
	if n == 0 {
		return emptyRoot()
	}
	if n == 1 {
		return merkleLeaf(leafData[0])
	}
	k := largestPowerOfTwo(n)
	return merkleNode(MTH(leafData[:k]), MTH(leafData[k:]))
}

// TreeRoot 是 MTH 的语义化别名。
func TreeRoot(leafData [][]byte) []byte { return MTH(leafData) }

// InclusionProof 生成 leafIndex 在规模 len(leafData) 内的包含证明（RFC PATH）。
func InclusionProof(leafData [][]byte, leafIndex uint64) MerkleProof {
	treeSize := uint64(len(leafData))
	hashes := [][]byte{}
	var path func(start, end, idx uint64)
	path = func(start, end, idx uint64) {
		n := end - start
		if n <= 1 {
			return
		}
		k := uint64(largestPowerOfTwo(int(n)))
		if idx < start+k {
			path(start, start+k, idx)
			hashes = append(hashes, MTH(leafData[start+k:end]))
		} else {
			path(start+k, end, idx)
			hashes = append(hashes, MTH(leafData[start:start+k]))
		}
	}
	path(0, treeSize, leafIndex)
	return MerkleProof{LeafIndex: leafIndex, TreeSize: treeSize, Hashes: hashes}
}

// VerifyInclusion 用包含证明把叶子数据重算到给定树根（RFC 2.1.2 迭代法）。
func VerifyInclusion(p MerkleProof, leafData, root []byte) bool {
	if p.TreeSize == 0 || p.LeafIndex >= p.TreeSize {
		return false
	}
	fn := p.LeafIndex
	sn := p.TreeSize - 1
	calc := merkleLeaf(leafData)
	for _, h := range p.Hashes {
		if fn%2 == 1 || fn == sn {
			calc = merkleNode(h, calc)
			for fn%2 == 0 && fn != 0 {
				fn /= 2
				sn /= 2
			}
		} else {
			calc = merkleNode(calc, h)
		}
		fn /= 2
		sn /= 2
	}
	// 证明过长会在中途把 fn 推到 0 而 sn 仍 >0；证明过短则最终 fn>sn 或不相等。
	return fn == sn && equalBytes(calc, root)
}

// ConsistencyProof 生成 oldSize 到 len(leafData) 的一致性证明（RFC PROVE）。
// oldSize 必须满足 0 < oldSize <= len(leafData)；相等时返回空证明。
func ConsistencyProof(leafData [][]byte, oldSize uint64) [][]byte {
	n := uint64(len(leafData))
	if oldSize == 0 || oldSize > n {
		return nil
	}
	if oldSize == n {
		return [][]byte{}
	}
	return subproofRange(leafData, 0, n, oldSize, oldSize&(oldSize-1) == 0)
}

// subproofRange 在全局区间 [start, start+size) 上实现 RFC 的 SUBPROOF。
// m 是相对该区间起点的旧树规模；b 控制命中边界时是否输出子树根。
func subproofRange(leafData [][]byte, start, size, m uint64, b bool) [][]byte {
	if m == size {
		if b {
			return [][]byte{MTH(leafData[start : start+size])}
		}
		return nil
	}
	k := uint64(largestPowerOfTwo(int(size)))
	// 旧树恰好等于左完整子树：直接给 [旧根, 右子树根]，不再下钻。
	if m == k {
		return [][]byte{
			MTH(leafData[start : start+k]),
			MTH(leafData[start+k : start+size]),
		}
	}
	if m <= k {
		out := subproofRange(leafData, start, k, m, true)
		return append(out, MTH(leafData[start+k:start+size]))
	}
	out := [][]byte{MTH(leafData[start : start+k])}
	// 右子树相对旧树完全是新增部分，命中边界时必须输出，故 b 恒为 true。
	return append(out, subproofRange(leafData, start+k, size-k, m-k, true)...)
}

// VerifyConsistency 在持有完整叶子的前提下验证一致性证明：
// 重算旧/新树根，并重新生成期望证明与给定证明逐项比对。
// 这比纯根的 fn/sn 迭代更易审计，且本包验证时本来就持有日志前缀。
func VerifyConsistency(leafData [][]byte, oldSize uint64, proof [][]byte) bool {
	n := uint64(len(leafData))
	if oldSize == 0 || oldSize > n {
		return false
	}
	if oldSize == n {
		return len(proof) == 0
	}
	expected := ConsistencyProof(leafData, oldSize)
	if len(expected) != len(proof) {
		return false
	}
	for i := range expected {
		if !equalBytes(expected[i], proof[i]) {
			return false
		}
	}
	return true
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	out := byte(0)
	for i := range a {
		out |= a[i] ^ b[i]
	}
	return out == 0
}
