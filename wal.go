package pagedb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
)

// WAL 记录类型。
const (
	recUpdate byte = 1 // 页更新：携带整页新镜像
	recCommit byte = 2 // 事务提交标记：落盘并 fsync 即事务持久
)

// 记录布局（小端）：
//
//	[0:1]     类型
//	[1:9]     LSN（单调递增，同时作为页版本号）
//	[9:17]    事务 ID
//	[17:25]   页 ID（COMMIT 记录为 0）
//	[25:29]   数据长度 n（<= MaxPageData）
//	[29:29+n] 数据
//	末尾 4 字节：对前面全部字节的 CRC32
const recHeaderSize = 1 + 8 + 8 + 8 + 4
const recCRCSize = 4

var errBadRecord = errors.New("pagedb: malformed wal record")

// ErrMiddleCorruption 表示 WAL 中间出现损坏记录且其后仍存在合法记录，
// 这不是可忽略的撕裂尾，必须报错而不是静默截断。
var ErrMiddleCorruption = errors.New("pagedb: wal corrupt in the middle (valid records follow the bad one)")

type record struct {
	typ    byte
	lsn    uint64
	txid   uint64
	pageID uint64
	data   []byte
}

func encodeRecord(r record) []byte {
	buf := make([]byte, recHeaderSize+len(r.data)+recCRCSize)
	buf[0] = r.typ
	binary.LittleEndian.PutUint64(buf[1:9], r.lsn)
	binary.LittleEndian.PutUint64(buf[9:17], r.txid)
	binary.LittleEndian.PutUint64(buf[17:25], r.pageID)
	binary.LittleEndian.PutUint32(buf[25:29], uint32(len(r.data)))
	copy(buf[recHeaderSize:], r.data)
	crc := crc32.ChecksumIEEE(buf[:len(buf)-recCRCSize])
	binary.LittleEndian.PutUint32(buf[len(buf)-recCRCSize:], crc)
	return buf
}

// decodeOne 从 buf 开头解析一条记录，返回记录与其占用字节数。
func decodeOne(buf []byte) (record, int, error) {
	if len(buf) < recHeaderSize+recCRCSize {
		return record{}, 0, errBadRecord
	}
	n := binary.LittleEndian.Uint32(buf[25:29])
	if n > MaxPageData {
		return record{}, 0, errBadRecord
	}
	total := recHeaderSize + int(n) + recCRCSize
	if len(buf) < total {
		return record{}, 0, errBadRecord
	}
	rec, err := decodeRecord(buf[:total])
	if err != nil {
		return record{}, 0, err
	}
	return rec, total, nil
}

func decodeRecord(buf []byte) (record, error) {
	if len(buf) < recHeaderSize+recCRCSize {
		return record{}, errBadRecord
	}
	var r record
	r.typ = buf[0]
	if r.typ != recUpdate && r.typ != recCommit {
		return record{}, errBadRecord
	}
	r.lsn = binary.LittleEndian.Uint64(buf[1:9])
	r.txid = binary.LittleEndian.Uint64(buf[9:17])
	r.pageID = binary.LittleEndian.Uint64(buf[17:25])
	n := binary.LittleEndian.Uint32(buf[25:29])
	if int(n) != len(buf)-recHeaderSize-recCRCSize {
		return record{}, errBadRecord
	}
	want := binary.LittleEndian.Uint32(buf[len(buf)-recCRCSize:])
	if crc32.ChecksumIEEE(buf[:len(buf)-recCRCSize]) != want {
		return record{}, errBadRecord
	}
	r.data = append([]byte(nil), buf[recHeaderSize:recHeaderSize+n]...)
	return r, nil
}

type walScan struct {
	records  []record
	goodLen  int64 // 合法前缀长度（字节）
	tornTail bool  // 末尾存在残缺记录（崩溃/断电撕裂写）
}

// scanWAL 顺序解析 WAL。末尾残缺视为撕裂尾（打开时截断）；
// 中间损坏且其后还能找到合法记录则报 ErrMiddleCorruption。
func scanWAL(path string) (*walScan, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &walScan{}, nil
	}
	if err != nil {
		return nil, err
	}
	res := &walScan{}
	off := 0
	for off < len(data) {
		rec, n, err := decodeOne(data[off:])
		if err != nil {
			if hasValidRecordAfter(data, off) {
				return nil, fmt.Errorf("%w: offset %d", ErrMiddleCorruption, off)
			}
			res.tornTail = true
			break
		}
		res.records = append(res.records, rec)
		off += n
	}
	res.goodLen = int64(off)
	return res, nil
}

// hasValidRecordAfter 从 off+1 起逐字节重同步，判断损坏点之后
// 是否还存在完整合法的记录。CRC32 使随机数据误判概率约 2^-32。
func hasValidRecordAfter(data []byte, off int) bool {
	for i := off + 1; i < len(data); i++ {
		if _, _, err := decodeOne(data[i:]); err == nil {
			return true
		}
	}
	return false
}
