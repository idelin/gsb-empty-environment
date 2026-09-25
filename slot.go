package rootslot

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
)

// 槽的磁盘布局（固定 SlotSize 字节，恰好一个常见扇区大小）：
//
//	[0,8)    magic "RTSLT001"
//	[8,16)   seq      uint64 大端，单调递增，从 1 开始，0 表示“从未写入”
//	[16,20)  payloadLen uint32 大端
//	[20,32)  reserved，必须全零
//	[32,480) payload（根指针/根描述符， opaque 字节）
//	[480,512) sha256 校验和，覆盖前 480 字节
const (
	SlotSize       = 512
	MaxPayload     = 448
	checksumOffset = 480
	numSlots       = 2
)

var slotMagic = []byte("RTSLT001")

// slotState 描述一个槽在盘上读到后的判定结果。
type slotState int

const (
	slotEmpty   slotState = iota // 全零：从未写入（空仓库的合法状态）
	slotValid                    // 校验和通过
	slotCorrupt                  // 非全零但校验和/格式不通过：禁止猜测其内容
)

type slotContent struct {
	seq     uint64
	payload []byte
}

func encodeSlot(seq uint64, payload []byte) []byte {
	buf := make([]byte, SlotSize)
	copy(buf[0:8], slotMagic)
	binary.BigEndian.PutUint64(buf[8:16], seq)
	binary.BigEndian.PutUint32(buf[16:20], uint32(len(payload)))
	copy(buf[32:checksumOffset], payload)
	sum := sha256.Sum256(buf[:checksumOffset])
	copy(buf[checksumOffset:], sum[:])
	return buf
}

// decodeSlot 严格校验；任何不完整/不一致都判为 corrupt，绝不猜测。
func decodeSlot(buf []byte) (slotContent, slotState) {
	var c slotContent
	if len(buf) < SlotSize {
		return c, slotCorrupt
	}
	if isAllZero(buf[:SlotSize]) {
		return c, slotEmpty
	}
	if !bytes.Equal(buf[0:8], slotMagic) {
		return c, slotCorrupt
	}
	sum := sha256.Sum256(buf[:checksumOffset])
	if !bytes.Equal(buf[checksumOffset:SlotSize], sum[:]) {
		return c, slotCorrupt
	}
	payloadLen := binary.BigEndian.Uint32(buf[16:20])
	if payloadLen > MaxPayload {
		return c, slotCorrupt
	}
	if !isAllZero(buf[20:32]) {
		return c, slotCorrupt
	}
	if !isAllZero(buf[32+payloadLen : checksumOffset]) {
		return c, slotCorrupt
	}
	c.seq = binary.BigEndian.Uint64(buf[8:16])
	c.payload = append([]byte(nil), buf[32:32+payloadLen]...)
	return c, slotValid
}

func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
