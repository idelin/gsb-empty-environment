package lsm

import (
	"bufio"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"sync"
)

// WAL 记录类型。
const (
	recordTypePut byte = 1
	recordTypeDel byte = 2
)

// 记录头布局: crc32(4) | type(1) | keyLen(4) | valueLen(4)，随后是 key 与 value。
// crc 覆盖 type+len+key+value，用于识别崩溃造成的撕裂写(torn tail)。
const walHeaderSize = 4 + 1 + 4 + 4

const maxRecordPayload = 1 << 30 // 防御性上限，超出视为损坏

// wal 是追加写的预写日志段。每个 MemTable 对应一个独立的 WAL 段，
// 轮转时旧段随 Immutable MemTable 一起刷盘，刷盘完成后旧段被安全删除。
type wal struct {
	mu   sync.Mutex
	f    *os.File
	path string
	size int64
	buf  []byte // 复用的编码缓冲
}

func createWAL(path string) (*wal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &wal{f: f, path: path}, nil
}

func openWALAppend(path string) (*wal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &wal{f: f, path: path, size: fi.Size()}, nil
}

// append 将一条记录编码后单次 write 落盘(进入 OS 页缓存)。
// 不做逐条 fsync，段级 fsync 在轮转与关闭时进行，以兼顾吞吐。
func (w *wal) append(recType byte, key, value []byte) error {
	n := walHeaderSize + len(key) + len(value)
	w.mu.Lock()
	defer w.mu.Unlock()
	if cap(w.buf) < n {
		w.buf = make([]byte, n)
	}
	buf := w.buf[:n]
	buf[4] = recType
	binary.LittleEndian.PutUint32(buf[5:9], uint32(len(key)))
	binary.LittleEndian.PutUint32(buf[9:13], uint32(len(value)))
	crc := crc32.NewIEEE()
	crc.Write(buf[4:walHeaderSize])
	crc.Write(key)
	crc.Write(value)
	binary.LittleEndian.PutUint32(buf[0:4], crc.Sum32())
	copy(buf[walHeaderSize:], key)
	copy(buf[walHeaderSize+len(key):], value)
	if _, err := w.f.Write(buf); err != nil {
		return err
	}
	w.size += int64(n)
	return nil
}

// sync 将段数据 fsync 到磁盘。轮转与关闭前调用，保证 WAL 安全轮转。
func (w *wal) sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Sync()
}

func (w *wal) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

// length 返回段内已写入的字节数。
func (w *wal) length() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

// replayWAL 顺序回放一个 WAL 段，对每条完整记录调用 apply。
// 遇到 CRC 校验失败或截断的尾部记录时停止(视为崩溃点)，已回放数据不受影响。
func replayWAL(path string, apply func(recType byte, key, value []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var hdr [walHeaderSize]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil // EOF 或截断头: 正常结束
		}
		recType := hdr[4]
		klen := binary.LittleEndian.Uint32(hdr[5:9])
		vlen := binary.LittleEndian.Uint32(hdr[9:13])
		if klen > maxRecordPayload || vlen > maxRecordPayload {
			return nil // 长度域损坏
		}
		payload := make([]byte, int(klen)+int(vlen))
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil // 撕裂的尾部记录
		}
		crc := crc32.NewIEEE()
		crc.Write(hdr[4:])
		crc.Write(payload)
		if crc.Sum32() != binary.LittleEndian.Uint32(hdr[0:4]) {
			return nil // CRC 校验失败
		}
		apply(recType, payload[:klen], payload[klen:])
	}
}
