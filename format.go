package arcode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// 流格式（全部大端）：
//
//	偏移  长度  含义
//	0     4     魔数 "PAC1"
//	4     1     格式版本（当前为 1）
//	5     1     保留，必须为 0
//	6     8     原始数据长度（字节，不含终止符）
//	14    4     原始数据的 CRC32(IEEE) 校验和
//	18    ...   算术编码负载（数据符号 + 1 个终止符）
const (
	FormatVersion = 1
	headerLen     = 4 + 1 + 1 + 8 + 4
)

var magic = [4]byte{'P', 'A', 'C', '1'}

var (
	ErrBadMagic      = errors.New("arcode: 魔数不匹配")
	ErrBadVersion    = errors.New("arcode: 不支持的格式版本")
	ErrTooLarge      = errors.New("arcode: 声明长度超出解码输出预算")
	ErrPrematureEOF  = errors.New("arcode: 数据未填满声明长度就遇到终止符")
	ErrMissingEOF    = errors.New("arcode: 声明长度之后缺少终止符")
	ErrBadChecksum   = errors.New("arcode: CRC32 校验失败")
	ErrTruncatedHead = errors.New("arcode: 头部不完整")
)

// Encode 把 data 以带头的算术编码流写入 w。
func Encode(w io.Writer, data []byte) error {
	var hdr [headerLen]byte
	copy(hdr[0:4], magic[:])
	hdr[4] = FormatVersion
	hdr[5] = 0
	binary.BigEndian.PutUint64(hdr[6:14], uint64(len(data)))
	binary.BigEndian.PutUint32(hdr[14:18], crc32.ChecksumIEEE(data))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	e := newEncoder(w)
	for _, b := range data {
		e.encodeSymbol(int(b))
	}
	e.encodeSymbol(SymbolEOF)
	return e.finish()
}

// Decode 从 r 读取一个算术编码流并还原原始数据。
// maxOutput 是解码输出预算：头部声明长度超过它时立即拒绝，
// 因此解码的符号数与分配的内存都有界，错误流不可能无限解码。
func Decode(r io.Reader, maxOutput uint64) ([]byte, error) {
	var hdr [headerLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, ErrTruncatedHead
	}
	if !bytes.Equal(hdr[0:4], magic[:]) {
		return nil, ErrBadMagic
	}
	if hdr[4] != FormatVersion {
		return nil, ErrBadVersion
	}
	length := binary.BigEndian.Uint64(hdr[6:14])
	wantCRC := binary.BigEndian.Uint32(hdr[14:18])
	if length > maxOutput {
		return nil, ErrTooLarge
	}

	// 每个符号最多输出约 64 比特（归一化移位 + 挂起比特），
	// 据此限制读取的负载字节数，避免在畸形输入上无界读入。
	maxPayload := int64(length)*8 + 64
	payload, err := io.ReadAll(io.LimitReader(r, maxPayload))
	if err != nil {
		return nil, err
	}

	d := newDecoder(payload)
	out := make([]byte, 0, length)
	for i := uint64(0); i < length; i++ {
		s := d.decodeSymbol()
		if s == SymbolEOF {
			return nil, ErrPrematureEOF
		}
		out = append(out, byte(s))
	}
	if s := d.decodeSymbol(); s != SymbolEOF {
		return nil, ErrMissingEOF
	}
	if crc32.ChecksumIEEE(out) != wantCRC {
		return nil, ErrBadChecksum
	}
	return out, nil
}
