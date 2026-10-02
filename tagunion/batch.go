package tagunion

import (
	"encoding/binary"
	"fmt"
)

// 批量格式：uvarint 对象数，随后依次拼接各对象帧。
// 解码是原子的：任一对象出错则整体失败，不返回部分结果；
// 错误携带分支路径（obj[i] 或 obj[i].field(name)）与字节偏移。

// EncodeBatch 把一组已编码帧打包为批量缓冲区。
func EncodeBatch(frames ...[]byte) ([]byte, error) {
	if len(frames) > MaxBatchCount {
		return nil, errf(0, "batch count %d exceeds max %d", len(frames), MaxBatchCount)
	}
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], uint64(len(frames)))
	out := append([]byte(nil), b[:n]...)
	for _, f := range frames {
		out = append(out, f...)
	}
	return out, nil
}

// DecodeBatch 原子地解码批量缓冲区。出错时返回 nil 与带路径/偏移的 *Error。
func (r *Registry) DecodeBatch(data []byte) ([]Value, error) {
	count, n, err := readUvarint(data, 0, "batch count")
	if err != nil {
		return nil, err
	}
	// 先验证数量上界再分配。
	if count > MaxBatchCount {
		return nil, errf(0, "batch count %d exceeds max %d", count, MaxBatchCount)
	}
	pos := n
	values := make([]Value, 0, count)
	for i := uint64(0); i < count; i++ {
		path := fmt.Sprintf("obj[%d]", i)
		v, consumed, err := r.decodeAt(data, pos, path)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
		pos += consumed
	}
	if pos != len(data) {
		return nil, errf(pos, "%d trailing bytes after %d objects", len(data)-pos, count)
	}
	return values, nil
}
