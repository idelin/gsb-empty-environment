package walstore

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// 日志记录布局:
//   [type:1][payloadLen:4][payload:payloadLen][crc32:4]
// crc 覆盖 type+len+payload。扫描时任何截断/撕裂都会表现为
// 长度不足或 crc 不匹配,从而安全地截断日志尾部。
const (
	recUpdate byte = 1 // 物理日志:整页后像
	recCommit byte = 2 // 事务提交标记
)

const (
	recHeaderSize = 5 // type(1) + len(4)
	recCRCSize    = 4
)

var errTornRecord = errors.New("walstore: torn or corrupt record")

func encodeRecord(typ byte, payload []byte) []byte {
	buf := make([]byte, recHeaderSize+len(payload)+recCRCSize)
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[recHeaderSize:], payload)
	crc := crc32.ChecksumIEEE(buf[:recHeaderSize+len(payload)])
	binary.BigEndian.PutUint32(buf[recHeaderSize+len(payload):], crc)
	return buf
}

// decodeRecord 从 buf 起始处解码一条记录,返回类型、载荷与总长度。
// 记录不完整或 crc 不匹配时返回 errTornRecord。
func decodeRecord(buf []byte) (typ byte, payload []byte, total int, err error) {
	if len(buf) < recHeaderSize {
		return 0, nil, 0, errTornRecord
	}
	typ = buf[0]
	n := binary.BigEndian.Uint32(buf[1:5])
	if n > 1<<30 { // 防御长度字段损坏
		return 0, nil, 0, errTornRecord
	}
	total = recHeaderSize + int(n) + recCRCSize
	if len(buf) < total {
		return 0, nil, 0, errTornRecord
	}
	body := buf[:recHeaderSize+int(n)]
	want := binary.BigEndian.Uint32(buf[recHeaderSize+int(n) : total])
	if crc32.ChecksumIEEE(body) != want {
		return 0, nil, 0, errTornRecord
	}
	return typ, buf[recHeaderSize : recHeaderSize+int(n)], total, nil
}

// ---- 载荷编解码 ----

// update 记录载荷: txid(8) | lsn(8) | pageID(4) | pagePayload(pagePayloadSize)
type updateRec struct {
	txid    uint64
	lsn     int64
	pageID  uint32
	payload []byte // 长度 = pageSize - pageLSNSize
}

func encodeUpdatePayload(u updateRec) []byte {
	b := make([]byte, 8+8+4+len(u.payload))
	binary.BigEndian.PutUint64(b[0:8], u.txid)
	binary.BigEndian.PutUint64(b[8:16], uint64(u.lsn))
	binary.BigEndian.PutUint32(b[16:20], u.pageID)
	copy(b[20:], u.payload)
	return b
}

func decodeUpdatePayload(b []byte) (updateRec, bool) {
	if len(b) < 20 {
		return updateRec{}, false
	}
	return updateRec{
		txid:    binary.BigEndian.Uint64(b[0:8]),
		lsn:     int64(binary.BigEndian.Uint64(b[8:16])),
		pageID:  binary.BigEndian.Uint32(b[16:20]),
		payload: b[20:],
	}, true
}

// commit 记录载荷: txid(8)
func encodeCommitPayload(txid uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, txid)
	return b
}

func decodeCommitPayload(b []byte) (uint64, bool) {
	if len(b) != 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(b), true
}

// ---- 日志写入器 ----

// LogWriter 顺序追加记录。LSN 即记录在文件中的起始偏移。
type LogWriter struct {
	f      File
	offset int64
}

func NewLogWriter(f File) *LogWriter { return &LogWriter{f: f, offset: f.Size()} }

// NextLSN 返回下一条记录将占用的 LSN(单线程追加,先取后写)。
func (w *LogWriter) NextLSN() int64 { return w.offset }

// Append 追加一条记录,处理短写:循环写满为止。
func (w *LogWriter) Append(typ byte, payload []byte) (int64, error) {
	buf := encodeRecord(typ, payload)
	lsn := w.offset
	written := 0
	for written < len(buf) {
		n, err := w.f.WriteAt(buf[written:], lsn+int64(written))
		written += n
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, errors.New("walstore: write made no progress")
		}
	}
	w.offset += int64(len(buf))
	return lsn, nil
}

// Sync 是持久化屏障:返回后,此前 Append 的所有记录断电不丢。
func (w *LogWriter) Sync() error { return w.f.Sync() }

// ---- 日志扫描 ----

type logRecord struct {
	typ     byte
	lsn     int64
	payload []byte
}

// scanLog 从头扫描,返回所有完整且校验通过的记录;
// 遇到撕裂/损坏的尾部即停止(前缀之后的字节一律忽略)。
func scanLog(f File) ([]logRecord, error) {
	size := f.Size()
	buf := make([]byte, size)
	got := int64(0)
	for got < size { // 同样容忍短读
		n, err := f.ReadAt(buf[got:], got)
		got += int64(n)
		if err != nil && n == 0 {
			break
		}
	}
	buf = buf[:got]
	var recs []logRecord
	off := int64(0)
	for off < int64(len(buf)) {
		typ, payload, total, err := decodeRecord(buf[off:])
		if err != nil {
			break // 撕裂尾部:截断
		}
		recs = append(recs, logRecord{typ: typ, lsn: off, payload: payload})
		off += int64(total)
	}
	return recs, nil
}
