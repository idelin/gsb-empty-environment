package mvcc

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// 日志格式：每次 Commit 追加一个帧（frame），帧是事务的原子单位。
//
//	+------------+-----------+============================+
//	| len  (4B)  | crc (4B)  | payload (len B)            |
//	+------------+-----------+============================+
//
// payload:
//
//	version(8B) | opCount(4B) | [ op(1B) keyLen(4B) valLen(4B) key val ]...
//
// crc 覆盖 payload。恢复时若尾部帧长度不足或 CRC 不匹配，
// 视为断电造成的撕裂写（torn write），截断到最后一个完整帧。
const (
	opPut byte = 1
	opDel byte = 2

	frameHeaderLen = 8 // len(4) + crc(4)
)

type logOp struct {
	del   bool
	key   string
	value []byte
}

func encodeFrame(version uint64, ops []logOp) []byte {
	size := 8 + 4
	for _, op := range ops {
		size += 1 + 4 + 4 + len(op.key) + len(op.value)
	}
	payload := make([]byte, 0, size)
	payload = binary.BigEndian.AppendUint64(payload, version)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(ops)))
	for _, op := range ops {
		b := opPut
		if op.del {
			b = opDel
		}
		payload = append(payload, b)
		payload = binary.BigEndian.AppendUint32(payload, uint32(len(op.key)))
		payload = binary.BigEndian.AppendUint32(payload, uint32(len(op.value)))
		payload = append(payload, op.key...)
		payload = append(payload, op.value...)
	}
	frame := make([]byte, 0, frameHeaderLen+len(payload))
	frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
	frame = binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(payload))
	frame = append(frame, payload...)
	return frame
}

// replayLog 顺序扫描日志，返回所有完整且 CRC 正确的帧，
// 以及有效字节数。调用方负责把文件截断到 validLen。
func replayLog(f *os.File) (frames [][]byte, validLen int64, err error) {
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}
	var off int64
	for {
		header := make([]byte, frameHeaderLen)
		n, rerr := io.ReadFull(f, header)
		if rerr == io.EOF && n == 0 {
			break // 干净的文件尾
		}
		if rerr != nil {
			break // 头部不完整：撕裂写，截断
		}
		length := binary.BigEndian.Uint32(header[0:4])
		crc := binary.BigEndian.Uint32(header[4:8])
		if length > 1<<30 {
			break // 长度明显损坏
		}
		payload := make([]byte, length)
		if _, rerr = io.ReadFull(f, payload); rerr != nil {
			break // payload 不完整
		}
		if crc32.ChecksumIEEE(payload) != crc {
			break // CRC 不匹配
		}
		frames = append(frames, payload)
		off += frameHeaderLen + int64(length)
	}
	return frames, off, nil
}

func decodeFrame(payload []byte) (version uint64, ops []logOp, err error) {
	if len(payload) < 12 {
		return 0, nil, fmt.Errorf("mvcc: frame payload too short")
	}
	version = binary.BigEndian.Uint64(payload[0:8])
	count := binary.BigEndian.Uint32(payload[8:12])
	rest := payload[12:]
	for i := uint32(0); i < count; i++ {
		if len(rest) < 9 {
			return 0, nil, fmt.Errorf("mvcc: truncated op in frame")
		}
		op := logOp{del: rest[0] == opDel}
		keyLen := binary.BigEndian.Uint32(rest[1:5])
		valLen := binary.BigEndian.Uint32(rest[5:9])
		rest = rest[9:]
		if uint32(len(rest)) < keyLen+valLen {
			return 0, nil, fmt.Errorf("mvcc: truncated kv in frame")
		}
		op.key = string(rest[:keyLen])
		op.value = append([]byte(nil), rest[keyLen:keyLen+valLen]...)
		rest = rest[keyLen+valLen:]
		ops = append(ops, op)
	}
	return version, ops, nil
}
