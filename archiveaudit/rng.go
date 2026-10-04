package main

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
)

// byteStream 是由种子字符串派生的确定性字节流（SHA-256 计数器模式）。
// 不依赖 Go 标准库 PRNG，因此在不同 Go 版本、不同平台上结果一致，
// 调用方提供相同种子即可复现完全相同的抽样过程。
type byteStream struct {
	seed    [32]byte
	counter uint64
	buf     []byte
	off     int
}

func newStream(seed string) *byteStream {
	return &byteStream{seed: sha256.Sum256([]byte(seed))}
}

// Read 用确定性字节填满 p。
func (s *byteStream) Read(p []byte) {
	for i := range p {
		if s.off >= len(s.buf) {
			var block [40]byte // 32 字节种子 + 8 字节计数器
			copy(block[:32], s.seed[:])
			binary.BigEndian.PutUint64(block[32:], s.counter)
			sum := sha256.Sum256(block[:])
			s.buf = sum[:]
			s.off = 0
			s.counter++
		}
		p[i] = s.buf[s.off]
		s.off++
	}
}

// byteSource 是确定性字节来源，便于测试时替换。
type byteSource interface {
	Read(p []byte)
}

// uniformBig 返回 [0, n) 上的均匀随机大整数。
// 采用拒绝采样：丢弃 >= n 的候选值重抽，不存在取模偏差，
// 因此即使总逻辑长度超过 64 位、任意巨大，分布依然严格均匀。
func uniformBig(s byteSource, n *big.Int) *big.Int {
	if n == nil || n.Sign() <= 0 {
		panic("uniformBig: n 必须为正整数")
	}
	bits := uint(n.BitLen())
	nbytes := int((bits + 7) / 8)
	excess := uint(nbytes*8) - bits
	buf := make([]byte, nbytes)
	for {
		s.Read(buf)
		if excess > 0 {
			buf[0] &= byte(0xFF >> excess) // 屏蔽高位，保持候选域为 [0, 2^bits)
		}
		r := new(big.Int).SetBytes(buf)
		if r.Cmp(n) < 0 {
			return r
		}
		// 候选值落在 [n, 2^bits) 内，拒绝并重抽
	}
}
