package checksumdiff

import (
	"encoding/binary"
	"hash/fnv"
)

// NumBuckets 是分桶（Merkle 叶子）数量，按主键取模划分。
const NumBuckets = 1024

// BucketSig 是单个分桶的签名：行哈希的滚动 XOR + 行数。
// XOR 支持 O(1) 的增量增删改；Count 用于捕捉"一增一删哈希恰好抵消"的场景。
type BucketSig struct {
	XOR   uint64
	Count int64
}

// Index 是滚动校验和索引（类 Merkle 树的叶子层 + 根）。
// 每次行变更只需 XOR 掉旧哈希、XOR 入新哈希，无需全量重算。
type Index struct {
	xor   [NumBuckets]uint64
	count [NumBuckets]int64
}

func bucketOf(id int64) int {
	b := id % NumBuckets
	if b < 0 {
		b += NumBuckets
	}
	return int(b)
}

// Add 将一行哈希滚入对应分桶。
func (ix *Index) Add(id int64, h uint64) {
	b := bucketOf(id)
	ix.xor[b] ^= h
	ix.count[b]++
}

// Remove 将一行哈希从对应分桶滚出。
func (ix *Index) Remove(id int64, h uint64) {
	b := bucketOf(id)
	ix.xor[b] ^= h
	ix.count[b]--
}

// Update 等价于 Remove(old) + Add(new)，用于行更新。
func (ix *Index) Update(id int64, oldHash, newHash uint64) {
	b := bucketOf(id)
	ix.xor[b] ^= oldHash ^ newHash
}

// BucketSig 返回指定分桶的当前签名。
func (ix *Index) BucketSig(b int) BucketSig {
	return BucketSig{XOR: ix.xor[b], Count: ix.count[b]}
}

// Root 返回整库根签名（对所有分桶签名再做一次哈希，类似 Merkle 根）。
func (ix *Index) Root() uint64 {
	h := fnv.New64a()
	var buf [16]byte
	for b := 0; b < NumBuckets; b++ {
		binary.LittleEndian.PutUint64(buf[:8], ix.xor[b])
		binary.LittleEndian.PutUint64(buf[8:], uint64(ix.count[b]))
		h.Write(buf[:])
	}
	return h.Sum64()
}
