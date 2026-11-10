package offlinecli

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// 紧凑二进制补丁格式（小端）：
//
//	magic   = "OFWP" (4 字节)
//	version = uint16，当前为 1
//	opCount = uint32
//	op[]:
//	  tag   uint8   (1=COPY 原始区, 2=COPY 已生成区, 3=ADD)
//	  src   uint64  (ADD 时为 0)
//	  dst   uint64  (ADD 时为 0)
//	  len   uint64
//	  data  len 字节 (仅 ADD)
//
// 编解码不按镜像大小展开数据，ADD 有效载荷上限由解码方约束。

var patchMagic = [4]byte{'O', 'F', 'W', 'P'}

const (
	patchVersion  uint16 = 1
	maxPatchBytes        = 256 * 1024 * 1024
	maxOpCount           = 1 << 28
)

// EncodePatch 将操作列表编码为二进制补丁。
func EncodePatch(ops []Op) ([]byte, error) {
	if len(ops) > maxOpCount {
		return nil, fmt.Errorf("too many ops: %d: %w", len(ops), ErrMalformedPatch)
	}
	var buf bytes.Buffer
	buf.Write(patchMagic[:])
	if err := binary.Write(&buf, binary.LittleEndian, patchVersion); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(ops))); err != nil {
		return nil, err
	}
	for i, op := range ops {
		switch op.Type {
		case OpCopyOriginal, OpCopyGenerated, OpAdd:
		default:
			return nil, fmt.Errorf("op %d: unknown type %d: %w", i, op.Type, ErrMalformedPatch)
		}
		if op.Type == OpAdd {
			if op.Len != uint64(len(op.Data)) {
				return nil, fmt.Errorf("op %d: add length %d != data %d: %w", i, op.Len, len(op.Data), ErrMalformedPatch)
			}
		} else if len(op.Data) != 0 {
			return nil, fmt.Errorf("op %d: non-add op must not carry data: %w", i, ErrMalformedPatch)
		}
		if err := buf.WriteByte(byte(op.Type)); err != nil {
			return nil, err
		}
		for _, v := range []uint64{op.Src, op.Dst, op.Len} {
			if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
				return nil, err
			}
		}
		if op.Type == OpAdd {
			buf.Write(op.Data)
		}
	}
	if buf.Len() > maxPatchBytes {
		return nil, fmt.Errorf("encoded patch %d bytes exceeds limit %d: %w", buf.Len(), maxPatchBytes, ErrMalformedPatch)
	}
	return buf.Bytes(), nil
}

// DecodePatch 解码二进制补丁，并对 ADD 有效载荷施加上限保护。
func DecodePatch(r io.Reader) ([]Op, error) {
	var magic [4]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return nil, fmt.Errorf("read magic: %w", wrapMalformed(err))
	}
	if magic != patchMagic {
		return nil, fmt.Errorf("bad magic %q: %w", string(magic[:]), ErrMalformedPatch)
	}
	var version uint16
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return nil, fmt.Errorf("read version: %w", wrapMalformed(err))
	}
	if version != patchVersion {
		return nil, fmt.Errorf("unsupported patch version %d: %w", version, ErrMalformedPatch)
	}
	var count uint32
	if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
		return nil, fmt.Errorf("read op count: %w", wrapMalformed(err))
	}
	if count > maxOpCount {
		return nil, fmt.Errorf("op count %d exceeds limit: %w", count, ErrMalformedPatch)
	}
	ops := make([]Op, 0, count)
	totalData := uint64(0)
	for i := uint32(0); i < count; i++ {
		tag, err := readByte(r)
		if err != nil {
			return nil, fmt.Errorf("op %d: read tag: %w", i, wrapMalformed(err))
		}
		op := Op{Type: OpType(tag)}
		switch op.Type {
		case OpCopyOriginal, OpCopyGenerated, OpAdd:
		default:
			return nil, fmt.Errorf("op %d: unknown tag %d: %w", i, tag, ErrMalformedPatch)
		}
		for _, field := range []*uint64{&op.Src, &op.Dst, &op.Len} {
			if err := binary.Read(r, binary.LittleEndian, field); err != nil {
				return nil, fmt.Errorf("op %d: read field: %w", i, wrapMalformed(err))
			}
		}
		if op.Type == OpAdd {
			totalData += op.Len
			if totalData > maxPatchBytes || op.Len > maxPatchBytes {
				return nil, fmt.Errorf("op %d: add payload too large: %w", i, ErrMalformedPatch)
			}
			op.Data = make([]byte, op.Len)
			if op.Len > 0 {
				if _, err := io.ReadFull(r, op.Data); err != nil {
					return nil, fmt.Errorf("op %d: read add data: %w", i, wrapMalformed(err))
				}
			}
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func readByte(r io.Reader) (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r, b[:])
	return b[0], err
}

func wrapMalformed(err error) error {
	return fmt.Errorf("%v: %w", err, ErrMalformedPatch)
}
