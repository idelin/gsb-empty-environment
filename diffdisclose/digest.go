package diffdisclose

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// DigestSize 是所有节点摘要的字节长度（SHA-256 输出）。
const DigestSize = sha256.Size

// SaltSize 是每个非空叶子绑定盐的字节长度。
const SaltSize = 32

// MaxHeight 是稀疏树允许的最大高度；48 位键空间取 MaxHeight。
const MaxHeight = 48

// 域分离前缀，确保空摘要、叶摘要、内部摘要与清单摘要不可互相挪用。
var (
	tagEmpty    = []byte("ddd/empty/v1:")
	tagLeaf     = []byte("ddd/leaf/v1:")
	tagInternal = []byte("ddd/internal/v1:")
	tagManifest = []byte("ddd/manifest/v1:")
)

// Digest 是 32 字节节点摘要。
type Digest [DigestSize]byte

// 预先计算的空子树摘要，索引为子树高度（0..MaxHeight）。
// 空摘要显式绑定高度，验证者据此拒绝在不同高度之间挪用空摘要。
var emptyDigest [MaxHeight + 1]Digest

func init() {
	for h := 0; h <= MaxHeight; h++ {
		hbuf := make([]byte, 0, len(tagEmpty)+1)
		hbuf = append(hbuf, tagEmpty...)
		hbuf = append(hbuf, byte(h))
		emptyDigest[h] = sha256.Sum256(hbuf)
	}
}

// EmptyDigest 返回高度为 height 的空子树预定义摘要。
func EmptyDigest(height int) (Digest, error) {
	if height < 0 || height > MaxHeight {
		return Digest{}, errors.New("diffdisclose: subtree height out of range")
	}
	return emptyDigest[height], nil
}

// mustEmptyDigest 用于内部已校验高度的调用点。
func mustEmptyDigest(height int) Digest {
	return emptyDigest[height]
}

// leafDigest 计算非空叶子摘要，绑定键、值长度、值与 32 字节盐。
// 域分离标签后紧跟：8 字节大端键、8 字节大端值长度、值、32 字节盐。
func leafDigest(key uint64, value, salt []byte) Digest {
	if len(salt) != SaltSize {
		panic("diffdisclose: leaf salt must be exactly 32 bytes")
	}
	h := sha256.New()
	h.Write(tagLeaf)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], key)
	h.Write(num[:])
	binary.BigEndian.PutUint64(num[:], uint64(len(value)))
	h.Write(num[:])
	h.Write(value)
	h.Write(salt)
	var out Digest
	h.Sum(out[:0])
	return out
}

// internalDigest 计算内部节点摘要，绑定高度、节点地址前缀与左右子摘要。
// 地址以高位在前的位串给出，位数必须等于 MaxHeight-height。
func internalDigest(height int, addr addr, left, right Digest) Digest {
	if height <= 0 || height > MaxHeight {
		panic("diffdisclose: internal node height out of range")
	}
	// 全空子树规约到按高度预定义的空摘要，保证建树短路与验证者递归
	// 重算得到同一个值；非空内部节点仍绑定高度、前缀与左右摘要。
	if left == emptyDigest[height-1] && right == emptyDigest[height-1] {
		return emptyDigest[height]
	}
	h := sha256.New()
	h.Write(tagInternal)
	h.Write([]byte{byte(height)})
	prefixBytes, prefixLen := addr.bytes()
	h.Write([]byte{byte(prefixLen)})
	h.Write(prefixBytes)
	h.Write(left[:])
	h.Write(right[:])
	var out Digest
	h.Sum(out[:0])
	return out
}

// manifestDigest 计算签名清单的规范摘要，绑定租户、版本、父清单与根。
func manifestDigest(tenant string, version uint64, parent, root Digest) Digest {
	h := sha256.New()
	h.Write(tagManifest)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], uint64(len(tenant)))
	h.Write(num[:])
	h.Write([]byte(tenant))
	binary.BigEndian.PutUint64(num[:], version)
	h.Write(num[:])
	h.Write(parent[:])
	h.Write(root[:])
	var out Digest
	h.Sum(out[:0])
	return out
}
