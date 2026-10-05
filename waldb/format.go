package waldb

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// 日志记录类型。
const (
	recBegin           byte = 1 // 事务开始
	recUpdate          byte = 2 // 页更新，含 before/after 镜像
	recCommit          byte = 3 // 提交；该记录持久化后事务才算提交成功
	recAbort           byte = 4 // 主动回滚
	recCLR             byte = 5 // 补偿日志记录（撤销的记录），含 undoNextLSN
	recEnd             byte = 6 // 事务结束（提交/回滚收尾）
	recCheckpointBegin byte = 7 // 模糊检查点起点
	recCheckpointEnd   byte = 8 // 检查点完成，payload 为事务表+脏页表快照
)

// NilLSN 表示空 LSN 链接。LSN 即记录在日志文件中的字节偏移，严格递增。
const NilLSN int64 = -1

const (
	// 记录头：type(1) lsn(8) prevLSN(8) txnID(8) payloadLen(4)
	headerSize = 1 + 8 + 8 + 8 + 4
	crcSize    = 4
	maxPayload = 1 << 20
)

var errCorruptTail = errors.New("waldb: incomplete or corrupt log tail")

// LogRecord 是一条预写日志记录。
type LogRecord struct {
	Type        byte
	LSN         int64
	PrevLSN     int64 // 同事务上一条记录
	TxnID       int64
	PageID      int32  // update / CLR
	Before      uint64 // update: 旧值；CLR: 被撤销记录的 after
	After       uint64 // update: 新值；CLR: 恢复后的值
	UndoNextLSN int64  // 仅 CLR：撤销链上下一个待撤销记录
	Payload     []byte // 仅 checkpoint_end：快照
}

func (r *LogRecord) encode() []byte {
	var body []byte
	switch r.Type {
	case recUpdate:
		body = make([]byte, 20)
		binary.LittleEndian.PutUint32(body[0:], uint32(r.PageID))
		binary.LittleEndian.PutUint64(body[4:], r.Before)
		binary.LittleEndian.PutUint64(body[12:], r.After)
	case recCLR:
		body = make([]byte, 28)
		binary.LittleEndian.PutUint32(body[0:], uint32(r.PageID))
		binary.LittleEndian.PutUint64(body[4:], r.Before)
		binary.LittleEndian.PutUint64(body[12:], r.After)
		binary.LittleEndian.PutUint64(body[20:], uint64(r.UndoNextLSN))
	case recCheckpointEnd:
		body = r.Payload
	}
	buf := make([]byte, headerSize+len(body)+crcSize)
	buf[0] = r.Type
	binary.LittleEndian.PutUint64(buf[1:], uint64(r.LSN))
	binary.LittleEndian.PutUint64(buf[9:], uint64(r.PrevLSN))
	binary.LittleEndian.PutUint64(buf[17:], uint64(r.TxnID))
	binary.LittleEndian.PutUint32(buf[25:], uint32(len(body)))
	copy(buf[headerSize:], body)
	crc := crc32.ChecksumIEEE(buf[:headerSize+len(body)])
	binary.LittleEndian.PutUint32(buf[headerSize+len(body):], crc)
	return buf
}

// decodeAt 解析位于 off 的记录，返回记录与下一条记录的偏移。
// 任何不完整（撕裂写）或校验失败都返回 errCorruptTail，调用方据此截断日志尾。
func decodeAt(data []byte, off int64) (*LogRecord, int64, error) {
	if off < 0 || off+headerSize > int64(len(data)) {
		return nil, off, errCorruptTail
	}
	h := data[off : off+headerSize]
	typ := h[0]
	if typ < recBegin || typ > recCheckpointEnd {
		return nil, off, errCorruptTail
	}
	r := &LogRecord{Type: typ}
	r.LSN = int64(binary.LittleEndian.Uint64(h[1:]))
	r.PrevLSN = int64(binary.LittleEndian.Uint64(h[9:]))
	r.TxnID = int64(binary.LittleEndian.Uint64(h[17:]))
	plen := int64(binary.LittleEndian.Uint32(h[25:]))
	if plen < 0 || plen > maxPayload {
		return nil, off, errCorruptTail
	}
	end := off + headerSize + plen + crcSize
	if end > int64(len(data)) {
		return nil, off, errCorruptTail
	}
	body := data[off+headerSize : off+headerSize+plen]
	want := binary.LittleEndian.Uint32(data[off+headerSize+plen : end])
	if crc32.ChecksumIEEE(data[off:end-crcSize]) != want {
		return nil, off, errCorruptTail
	}
	if r.LSN != off { // LSN 即偏移，防御错位垃圾数据
		return nil, off, errCorruptTail
	}
	switch typ {
	case recUpdate:
		if plen != 20 {
			return nil, off, errCorruptTail
		}
		r.PageID = int32(binary.LittleEndian.Uint32(body[0:]))
		r.Before = binary.LittleEndian.Uint64(body[4:])
		r.After = binary.LittleEndian.Uint64(body[12:])
	case recCLR:
		if plen != 28 {
			return nil, off, errCorruptTail
		}
		r.PageID = int32(binary.LittleEndian.Uint32(body[0:]))
		r.Before = binary.LittleEndian.Uint64(body[4:])
		r.After = binary.LittleEndian.Uint64(body[12:])
		r.UndoNextLSN = int64(binary.LittleEndian.Uint64(body[20:]))
	case recCheckpointEnd:
		r.Payload = append([]byte(nil), body...)
	}
	return r, end, nil
}
