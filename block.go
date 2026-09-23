package blockdict

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// 块格式（全部大端，定长 54 字节头 + 载荷）：
//
//	0..4   magic = "BLK1"
//	4      格式版本 = 1
//	5      flags: bit0 = 载荷为未压缩存储
//	6..38  字典 SHA-256 摘要（空字典为 sha256("")）
//	38..42 解压后长度
//	42..46 载荷长度
//	46..50 载荷 CRC32
//	50..54 头 CRC32（保护以上全部字段，使长度字段可信）
//	54..   载荷
//
// 头 CRC 保证：载荷损坏时长度字段仍可信，可据此跳过坏块定位下一块；
// 头本身损坏时用 FindNext 扫描“magic + 头 CRC 合法”重新同步。

const (
	Magic      uint32 = 0x424C4B31 // "BLK1"
	Version    byte   = 1
	HeaderSize        = 54
	MaxPayload        = 1 << 22 // 单块载荷上限 4 MiB
	flagStored byte   = 1 << 0
)

var (
	ErrBadMagic      = errors.New("blockdict: bad magic")
	ErrBadVersion    = errors.New("blockdict: unsupported version")
	ErrHeaderCRC     = errors.New("blockdict: header checksum mismatch")
	ErrPayloadCRC    = errors.New("blockdict: payload checksum mismatch")
	ErrDictMismatch  = errors.New("blockdict: dictionary digest mismatch")
	ErrBlockTooLarge = errors.New("blockdict: block payload exceeds limit")
	ErrOutputLimit   = errors.New("blockdict: decoded output exceeds limit")
	ErrTruncated     = errors.New("blockdict: truncated block")
	ErrCorrupt       = errors.New("blockdict: corrupt payload")
)

// AppendBlock 把 src 编码为一个独立块追加到 dst，返回追加后的切片。
// 字典为 nil 时按空字典处理。压缩后反而变大时退化为原样存储。
func AppendBlock(dst []byte, d *Dict, src []byte) []byte {
	comp := compress(d.bytes(), src)
	payload, flags := comp, byte(0)
	if len(comp) >= len(src) {
		payload, flags = src, flagStored
	}

	sum := d.sum()
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint32(hdr[0:4], Magic)
	hdr[4] = Version
	hdr[5] = flags
	copy(hdr[6:38], sum[:])
	binary.BigEndian.PutUint32(hdr[38:42], uint32(len(src)))
	binary.BigEndian.PutUint32(hdr[42:46], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[46:50], crc32.ChecksumIEEE(payload))
	binary.BigEndian.PutUint32(hdr[50:54], crc32.ChecksumIEEE(hdr[0:50]))

	dst = append(dst, hdr[:]...)
	return append(dst, payload...)
}

// DecodeBlockAt 解码 buf 中偏移 off 处的块，返回解码结果与下一块偏移。
// maxOut 是解码输出硬上限：头中声明的解压长度或实际解码输出超过它都会被拒绝。
// 载荷损坏时返回错误，但 next 仍指向下一块起点，调用方可继续解码后续独立块。
func DecodeBlockAt(buf []byte, off int, d *Dict, maxOut int) (out []byte, next int, err error) {
	if off < 0 || off+HeaderSize > len(buf) {
		return nil, off, ErrTruncated
	}
	hdr := buf[off : off+HeaderSize]
	if binary.BigEndian.Uint32(hdr[0:4]) != Magic {
		return nil, off, ErrBadMagic
	}
	if hdr[4] != Version {
		return nil, off, ErrBadVersion
	}
	if crc32.ChecksumIEEE(hdr[0:50]) != binary.BigEndian.Uint32(hdr[50:54]) {
		return nil, off, ErrHeaderCRC
	}
	if sum := d.sum(); string(hdr[6:38]) != string(sum[:]) {
		return nil, off, ErrDictMismatch
	}
	flags := hdr[5]
	uLen := binary.BigEndian.Uint32(hdr[38:42])
	cLen := binary.BigEndian.Uint32(hdr[42:46])
	if cLen > MaxPayload {
		return nil, off, ErrBlockTooLarge
	}
	if uint64(uLen) > uint64(maxOut) {
		return nil, off, ErrOutputLimit
	}
	end := off + HeaderSize + int(cLen)
	if end > len(buf) {
		return nil, off, ErrTruncated
	}
	payload := buf[off+HeaderSize : end]
	if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(hdr[46:50]) {
		return nil, end, ErrPayloadCRC
	}

	if flags&flagStored != 0 {
		if int(uLen) != len(payload) {
			return nil, end, ErrCorrupt
		}
		out = append([]byte(nil), payload...)
	} else {
		out, err = decompress(d.bytes(), payload, maxOut)
		if err != nil {
			return nil, end, err
		}
		if len(out) != int(uLen) {
			return nil, end, ErrCorrupt
		}
	}
	return out, end, nil
}

// FindNext 从 off 起扫描下一个“magic 正确且头 CRC 合法”的块头，
// 用于块头本身损坏后的重新同步。找不到返回 -1。
func FindNext(buf []byte, off int) int {
	for i := off; i+HeaderSize <= len(buf); i++ {
		if binary.BigEndian.Uint32(buf[i:i+4]) == Magic &&
			buf[i+4] == Version &&
			crc32.ChecksumIEEE(buf[i:i+50]) == binary.BigEndian.Uint32(buf[i+50:i+54]) {
			return i
		}
	}
	return -1
}
