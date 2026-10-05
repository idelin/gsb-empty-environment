package pubstore

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Slot 二进制布局（小端）：
//
//	偏移  0: magic      8 字节  "CFGSLOT\x01"
//	偏移  8: generation 8 字节  uint64 代际
//	偏移 16: length     4 字节  uint32 载荷长度
//	偏移 20: commit     1 字节  提交标记（0x00 未提交 / 0xA5 已提交）
//	偏移 21: reserved   3 字节  必须为 0
//	偏移 24: digest    32 字节  SHA-256(magic || generation || length || payload)
//	偏移 56: reserved   8 字节  必须为 0
//	偏移 64: payload    length 字节
//
// 摘要覆盖代际、长度与载荷，因此头部或载荷被撕裂都会被校验拒绝；
// 提交标记不参与摘要，因为它必须在载荷与摘要落盘之后最后写入。
const (
	Magic = "CFGSLOT\x01"

	headerSize    = 64
	magicOff      = 0
	generationOff = 8
	lengthOff     = 16
	commitOff     = 20
	digestOff     = 24
	payloadOff    = headerSize

	commitUncommitted = 0x00
	commitCommitted   = 0xA5

	// MaxPayload 是单个配置载荷的最大字节数。
	MaxPayload = 1 << 20
)

// ErrInvalidSlot 表示槽位未通过独立验证，所有验证失败都包装它。
var ErrInvalidSlot = errors.New("invalid slot")

// Slot 是一个通过验证的、完整提交的槽位镜像。
type Slot struct {
	Generation uint64
	Payload    []byte // 引用传入的 data，调用方不得修改
	Digest     [32]byte
}

// ValidateSlot 是独立槽验证器：不依赖 Store，仅根据字节内容判定槽位
// 是否为一次完整提交。任何撕裂写入（截断、摘要不符、标记缺失）都会被拒绝。
func ValidateSlot(data []byte) (*Slot, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("%w: size %d < header %d", ErrInvalidSlot, len(data), headerSize)
	}
	if string(data[magicOff:magicOff+8]) != Magic {
		return nil, fmt.Errorf("%w: bad magic %q", ErrInvalidSlot, data[magicOff:magicOff+8])
	}
	generation := binary.LittleEndian.Uint64(data[generationOff:])
	length := binary.LittleEndian.Uint32(data[lengthOff:])
	if length > MaxPayload {
		return nil, fmt.Errorf("%w: length %d exceeds max %d", ErrInvalidSlot, length, MaxPayload)
	}
	if uint64(len(data)) != uint64(headerSize)+uint64(length) {
		return nil, fmt.Errorf("%w: size %d != header %d + length %d",
			ErrInvalidSlot, len(data), headerSize, length)
	}
	if data[commitOff] != commitCommitted {
		return nil, fmt.Errorf("%w: commit marker 0x%02x != 0x%02x",
			ErrInvalidSlot, data[commitOff], commitCommitted)
	}
	payload := data[payloadOff:]
	var want [32]byte
	copy(want[:], data[digestOff:digestOff+32])
	if got := slotDigest(generation, length, payload); got != want {
		return nil, fmt.Errorf("%w: digest mismatch", ErrInvalidSlot)
	}
	return &Slot{Generation: generation, Payload: payload, Digest: want}, nil
}

// slotDigest 计算覆盖代际、长度与载荷的摘要。
func slotDigest(generation uint64, length uint32, payload []byte) [32]byte {
	h := sha256.New()
	h.Write([]byte(Magic))
	var buf [12]byte
	binary.LittleEndian.PutUint64(buf[0:8], generation)
	binary.LittleEndian.PutUint32(buf[8:12], length)
	h.Write(buf[:])
	h.Write(payload)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// encodeSlot 构造完整槽位镜像；committed 为 false 时提交标记为未提交，
// 用于“先写内容、最后写标记”的两阶段写入。
func encodeSlot(generation uint64, payload []byte, committed bool) []byte {
	buf := make([]byte, headerSize+len(payload))
	copy(buf[magicOff:], Magic)
	binary.LittleEndian.PutUint64(buf[generationOff:], generation)
	binary.LittleEndian.PutUint32(buf[lengthOff:], uint32(len(payload)))
	if committed {
		buf[commitOff] = commitCommitted
	}
	digest := slotDigest(generation, uint32(len(payload)), payload)
	copy(buf[digestOff:], digest[:])
	copy(buf[payloadOff:], payload)
	return buf
}
