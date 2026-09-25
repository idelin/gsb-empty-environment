package pagealloc

import (
	"encoding/binary"
	"hash/crc32"
)

// 日志文件布局（小端）：
//
//	magic   4B   "PGJ1"
//	opCount 4B
//	ops     opCount * 17B:  op(1B) | start(8B) | count(8B)
//	commit  1B    commitMark
//	crc32   4B    覆盖 commit 字节之前的全部内容
//
// 只有完整写入 commit 记录且 CRC 校验通过，事务才视为已提交；
// 否则恢复时整体丢弃，等价于回滚。
const (
	journalMagic = "PGJ1"
	opRecSize    = 1 + 8 + 8
	commitMark   = 0xC0
)

const (
	opAlloc byte = 1
	opFree  byte = 2
)

type op struct {
	typ   byte
	start uint64
	count uint64
}

// encodeJournalOps 序列化头部与全部操作记录（不含 commit 记录）。
func encodeJournalOps(ops []op) []byte {
	buf := make([]byte, 0, 8+len(ops)*opRecSize)
	buf = append(buf, journalMagic...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(ops)))
	for _, o := range ops {
		buf = append(buf, o.typ)
		buf = binary.LittleEndian.AppendUint64(buf, o.start)
		buf = binary.LittleEndian.AppendUint64(buf, o.count)
	}
	return buf
}

// encodeCommitRecord 生成 commit 记录：标记字节 + 对 ops 字节的 CRC32。
func encodeCommitRecord(opsBytes []byte) []byte {
	out := make([]byte, 0, 5)
	out = append(out, commitMark)
	return binary.LittleEndian.AppendUint32(out, crc32.ChecksumIEEE(opsBytes))
}

// decodeJournal 解析日志。返回 (ops, committed, err)。
// 文件不存在视为无日志；未提交或校验失败不算错误，仅 committed=false。
func decodeJournal(data []byte) ([]op, bool, error) {
	if len(data) == 0 {
		return nil, false, nil
	}
	hdr := 4 + 4
	if len(data) < hdr || string(data[:4]) != journalMagic {
		return nil, false, nil // 半截写入的头部：按未提交处理
	}
	n := binary.LittleEndian.Uint32(data[4:8])
	opsEnd := hdr + int(n)*opRecSize
	if n > 1<<20 || opsEnd > len(data) {
		return nil, false, nil
	}
	ops := make([]op, 0, n)
	for off := hdr; off < opsEnd; off += opRecSize {
		typ := data[off]
		if typ != opAlloc && typ != opFree {
			return nil, false, nil
		}
		ops = append(ops, op{
			typ:   typ,
			start: binary.LittleEndian.Uint64(data[off+1 : off+9]),
			count: binary.LittleEndian.Uint64(data[off+9 : off+17]),
		})
	}
	// commit 记录必须完整且 CRC 匹配
	if len(data) < opsEnd+5 || data[opsEnd] != commitMark {
		return nil, false, nil
	}
	want := binary.LittleEndian.Uint32(data[opsEnd+1 : opsEnd+5])
	if crc32.ChecksumIEEE(data[:opsEnd]) != want {
		return nil, false, nil
	}
	return ops, true, nil
}
