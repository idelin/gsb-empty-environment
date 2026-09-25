package kvstore

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// 记录物理布局（追加写，永不原地修改）：
//
//	| crc32(4B) | keyLen(4B) | valLen(4B) | flags(1B) | key ... | value ... |
//
// crc32 覆盖其后所有字节；flags 最低位为墓碑标记。
// 断电/崩溃可能留下写了一半的尾部记录，扫描时靠长度+CRC 识别并截断。
const (
	flagTombstone = 1
	headerSize    = 13 // crc4 + klen4 + vlen4 + flags1
	maxKeySize    = 1 << 20
	maxValSize    = 1 << 30
)

var errCorrupt = errors.New("kvstore: corrupt record")

func encodeRecord(key, value []byte, tombstone bool) []byte {
	buf := make([]byte, headerSize+len(key)+len(value))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(key)))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(value)))
	if tombstone {
		buf[12] = flagTombstone
	}
	copy(buf[headerSize:], key)
	copy(buf[headerSize+len(key):], value)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.ChecksumIEEE(buf[4:]))
	return buf
}

// decodeRecord 解析一条完整记录，返回 key/value 副本与墓碑标记。
func decodeRecord(buf []byte) (key, value []byte, tombstone bool, err error) {
	if len(buf) < headerSize {
		return nil, nil, false, errCorrupt
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != crc32.ChecksumIEEE(buf[4:]) {
		return nil, nil, false, errCorrupt
	}
	klen := binary.LittleEndian.Uint32(buf[4:8])
	vlen := binary.LittleEndian.Uint32(buf[8:12])
	if int(klen)+int(vlen)+headerSize != len(buf) {
		return nil, nil, false, errCorrupt
	}
	key = append([]byte(nil), buf[headerSize:headerSize+klen]...)
	value = append([]byte(nil), buf[headerSize+klen:]...)
	return key, value, buf[12]&flagTombstone != 0, nil
}
