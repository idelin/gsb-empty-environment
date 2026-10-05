package main

import (
	"errors"
	"fmt"
	"os"
)

/*
故障点枚举：数据与索引交替写入（append 顺序：写数据 -> 写索引 -> 按需 fsync），
在任意两步之间崩溃都会留下一种可识别的文件状态：

	F0 数据未写、索引未写            -> 无影响。
	F1 数据已写未落盘、索引未写      -> 两者一起丢失，安全。
	F2 数据已落盘、索引未写          -> 索引落后于数据：恢复时从最后一个有效
	                                   索引项之后增量扫描数据、补建索引。
	F3 索引已落盘、数据未落盘        -> 索引含未持久化尾部：索引项指向的数据
	                                   缺失或校验不符，校验时整条丢弃。
	F4 数据记录撕裂写（部分字节）    -> 数据尾部出现不完整记录：正常的尾截断，
	                                   截断到上一条完整记录边界。
	F5 索引项撕裂写（长度非 16 倍数）-> 索引尾部残缺字节直接截断。
	F6 完整记录内容腐败（比特衰减等）-> 记录 framing 完整但 CRC 不符：必须
	                                   显式报告，绝不能当作普通尾截断跳过；
	                                   恢复在该记录前停止并返回错误。

重开一致性：恢复后数据文件与索引文件严格一一对应（索引项 i 描述第 i 条记录），
再次打开时恢复为幂等空操作，已恢复的记录不漏不重。
*/

// ErrCorruptRecord 表示扫描到一条 framing 完整但内容腐败的记录。
var ErrCorruptRecord = errors.New("logrec: corrupt record encountered")

// CorruptRecord 描述一条完整但校验失败的记录。
type CorruptRecord struct {
	Offset    int64  // 记录在数据文件中的偏移
	Length    uint32 // payload 长度
	HeaderCRC uint32 // 记录头中声明的 CRC
	ActualCRC uint32 // 实际算出的 CRC
}

// Report 描述一次恢复输出的修复范围。
type Report struct {
	DataSizeBefore  int64 // 恢复前数据文件大小
	IndexSizeBefore int64 // 恢复前索引文件大小

	ValidIndexEntries   int // 校验通过的既有索引项数
	DroppedIndexEntries int // 因指向未持久化/无效数据而丢弃的索引项数
	PartialIndexTail    int // 截掉的索引尾部残缺字节数（F5）

	RebuildFromOffset int64 // 增量补索引的数据起始偏移（无补建为 -1）
	RebuildToOffset   int64 // 补建结束偏移（即恢复后数据逻辑末尾）
	RebuiltEntries    int   // 补建的索引项数（F2）

	TruncatedDataAt int64 // 数据尾截断位置（无截断为 -1，F4）
	TruncatedBytes  int64 // 截掉的尾部字节数

	Corrupt []CorruptRecord // 完整但腐败的记录（F6），非空则恢复失败
}

func (r *Report) String() string {
	s := fmt.Sprintf("valid index entries: %d, dropped: %d, partial index tail: %d bytes",
		r.ValidIndexEntries, r.DroppedIndexEntries, r.PartialIndexTail)
	if r.RebuiltEntries > 0 {
		s += fmt.Sprintf("\nrebuilt %d index entries over data range [%d, %d)",
			r.RebuiltEntries, r.RebuildFromOffset, r.RebuildToOffset)
	} else {
		s += "\nno index rebuild needed"
	}
	if r.TruncatedDataAt >= 0 {
		s += fmt.Sprintf("\ntruncated data tail at offset %d (%d bytes)",
			r.TruncatedDataAt, r.TruncatedBytes)
	}
	for _, c := range r.Corrupt {
		s += fmt.Sprintf("\nCORRUPT record at offset %d: length=%d header crc=%08x actual crc=%08x",
			c.Offset, c.Length, c.HeaderCRC, c.ActualCRC)
	}
	return s
}

// scanResult 是数据扫描的单步结果。
type scanResult int

const (
	scanOK      scanResult = iota // 读到一条完整有效记录
	scanTail                      // 不完整/不可 framing 的尾部（F4），可截断
	scanCorrupt                   // 完整但 CRC 不符的记录（F6）
)

// scanRecord 在 data 的 pos 处尝试解析一条记录。
// 返回结果类别、记录总长（仅 scanOK/scanCorrupt 有效）与记录头信息。
func scanRecord(data *os.File, pos, dataLen int64) (scanResult, int64, uint32, uint32, error) {
	var hdr [recordHeaderSize]byte
	if dataLen-pos < recordHeaderSize {
		return scanTail, 0, 0, 0, nil
	}
	if _, err := data.ReadAt(hdr[:], pos); err != nil {
		return scanTail, 0, 0, 0, err
	}
	magic := le32(hdr[0:4])
	length := le32(hdr[4:8])
	headerCRC := le32(hdr[8:12])
	if magic != recordMagic {
		// 无法 framing：视为撕裂写留下的垃圾尾（F4）。
		return scanTail, 0, 0, 0, nil
	}
	total := int64(recordHeaderSize) + int64(length)
	if dataLen-pos < total {
		// 头完整但 payload 未写完：撕裂写尾（F4）。
		return scanTail, 0, 0, 0, nil
	}
	payload := make([]byte, length)
	if _, err := data.ReadAt(payload, pos+recordHeaderSize); err != nil {
		return scanTail, 0, 0, 0, err
	}
	actual := checksum(payload)
	if actual != headerCRC {
		// framing 完整但内容腐败（F6）：必须报告，禁止当尾截断。
		return scanCorrupt, total, headerCRC, actual, nil
	}
	return scanOK, total, headerCRC, actual, nil
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// Recover 打开 data/index 文件对，找出最后一个有效索引项，
// 然后增量补建索引，使两个文件重新一致。返回修复范围报告。
//
// 若遇到完整但腐败的记录（F6），报告其位置并返回 ErrCorruptRecord；
// 此时腐败记录及其后的数据保持原样（不截断、不索引），
// 腐败点之前的部分已恢复一致，可安全重开。
func Recover(dataPath, indexPath string) (*Report, error) {
	rep := &Report{RebuildFromOffset: -1, TruncatedDataAt: -1}

	data, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	defer data.Close()
	index, err := os.OpenFile(indexPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	defer index.Close()

	dfi, err := data.Stat()
	if err != nil {
		return nil, err
	}
	ifi, err := index.Stat()
	if err != nil {
		return nil, err
	}
	dataLen := dfi.Size()
	indexLen := ifi.Size()
	rep.DataSizeBefore, rep.IndexSizeBefore = dataLen, indexLen

	// F5：索引尾部撕裂写，截掉不足一个索引项的残缺字节。
	if rem := indexLen % indexEntrySize; rem != 0 {
		rep.PartialIndexTail = int(rem)
		indexLen -= rem
		if err := index.Truncate(indexLen); err != nil {
			return nil, err
		}
	}

	// 第一步：从头校验索引项，找出最后一个有效项。
	// 有效 = 与前一项严格衔接 + 指向的数据完整存在 + 摘要与数据 CRC 一致。
	total := indexLen / indexEntrySize
	lastValid := int64(-1) // 最后一个有效索引项序号
	resume := int64(0)     // 最后一个有效记录之后的数据偏移
	buf := make([]byte, indexEntrySize)
	for i := int64(0); i < total; i++ {
		if _, err := index.ReadAt(buf, i*indexEntrySize); err != nil {
			return nil, err
		}
		e := decodeIndexEntry(buf)
		if e.Offset != uint64(resume) {
			break // 链断裂：之后皆为未持久化/垃圾尾（F3）
		}
		end := int64(e.Offset) + recordHeaderSize + int64(e.Length)
		if end > dataLen {
			break // 数据未持久化（F3）
		}
		res, _, _, _, err := scanRecord(data, int64(e.Offset), dataLen)
		if err != nil {
			return nil, err
		}
		if res != scanOK {
			break // 数据无效（含腐败）：索引项不可信
		}
		// 摘要与数据互相绑定校验。
		var hdr [recordHeaderSize]byte
		if _, err := data.ReadAt(hdr[:], int64(e.Offset)); err != nil {
			return nil, err
		}
		if le32(hdr[4:8]) != e.Length || le32(hdr[8:12]) != e.Digest {
			break
		}
		lastValid = i
		resume = end
	}
	rep.ValidIndexEntries = int(lastValid + 1)
	rep.DroppedIndexEntries = int(total - (lastValid + 1))

	// 丢弃无效索引尾（F3）。
	keepLen := (lastValid + 1) * indexEntrySize
	if keepLen != indexLen {
		if err := index.Truncate(keepLen); err != nil {
			return nil, err
		}
	}

	// 第二步：从 resume 起增量扫描数据，补建索引（F2）。
	// 写前显式定位到保留索引的末尾（此前只做过 ReadAt/Truncate，写偏移在 0）。
	if _, err := index.Seek(keepLen, 0); err != nil {
		return rep, err
	}
	if resume < dataLen {
		rep.RebuildFromOffset = resume
	}
	pos := resume
	for pos < dataLen {
		res, recLen, headerCRC, actual, err := scanRecord(data, pos, dataLen)
		if err != nil {
			return rep, err
		}
		switch res {
		case scanTail:
			// F4：普通尾截断。
			rep.TruncatedDataAt = pos
			rep.TruncatedBytes = dataLen - pos
			if err := data.Truncate(pos); err != nil {
				return rep, err
			}
			dataLen = pos
		case scanCorrupt:
			// F6：完整坏记录，报告并停止；不截断、不索引。
			var hdr [recordHeaderSize]byte
			if _, err := data.ReadAt(hdr[:], pos); err != nil {
				return rep, err
			}
			rep.Corrupt = append(rep.Corrupt, CorruptRecord{
				Offset:    pos,
				Length:    le32(hdr[4:8]),
				HeaderCRC: headerCRC,
				ActualCRC: actual,
			})
			// 截断索引到已恢复部分，保证腐败点之前一致。
			if err := data.Sync(); err != nil {
				return rep, err
			}
			if err := index.Sync(); err != nil {
				return rep, err
			}
			return rep, ErrCorruptRecord
		case scanOK:
			var hdr [recordHeaderSize]byte
			if _, err := data.ReadAt(hdr[:], pos); err != nil {
				return rep, err
			}
			entry := encodeIndexEntry(indexEntry{
				Offset: uint64(pos),
				Length: le32(hdr[4:8]),
				Digest: le32(hdr[8:12]),
			})
			if _, err := index.Write(entry); err != nil {
				return rep, err
			}
			rep.RebuiltEntries++
			pos += recLen
		}
	}
	rep.RebuildToOffset = pos

	if err := data.Sync(); err != nil {
		return rep, err
	}
	if err := index.Sync(); err != nil {
		return rep, err
	}
	return rep, nil
}
