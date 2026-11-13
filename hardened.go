package netsec

import (
	"crypto/subtle"
	"encoding/binary"
)

// DomainLabel 是加固格式的固定域标签：15 字节 ASCII + 一个零字节，
// 恰好构成一个分组。
var DomainLabel = [BlockSize]byte{
	'Q', '0', '1', '0', '9', '-', 'C', 'B', 'C', 'M', 'A', 'C', '-', 'V', '1', 0x00,
}

// HardenedOracle 实现加固格式：MAC 输入为
//
//	DomainLabel(1 块) || 消息块数 big-endian uint64(1 块) || 消息
//
// 域标签与长度前缀使不同长度、不同域的输入互不兼容，
// 旧格式（裸消息）的 tag 在新格式下不被接受。
type HardenedOracle struct {
	inner *Oracle
}

// NewHardenedOracle 以宿主密钥回调创建加固格式 oracle。
func NewHardenedOracle(k KeyHandle) *HardenedOracle {
	return &HardenedOracle{inner: NewOracle(k)}
}

// encode 组装加固格式的 MAC 输入。
func encode(message []byte) ([]byte, error) {
	if err := validate(message); err != nil {
		return nil, err
	}
	blocks := len(message) / BlockSize
	buf := make([]byte, 2*BlockSize+len(message))
	copy(buf, DomainLabel[:])
	binary.BigEndian.PutUint64(buf[BlockSize+8:], uint64(blocks))
	copy(buf[2*BlockSize:], message)
	return buf, nil
}

// Tag 计算加固格式标签。
func (h *HardenedOracle) Tag(message []byte) ([]byte, error) {
	input, err := encode(message)
	if err != nil {
		return nil, err
	}
	tag, _, err := h.inner.tagRaw(input)
	return tag, err
}

// Verify 以常量时间比较校验加固格式标签。
// 旧格式（裸消息）的 tag 因 MAC 输入不同而不会通过。
func (h *HardenedOracle) Verify(message, tag []byte) bool {
	want, err := h.Tag(message)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(want, tag) == 1
}
