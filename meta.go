package diskbtree

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
)

// superblock(meta 文件)布局: 两个 64 字节的槽位交替写入。
// 每次提交把 txid 更大的记录写到 (txid % 2) 槽位, 恢复时取 CRC 校验
// 通过且 txid 最大的那条。单槽 64 字节 + CRC, 即使写入被断电撕裂,
// 也只会表现为该校验失败, 另一槽仍是上一个完整状态。
const (
	metaMagic    uint32 = 0xDBB7EE01
	metaSlotSize        = 64
	metaSlots           = 2
)

var errNoValidMeta = errors.New("diskbtree: no valid meta record")

type metaRecord struct {
	txid     uint64
	root     uint64
	numPages uint64
	pageSize uint32
}

func encodeMeta(m *metaRecord) []byte {
	buf := make([]byte, metaSlotSize)
	binary.LittleEndian.PutUint32(buf[0:4], metaMagic)
	binary.LittleEndian.PutUint64(buf[8:16], m.txid)
	binary.LittleEndian.PutUint64(buf[16:24], m.root)
	binary.LittleEndian.PutUint64(buf[24:32], m.numPages)
	binary.LittleEndian.PutUint32(buf[32:36], m.pageSize)
	binary.LittleEndian.PutUint32(buf[36:40], crc32.ChecksumIEEE(buf[0:36]))
	return buf
}

func decodeMeta(buf []byte) (*metaRecord, error) {
	if len(buf) < metaSlotSize {
		return nil, errNoValidMeta
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != metaMagic {
		return nil, errNoValidMeta
	}
	if crc32.ChecksumIEEE(buf[0:36]) != binary.LittleEndian.Uint32(buf[36:40]) {
		return nil, errNoValidMeta
	}
	return &metaRecord{
		txid:     binary.LittleEndian.Uint64(buf[8:16]),
		root:     binary.LittleEndian.Uint64(buf[16:24]),
		numPages: binary.LittleEndian.Uint64(buf[24:32]),
		pageSize: binary.LittleEndian.Uint32(buf[32:36]),
	}, nil
}

// readMeta 读取两个槽位, 返回校验通过且 txid 最大的记录。
func readMeta(f *os.File) (*metaRecord, error) {
	var best *metaRecord
	for i := 0; i < metaSlots; i++ {
		buf := make([]byte, metaSlotSize)
		if _, err := f.ReadAt(buf, int64(i*metaSlotSize)); err != nil {
			continue
		}
		m, err := decodeMeta(buf)
		if err != nil {
			continue
		}
		if best == nil || m.txid > best.txid {
			best = m
		}
	}
	if best == nil {
		return nil, errNoValidMeta
	}
	return best, nil
}

// writeMeta 把新状态写入 txid 对应的槽位(调用方负责随后 fsync)。
func (db *DB) writeMeta(txid, root, numPages uint64) error {
	m := &metaRecord{txid: txid, root: root, numPages: numPages, pageSize: uint32(db.pageSize)}
	_, err := db.meta.WriteAt(encodeMeta(m), int64(txid%metaSlots)*metaSlotSize)
	return err
}
