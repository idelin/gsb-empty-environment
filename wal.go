package diskqueue

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// WAL 记录类型。
const (
	recEnqueue   byte = 1
	recUpdate    byte = 2
	recDelete    byte = 3
	recPopCommit byte = 4
)

const maxRecordSize = 64 << 20

// 帧格式: uint32 bodyLen | type(1) | payload | crc32(type+payload)(4)
// bodyLen 覆盖 type+payload+crc。CRC 用于识别断电/崩溃造成的撕裂尾帧。

func appendRecord(f *os.File, typ byte, payload []byte) error {
	bodyLen := 1 + len(payload) + 4
	buf := make([]byte, 4+bodyLen)
	binary.LittleEndian.PutUint32(buf, uint32(bodyLen))
	buf[4] = typ
	copy(buf[5:], payload)
	crc := crc32.ChecksumIEEE(buf[4 : 5+len(payload)])
	binary.LittleEndian.PutUint32(buf[5+len(payload):], crc)
	for len(buf) > 0 {
		n, err := f.Write(buf)
		if err != nil {
			return err
		}
		buf = buf[n:]
	}
	return nil
}

// replayWAL 顺序回放 WAL，返回最后一个完好帧的结束偏移。
// 遇到撕裂/损坏的尾部即停止，由调用方截断。
func replayWAL(f *os.File, apply func(typ byte, payload []byte) error) (int64, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	var hdr [4]byte
	var offset int64
	for {
		_, err := io.ReadFull(f, hdr[:])
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return offset, nil // 干净结尾或撕裂的头
		}
		if err != nil {
			return 0, err
		}
		bodyLen := binary.LittleEndian.Uint32(hdr[:])
		if bodyLen < 5 || bodyLen > maxRecordSize {
			return offset, nil // 长度损坏，视为撕裂尾
		}
		body := make([]byte, bodyLen)
		if _, err := io.ReadFull(f, body); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return offset, nil // 撕裂的帧体
			}
			return 0, err
		}
		end := len(body) - 4
		if crc32.ChecksumIEEE(body[:end]) != binary.LittleEndian.Uint32(body[end:]) {
			return offset, nil // CRC 不匹配，视为撕裂尾
		}
		if err := apply(body[0], body[1:end]); err != nil {
			return 0, err
		}
		offset += int64(4 + bodyLen)
	}
}

// ---- 记录编解码 ----

func encodeEnqueue(seq uint64, prio int64, payload []byte) []byte {
	b := make([]byte, 8+8+4+len(payload))
	binary.LittleEndian.PutUint64(b[0:], seq)
	binary.LittleEndian.PutUint64(b[8:], uint64(prio))
	binary.LittleEndian.PutUint32(b[16:], uint32(len(payload)))
	copy(b[20:], payload)
	return b
}

func decodeEnqueue(b []byte) (seq uint64, prio int64, payload []byte, err error) {
	if len(b) < 20 {
		return 0, 0, nil, fmt.Errorf("enqueue record too short: %d", len(b))
	}
	seq = binary.LittleEndian.Uint64(b[0:])
	prio = int64(binary.LittleEndian.Uint64(b[8:]))
	n := binary.LittleEndian.Uint32(b[16:])
	if uint64(len(b)-20) < uint64(n) {
		return 0, 0, nil, fmt.Errorf("enqueue payload truncated")
	}
	payload = append([]byte(nil), b[20:20+n]...)
	return seq, prio, payload, nil
}

func encodeUpdate(seq uint64, prio int64) []byte {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint64(b[0:], seq)
	binary.LittleEndian.PutUint64(b[8:], uint64(prio))
	return b
}

func decodeUpdate(b []byte) (seq uint64, prio int64, err error) {
	if len(b) != 16 {
		return 0, 0, fmt.Errorf("bad update record length: %d", len(b))
	}
	return binary.LittleEndian.Uint64(b[0:]), int64(binary.LittleEndian.Uint64(b[8:])), nil
}

func encodeSeq(seq uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, seq)
	return b
}

func decodeSeq(b []byte) (uint64, error) {
	if len(b) != 8 {
		return 0, fmt.Errorf("bad seq record length: %d", len(b))
	}
	return binary.LittleEndian.Uint64(b), nil
}

func encodePopCommit(seqs []uint64) []byte {
	b := make([]byte, 4+8*len(seqs))
	binary.LittleEndian.PutUint32(b[0:], uint32(len(seqs)))
	for i, s := range seqs {
		binary.LittleEndian.PutUint64(b[4+8*i:], s)
	}
	return b
}

func decodePopCommit(b []byte) ([]uint64, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("pop commit record too short")
	}
	n := binary.LittleEndian.Uint32(b[0:])
	if uint64(len(b)-4) != uint64(n)*8 {
		return nil, fmt.Errorf("bad pop commit record length")
	}
	seqs := make([]uint64, n)
	for i := range seqs {
		seqs[i] = binary.LittleEndian.Uint64(b[4+8*i:])
	}
	return seqs, nil
}
