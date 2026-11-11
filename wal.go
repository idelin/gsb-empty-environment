package repartition

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

// walName 是组件在数据目录中使用的预写日志文件名。
const walName = "repartition.wal"

// frame 是 WAL 中的一条持久化记录。
//
// 磁盘布局（小端序）：

//	magic   [4]byte  固定为 "RPWL"
//	length  uint32   JSON 负载长度
//	crc     uint32   负载的 CRC32（IEEE）
//	payload []byte   length 字节的 JSON 编码 frame
//	check   [32]byte 整个 frame（magic+length+crc+payload）的 SHA-256
//
// 末尾的 SHA-256 让重启时可以识别半写（torn write）：任何不完整或损坏的
// 帧都会被截断，保证日志只在帧边界处生效。
type frame struct {
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

const (
	frameMagic = "RPWL"
	frameHead  = 4 + 4 + 4   // magic + length + crc
	frameTail  = sha256.Size // 末尾校验和
	frameLimit = 16 * 1024 * 1024
)

// wal 是只追加的预写日志。所有状态变更在对消费者可见之前都已 fsync 落盘。
type wal struct {
	f       *os.File
	w       *bufio.Writer
	dir     string
	appends int // 自本进程打开以来成功追加的帧数（用于故障注入定位）
}

// openWAL 打开（必要时创建）数据目录中的日志，并把完整帧回放给 visit。
// 遇到损坏/半写的尾部时自动截断到最后一个完整帧，实现“任意一次写盘后重启”。
func openWAL(dir string, visit func(fr frame) error) (*wal, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("repartition: 创建数据目录失败: %w", err)
	}
	path := filepath.Join(dir, walName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("repartition: 打开 WAL 失败: %w", err)
	}

	goodBytes, err := replayWAL(f, visit)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Truncate(int64(goodBytes)); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("repartition: 截断损坏尾部失败: %w", err)
	}
	if _, err := f.Seek(int64(goodBytes), io.SeekStart); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &wal{f: f, w: bufio.NewWriter(f), dir: dir}, nil
}

// replayWAL 顺序解析完整帧，返回最后一个完整帧之后的字节偏移。
func replayWAL(f *os.File, visit func(fr frame) error) (int, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	r := bufio.NewReader(f)
	offset := 0
	for {
		head := make([]byte, frameHead)
		n, err := io.ReadFull(r, head)
		if errors.Is(err, io.EOF) {
			return offset, nil // 干净的文件结尾
		}
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrShortBuffer) {
			return offset, nil // 半写的帧头，截断
		}
		if err != nil {
			return 0, err
		}
		if !bytes.Equal(head[:4], []byte(frameMagic)) {
			return 0, fmt.Errorf("repartition: WAL 魔数损坏，偏移 %d", offset)
		}
		length := int(binary.LittleEndian.Uint32(head[4:8]))
		if length <= 0 || length > frameLimit {
			return 0, fmt.Errorf("repartition: WAL 帧长度非法 %d，偏移 %d", length, offset)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrShortBuffer) {
			return offset, nil // 半写的负载，截断
		} else if err != nil {
			return 0, err
		}
		tail := make([]byte, frameTail)
		if _, err := io.ReadFull(r, tail); errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrShortBuffer) {
			return offset, nil // 半写的校验和，截断
		} else if err != nil {
			return 0, err
		}

		whole := make([]byte, 0, frameHead+length)
		whole = append(whole, head...)
		whole = append(whole, payload...)
		sum := sha256.Sum256(whole)
		if !bytes.Equal(sum[:], tail) {
			return offset, nil // 校验和不匹配，截断损坏帧
		}
		wantCRC := binary.LittleEndian.Uint32(head[8:12])
		if crc32.ChecksumIEEE(payload) != wantCRC {
			return offset, nil // CRC 不匹配，截断损坏帧
		}

		var fr frame
		if err := json.Unmarshal(payload, &fr); err != nil {
			return offset, nil // 负载无法解析，按损坏帧截断
		}
		if err := visit(fr); err != nil {
			return 0, err
		}
		offset += frameHead + length + frameTail
		_ = n
	}
}

// append 编码并落盘一帧。fault 非 nil 时在配置的位置注入写盘故障，
// 模拟进程在 fsync 之前/之后崩溃；故障后该 WAL 实例进入中毒状态，
// 调用方必须丢弃它并重新 Open，模拟真实崩溃后的重启。
func (l *wal) append(kind string, body any, fault *FaultConfig) error {
	if l == nil || l.f == nil {
		return ErrClosed
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("repartition: 编码事件失败: %w", err)
	}
	fr, err := json.Marshal(frame{Kind: kind, Body: raw})
	if err != nil {
		return err
	}
	if len(fr) > frameLimit {
		return fmt.Errorf("repartition: 帧超过大小上限 %d", frameLimit)
	}

	head := make([]byte, frameHead)
	copy(head[:4], frameMagic)
	binary.LittleEndian.PutUint32(head[4:8], uint32(len(fr)))
	binary.LittleEndian.PutUint32(head[8:12], crc32.ChecksumIEEE(fr))
	checksum := sha256.Sum256(append(append([]byte{}, head...), fr...))

	buf := make([]byte, 0, frameHead+len(fr)+frameTail)
	buf = append(buf, head...)
	buf = append(buf, fr...)
	buf = append(buf, checksum[:]...)

	nextAppend := l.appends + 1
	if fault != nil && fault.AtAppend == nextAppend {
		if fault.BeforeSync {
			// 只写入部分字节且不 fsync：模拟写到一半断电，产生半写尾部。
			partial := len(buf) / 2
			if _, err := l.f.Write(buf[:partial]); err != nil {
				return err
			}
			l.poison()
			return fault.Err
		}
		// 完整写入并 fsync 后再“崩溃”：该帧在重启回放时必须生效。
		if _, err := l.w.Write(buf); err != nil {
			return err
		}
		if err := l.w.Flush(); err != nil {
			return err
		}
		if err := l.f.Sync(); err != nil {
			return err
		}
		l.appends = nextAppend
		l.poison()
		return fault.Err
	}

	if _, err := l.w.Write(buf); err != nil {
		return err
	}
	if err := l.w.Flush(); err != nil {
		return err
	}
	if err := l.f.Sync(); err != nil {
		return err
	}
	l.appends = nextAppend
	return nil
}

func (l *wal) poison() { _ = l.f.Close(); l.f = nil; l.w = nil }

func (l *wal) close() error {
	if l == nil || l.f == nil {
		return nil
	}
	if err := l.w.Flush(); err != nil {
		_ = l.f.Close()
		return err
	}
	if err := l.f.Sync(); err != nil {
		_ = l.f.Close()
		return err
	}
	err := l.f.Close()
	l.f = nil
	l.w = nil
	return err
}
