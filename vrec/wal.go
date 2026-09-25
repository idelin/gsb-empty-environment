package vrec

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
)

// WAL 是只追加日志。每个帧自描述长度与 CRC，因此尾部撕裂在重放时可按帧截断。
//
// 帧布局（小端序）：
//
//	magic   [2]byte  0x56 0x52 ("VR")
//	flags   uint8    flagTombstone、flagCompact
//	klen    uint16   键长度
//	vlen    uint32   值长度（墓碑帧为 0）
//	ver     uint64   该变更生效版本；compact 帧中是快照上界
//	crc     uint32   crc32(flags|klen|vlen|ver|key|value)
//	key     []byte
//	value   []byte
const (
	frameHeaderLen = 2 + 1 + 2 + 4 + 8 + 4
	magic0         = byte('V')
	magic1         = byte('R')

	flagTombstone = 1 << 0
	flagCompact   = 1 << 1
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// appendFrame 向 f 追加一个帧。写与 Sync 都发生在 wmu 持锁期间，因此无需额外加锁。
// torn==true 时只写一半值、且不 Sync，用来模拟断电留下的撕裂尾帧。
func appendFrame(f *os.File, fr frame, torn bool) error {
	if len(fr.key) > 0xffff || len(fr.value) > 0xffffffff {
		panic("vrec: frame field overflow")
	}
	buf := make([]byte, frameHeaderLen+len(fr.key)+len(fr.value))
	buf[0] = magic0
	buf[1] = magic1
	var flags byte
	if fr.tombstone {
		flags |= flagTombstone
	}
	if fr.compact {
		flags |= flagCompact
	}
	buf[2] = flags
	binary.LittleEndian.PutUint16(buf[3:5], uint16(len(fr.key)))
	binary.LittleEndian.PutUint32(buf[5:9], uint32(len(fr.value)))
	binary.LittleEndian.PutUint64(buf[9:17], uint64(fr.ver))

	h := crc32.New(crcTable)
	h.Write(buf[2:17])
	h.Write([]byte(fr.key))
	h.Write(fr.value)
	binary.LittleEndian.PutUint32(buf[17:21], h.Sum32())

	copy(buf[frameHeaderLen:], fr.key)
	copy(buf[frameHeaderLen+len(fr.key):], fr.value)

	payload := buf
	if torn {
		// 至少写满头与若干字节，确保 CRC 必然不匹配且长度可能不足。
		n := frameHeaderLen + len(fr.key)
		if len(fr.value) > 1 {
			n++
		}
		payload = buf[:n]
	}
	if _, err := f.Write(payload); err != nil {
		return err
	}
	if torn {
		// 故意不 Sync：模拟断电时页缓存中不完整的数据。
		return nil
	}
	return f.Sync()
}

type frame struct {
	key       string
	value     []byte
	ver       int64
	tombstone bool
	compact   bool
}

// replayResult 是重放结果。
type replayResult struct {
	// nodes 重建出的版本链：key -> 按版本升序的节点。
	nodes map[string][]*node
	nextV int64 // 下一个待分配版本
	// validLen 已确认完整帧覆盖的字节数；之后的撕裂/损坏尾部应被截断。
	validLen int64
}

// replayWAL 解析日志。帧是长度自描述的：任何短读、坏魔数、坏 CRC 都视为
// 日志结束（断电只能损坏尾部），并通过 validLen 告知调用方截断。
func replayWAL(path string) (replayResult, error) {
	res := replayResult{nodes: make(map[string][]*node), nextV: 1}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return res, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return res, err
	}

	off := 0
	for off+frameHeaderLen <= len(data) {
		hdr := data[off : off+frameHeaderLen]
		if hdr[0] != magic0 || hdr[1] != magic1 {
			break
		}
		flags := hdr[2]
		klen := int(binary.LittleEndian.Uint16(hdr[3:5]))
		vlen := int(binary.LittleEndian.Uint32(hdr[5:9]))
		ver := int64(binary.LittleEndian.Uint64(hdr[9:17]))
		wantCRC := binary.LittleEndian.Uint32(hdr[17:21])

		end := off + frameHeaderLen + klen + vlen
		if end > len(data) {
			break // 截断的尾帧
		}
		body := data[off+frameHeaderLen : end]
		keyBytes := body[:klen]
		valBytes := body[klen:]

		chk := crc32.New(crcTable)
		chk.Write(hdr[2:17])
		chk.Write(keyBytes)
		chk.Write(valBytes)
		if chk.Sum32() != wantCRC {
			break
		}

		tomb := flags&flagTombstone != 0
		compact := flags&flagCompact != 0
		if compact {
			// 压缩边界帧：其后的帧全部版本 > ver，之前的状态不再需要。
			res.nodes = make(map[string][]*node)
			res.nextV = ver + 1
			off = end
			res.validLen = int64(end)
			continue
		}
		if ver >= res.nextV {
			res.nextV = ver + 1
		}
		n := &node{ver: ver, tomb: tomb}
		if !tomb {
			n.val = append([]byte(nil), valBytes...)
		}
		key := string(keyBytes)
		res.nodes[key] = append(res.nodes[key], n)
		off = end
		res.validLen = int64(end)
	}

	return res, nil
}
