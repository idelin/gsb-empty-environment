package netsec

import (
	"encoding/binary"
	"errors"
	"math"
)

const (
	// MaxDescriptors 是描述符环的最大项数。
	MaxDescriptors = 64
	// DescriptorSize 是单个描述符的字节数。
	DescriptorSize = 16
	// MaxDescLen 是单个描述符允许的最大长度（1 MiB）。
	MaxDescLen = 1 << 20
	// MaxChainBytes 是单次提交链的总字节预算。
	MaxChainBytes = 8 << 20
)

// 描述符 flags 仅允许以下三位。
const (
	FlagRead  uint16 = 1 << iota // 设备从客户机内存读
	FlagWrite                    // 设备向客户机内存写
	FlagEnd                      // 链结束，不得继续沿 next 遍历
)

const flagMask = FlagRead | FlagWrite | FlagEnd

var (
	ErrBadFlags        = errors.New("netsec: descriptor carries unknown flag bits")
	ErrBadLength       = errors.New("netsec: descriptor length out of range [1, 1MiB]")
	ErrAddrOverflow    = errors.New("netsec: descriptor address+length overflows")
	ErrIndexOutOfRange = errors.New("netsec: descriptor index out of range")
	ErrDuplicate       = errors.New("netsec: descriptor visited twice in one chain")
	ErrMissingEnd      = errors.New("netsec: chain not terminated by an END descriptor")
	ErrBudgetExceeded  = errors.New("netsec: chain total byte budget exceeded")
)

// Descriptor 是 16 字节的 DMA 描述符。
type Descriptor struct {
	Addr  uint64 // 客户机物理地址
	Len   uint32 // 长度，范围 [1, 1MiB]
	Next  uint16 // 下一项索引（END 项忽略）
	Flags uint16 // 仅 READ/WRITE/END 三位
}

// end 返回区间末地址（含），并对长度与溢出做基础校验。
func (d Descriptor) end() (uint64, error) {
	if d.Len == 0 || d.Len > MaxDescLen {
		return 0, ErrBadLength
	}
	if d.Addr > math.MaxUint64-(uint64(d.Len)-1) {
		return 0, ErrAddrOverflow
	}
	return d.Addr + uint64(d.Len) - 1, nil
}

// validate 校验 flags、长度与地址溢出。
func (d Descriptor) validate() error {
	if d.Flags&^flagMask != 0 {
		return ErrBadFlags
	}
	_, err := d.end()
	return err
}

// needPerm 把描述符 flags 映射为所需页权限。
func (d Descriptor) needPerm() Perm {
	var p Perm
	if d.Flags&FlagRead != 0 {
		p |= PermRead
	}
	if d.Flags&FlagWrite != 0 {
		p |= PermWrite
	}
	return p
}

// DecodeDescriptor 从 16 字节小端编码解析描述符。
func DecodeDescriptor(b []byte) Descriptor {
	return Descriptor{
		Addr:  binary.LittleEndian.Uint64(b[0:8]),
		Len:   binary.LittleEndian.Uint32(b[8:12]),
		Next:  binary.LittleEndian.Uint16(b[12:14]),
		Flags: binary.LittleEndian.Uint16(b[14:16]),
	}
}

// EncodeDescriptor 把描述符编码为 16 字节小端形式。
func EncodeDescriptor(d Descriptor) [DescriptorSize]byte {
	var b [DescriptorSize]byte
	binary.LittleEndian.PutUint64(b[0:8], d.Addr)
	binary.LittleEndian.PutUint32(b[8:12], d.Len)
	binary.LittleEndian.PutUint16(b[12:14], d.Next)
	binary.LittleEndian.PutUint16(b[14:16], d.Flags)
	return b
}
