package aries

import (
	"errors"
	"io"
	"os"
	"sync"
)

// ErrClosed 表示管理器已关闭。
var ErrClosed = errors.New("aries: closed")

// LogManager 管理 WAL 文件。LSN 即文件内字节偏移。
// 写路径直接 WriteAt(无用户态缓冲),Flush 负责 fsync,
// 因此"已追加未刷盘"的日志在崩溃后丢失,已 Flush 的保证持久。
type LogManager struct {
	mu         sync.Mutex
	file       *os.File
	nextLSN    LSN
	flushedLSN LSN
	closed     bool
}

// OpenLogManager 打开(或创建)WAL 文件。
func OpenLogManager(path string) (*LogManager, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	size := LSN(st.Size())
	return &LogManager{file: f, nextLSN: size, flushedLSN: size}, nil
}

// Append 追加一条记录并返回其 LSN。
func (lm *LogManager) Append(r *LogRecord) (LSN, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.closed {
		return 0, ErrClosed
	}
	data := r.encode()
	if _, err := lm.file.WriteAt(data, int64(lm.nextLSN)); err != nil {
		return 0, err
	}
	r.LSN = lm.nextLSN
	lm.nextLSN += LSN(len(data))
	return r.LSN, nil
}

// Flush 保证 upto(含)之前的日志全部落盘。
func (lm *LogManager) Flush(upto LSN) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.closed {
		return ErrClosed
	}
	if upto <= lm.flushedLSN {
		return nil
	}
	if err := lm.file.Sync(); err != nil {
		return err
	}
	lm.flushedLSN = lm.nextLSN
	return nil
}

// SyncAll 将全部已追加日志落盘。
func (lm *LogManager) SyncAll() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.closed {
		return ErrClosed
	}
	if lm.flushedLSN >= lm.nextLSN {
		return nil
	}
	if err := lm.file.Sync(); err != nil {
		return err
	}
	lm.flushedLSN = lm.nextLSN
	return nil
}

// FlushedLSN 返回已保证持久化的最大 LSN。
func (lm *LogManager) FlushedLSN() LSN {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.flushedLSN
}

// NextLSN 返回下一条记录将被分配的 LSN。
func (lm *LogManager) NextLSN() LSN {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.nextLSN
}

// ReadRecord 按 LSN 精确读取一条记录。
func (lm *LogManager) ReadRecord(lsn LSN) (*LogRecord, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lsn >= lm.nextLSN {
		return nil, io.EOF
	}
	rec, _, err := readRecordAt(lm.file, lsn)
	return rec, err
}

// LogIterator 顺序扫描日志。
type LogIterator struct {
	lm  *LogManager
	off LSN
}

// Iterator 返回从 start 开始的顺序迭代器。
func (lm *LogManager) Iterator(start LSN) *LogIterator {
	return &LogIterator{lm: lm, off: start}
}

// Next 返回下一条记录;日志结束时返回 io.EOF。
func (it *LogIterator) Next() (*LogRecord, error) {
	it.lm.mu.Lock()
	defer it.lm.mu.Unlock()
	if it.off >= it.lm.nextLSN {
		return nil, io.EOF
	}
	rec, n, err := readRecordAt(it.lm.file, it.off)
	if err != nil {
		return nil, err
	}
	it.off += LSN(n)
	return rec, nil
}

// Close 关闭日志文件(不隐式刷盘,调用方需先 Flush)。
func (lm *LogManager) Close() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.closed {
		return nil
	}
	lm.closed = true
	return lm.file.Close()
}
