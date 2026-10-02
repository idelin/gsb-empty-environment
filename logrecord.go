package aries

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

// LSN 是日志序列号,直接使用日志文件中的字节偏移量。
type LSN = uint64

// NilLSN 表示空日志序列号。
const NilLSN LSN = 0

// RecordType 是 WAL 记录类型。
type RecordType uint8

const (
	TypeInvalid RecordType = iota
	TypeBegin
	TypeUpdate
	TypeCommit
	TypeAbort
	TypeEnd
	TypeCLR
	TypeCheckpoint
)

func (t RecordType) String() string {
	switch t {
	case TypeBegin:
		return "BEGIN"
	case TypeUpdate:
		return "UPDATE"
	case TypeCommit:
		return "COMMIT"
	case TypeAbort:
		return "ABORT"
	case TypeEnd:
		return "END"
	case TypeCLR:
		return "CLR"
	case TypeCheckpoint:
		return "CHECKPOINT"
	}
	return "INVALID"
}

// ErrCorruptRecord 表示日志记录损坏。
var ErrCorruptRecord = errors.New("aries: corrupt log record")

// CheckpointData 是模糊检查点(fuzzy checkpoint)携带的 ATT 与 DPT 快照。
type CheckpointData struct {
	// ActiveTxns: 事务 ID -> 该事务最后一条日志的 LSN。
	ActiveTxns map[uint64]LSN
	// DirtyPages: 页 ID -> recLSN(该页首次变脏的日志 LSN)。
	DirtyPages map[uint64]LSN
}

// LogRecord 是一条 WAL 记录。
type LogRecord struct {
	LSN         LSN
	PrevLSN     LSN // 同事务的前一条日志
	Type        RecordType
	TxnID       uint64
	PageID      uint64 // UPDATE/CLR 有效
	Offset      uint32 // 页内偏移,UPDATE/CLR 有效
	UndoNextLSN LSN    // CLR 专用:撤销链上下一个待撤销的记录
	BeforeImage []byte
	AfterImage  []byte
	Checkpoint  *CheckpointData // CHECKPOINT 有效
}

// 固定头:type(1) txn(8) prev(8) page(8) undoNext(8) offset(4) beforeLen(4) afterLen(4)
const fixedHeaderSize = 1 + 8 + 8 + 8 + 8 + 4 + 4 + 4

const maxImageSize = 1 << 20

func encodeCheckpointData(cd *CheckpointData) []byte {
	size := 4 + len(cd.ActiveTxns)*16 + 4 + len(cd.DirtyPages)*16
	buf := make([]byte, size)
	off := 0
	txnIDs := make([]uint64, 0, len(cd.ActiveTxns))
	for id := range cd.ActiveTxns {
		txnIDs = append(txnIDs, id)
	}
	sort.Slice(txnIDs, func(i, j int) bool { return txnIDs[i] < txnIDs[j] })
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(txnIDs)))
	off += 4
	for _, id := range txnIDs {
		binary.LittleEndian.PutUint64(buf[off:], id)
		binary.LittleEndian.PutUint64(buf[off+8:], cd.ActiveTxns[id])
		off += 16
	}
	pageIDs := make([]uint64, 0, len(cd.DirtyPages))
	for id := range cd.DirtyPages {
		pageIDs = append(pageIDs, id)
	}
	sort.Slice(pageIDs, func(i, j int) bool { return pageIDs[i] < pageIDs[j] })
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(pageIDs)))
	off += 4
	for _, id := range pageIDs {
		binary.LittleEndian.PutUint64(buf[off:], id)
		binary.LittleEndian.PutUint64(buf[off+8:], cd.DirtyPages[id])
		off += 16
	}
	return buf
}

func decodeCheckpointData(buf []byte) (*CheckpointData, error) {
	cd := &CheckpointData{ActiveTxns: map[uint64]LSN{}, DirtyPages: map[uint64]LSN{}}
	if len(buf) < 8 {
		return nil, ErrCorruptRecord
	}
	off := 0
	numTxns := int(binary.LittleEndian.Uint32(buf[off:]))
	off += 4
	if len(buf) < off+numTxns*16 {
		return nil, ErrCorruptRecord
	}
	for i := 0; i < numTxns; i++ {
		id := binary.LittleEndian.Uint64(buf[off:])
		last := binary.LittleEndian.Uint64(buf[off+8:])
		cd.ActiveTxns[id] = last
		off += 16
	}
	numPages := int(binary.LittleEndian.Uint32(buf[off:]))
	off += 4
	if len(buf) < off+numPages*16 {
		return nil, ErrCorruptRecord
	}
	for i := 0; i < numPages; i++ {
		id := binary.LittleEndian.Uint64(buf[off:])
		rec := binary.LittleEndian.Uint64(buf[off+8:])
		cd.DirtyPages[id] = rec
		off += 16
	}
	return cd, nil
}

// encode 将记录序列化为字节切片。
func (r *LogRecord) encode() []byte {
	var cp []byte
	if r.Type == TypeCheckpoint && r.Checkpoint != nil {
		cp = encodeCheckpointData(r.Checkpoint)
	}
	total := fixedHeaderSize + len(r.BeforeImage) + len(r.AfterImage) + 4 + len(cp)
	buf := make([]byte, total)
	buf[0] = byte(r.Type)
	binary.LittleEndian.PutUint64(buf[1:], r.TxnID)
	binary.LittleEndian.PutUint64(buf[9:], r.PrevLSN)
	binary.LittleEndian.PutUint64(buf[17:], r.PageID)
	binary.LittleEndian.PutUint64(buf[25:], r.UndoNextLSN)
	binary.LittleEndian.PutUint32(buf[33:], r.Offset)
	binary.LittleEndian.PutUint32(buf[37:], uint32(len(r.BeforeImage)))
	binary.LittleEndian.PutUint32(buf[41:], uint32(len(r.AfterImage)))
	off := fixedHeaderSize
	copy(buf[off:], r.BeforeImage)
	off += len(r.BeforeImage)
	copy(buf[off:], r.AfterImage)
	off += len(r.AfterImage)
	binary.LittleEndian.PutUint32(buf[off:], uint32(len(cp)))
	off += 4
	copy(buf[off:], cp)
	return buf
}

// readRecordAt 从 ra 的 off 处解码一条记录,返回记录与记录总字节数。
func readRecordAt(ra io.ReaderAt, off LSN) (*LogRecord, int, error) {
	hdr := make([]byte, fixedHeaderSize)
	if _, err := ra.ReadAt(hdr, int64(off)); err != nil {
		return nil, 0, err
	}
	r := &LogRecord{LSN: off}
	r.Type = RecordType(hdr[0])
	if r.Type < TypeBegin || r.Type > TypeCheckpoint {
		return nil, 0, fmt.Errorf("%w: bad type %d at LSN %d", ErrCorruptRecord, hdr[0], off)
	}
	r.TxnID = binary.LittleEndian.Uint64(hdr[1:])
	r.PrevLSN = binary.LittleEndian.Uint64(hdr[9:])
	r.PageID = binary.LittleEndian.Uint64(hdr[17:])
	r.UndoNextLSN = binary.LittleEndian.Uint64(hdr[25:])
	r.Offset = binary.LittleEndian.Uint32(hdr[33:])
	beforeLen := binary.LittleEndian.Uint32(hdr[37:])
	afterLen := binary.LittleEndian.Uint32(hdr[41:])
	if beforeLen > maxImageSize || afterLen > maxImageSize {
		return nil, 0, fmt.Errorf("%w: image too large at LSN %d", ErrCorruptRecord, off)
	}
	rest := make([]byte, int(beforeLen)+int(afterLen)+4)
	if _, err := ra.ReadAt(rest, int64(off)+fixedHeaderSize); err != nil {
		return nil, 0, err
	}
	r.BeforeImage = rest[:beforeLen]
	r.AfterImage = rest[beforeLen : beforeLen+afterLen]
	cpLen := binary.LittleEndian.Uint32(rest[int(beforeLen)+int(afterLen):])
	total := fixedHeaderSize + int(beforeLen) + int(afterLen) + 4 + int(cpLen)
	if cpLen > 0 {
		if cpLen > 1<<24 {
			return nil, 0, fmt.Errorf("%w: checkpoint too large at LSN %d", ErrCorruptRecord, off)
		}
		cpBuf := make([]byte, cpLen)
		if _, err := ra.ReadAt(cpBuf, int64(off)+int64(total)-int64(cpLen)); err != nil {
			return nil, 0, err
		}
		cd, err := decodeCheckpointData(cpBuf)
		if err != nil {
			return nil, 0, err
		}
		r.Checkpoint = cd
	}
	return r, total, nil
}
