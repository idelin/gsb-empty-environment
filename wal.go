package walstore

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

const (
	recData   byte = 1
	recCommit byte = 2

	walHeaderSize = 8 // uint32 长度 + uint32 CRC
	maxRecordSize = 1 << 20
)

// walRecord 是 WAL 里的一条记录：页镜像（redo）或提交标记。
type walRecord struct {
	lsn    uint64
	txnID  uint64
	pageID uint32
	data   []byte // commit 记录为 nil
	commit bool
}

// 记录格式: [len uint32][crc32 uint32][type byte][payload...]
// len 覆盖 type+payload；CRC 覆盖 type+payload。
// 撕裂/截断的记录会在扫描时被长度或 CRC 校验拦截。
func encodeRecord(typ byte, payload []byte) []byte {
	body := make([]byte, 1+len(payload))
	body[0] = typ
	copy(body[1:], payload)
	rec := make([]byte, walHeaderSize+len(body))
	binary.BigEndian.PutUint32(rec[0:4], uint32(len(body)))
	binary.BigEndian.PutUint32(rec[4:8], crc32.ChecksumIEEE(body))
	copy(rec[walHeaderSize:], body)
	return rec
}

func encodeDataRecord(lsn, txnID uint64, pageID uint32, data []byte) []byte {
	payload := make([]byte, 8+8+4+len(data))
	binary.BigEndian.PutUint64(payload[0:8], lsn)
	binary.BigEndian.PutUint64(payload[8:16], txnID)
	binary.BigEndian.PutUint32(payload[16:20], pageID)
	copy(payload[20:], data)
	return encodeRecord(recData, payload)
}

func encodeCommitRecord(lsn, txnID uint64) []byte {
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[0:8], lsn)
	binary.BigEndian.PutUint64(payload[8:16], txnID)
	return encodeRecord(recCommit, payload)
}

func decodeRecord(body []byte) (walRecord, bool) {
	if len(body) < 1 {
		return walRecord{}, false
	}
	switch body[0] {
	case recData:
		p := body[1:]
		if len(p) != 20+PageDataSize {
			return walRecord{}, false
		}
		return walRecord{
			lsn:    binary.BigEndian.Uint64(p[0:8]),
			txnID:  binary.BigEndian.Uint64(p[8:16]),
			pageID: binary.BigEndian.Uint32(p[16:20]),
			data:   append([]byte(nil), p[20:]...),
		}, true
	case recCommit:
		p := body[1:]
		if len(p) != 16 {
			return walRecord{}, false
		}
		return walRecord{
			lsn:    binary.BigEndian.Uint64(p[0:8]),
			txnID:  binary.BigEndian.Uint64(p[8:16]),
			commit: true,
		}, true
	}
	return walRecord{}, false
}

// scanWAL 读出 WAL 的有效前缀：遇到第一条截断、撕裂或 CRC 不符的
// 记录即停止，并返回有效日志的结束偏移（之后的字节一律视为垃圾）。
func scanWAL(f File) (recs []walRecord, validEnd int64, err error) {
	size, err := f.Size()
	if err != nil {
		return nil, 0, err
	}
	var off int64
	for off+walHeaderSize <= size {
		var hdr [walHeaderSize]byte
		if _, err := readFull(f, hdr[:], off); err != nil {
			return nil, 0, err
		}
		length := binary.BigEndian.Uint32(hdr[0:4])
		crc := binary.BigEndian.Uint32(hdr[4:8])
		if length < 1 || length > maxRecordSize || off+walHeaderSize+int64(length) > size {
			break // 撕裂/垃圾尾部
		}
		body := make([]byte, length)
		if _, err := readFull(f, body, off+walHeaderSize); err != nil {
			return nil, 0, err
		}
		if crc32.ChecksumIEEE(body) != crc {
			break // 损坏记录：视为日志结尾
		}
		rec, ok := decodeRecord(body)
		if !ok {
			break
		}
		recs = append(recs, rec)
		off += walHeaderSize + int64(length)
	}
	return recs, off, nil
}

func readFull(f File, p []byte, off int64) (int, error) {
	total := 0
	for total < len(p) {
		n, err := f.ReadAt(p[total:], off+int64(total))
		total += n
		if err != nil {
			if err == io.EOF && total == len(p) {
				return total, nil
			}
			return total, err
		}
		if n == 0 {
			return total, io.ErrUnexpectedEOF
		}
	}
	return total, nil
}
