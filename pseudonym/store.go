package pseudonym

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

// Store 是分配结果的追加式存储。
//
// 一次分配在互斥区内先写 binding，再依次写 alias，存储层按追加顺序
// 保证这些记录要么完整落盘、要么在崩溃时停在最后一条（重启时跳过
// 被截断的半条记录），从而让内存状态与已持久化的记录保持一致。
type Store interface {
	Append(entry Entry) error
}

// Loader 由支持重启恢复的存储实现。Load 按追加顺序回放全部记录，
// 第二个返回值是末尾被截断、尚未完整落盘的半行记录数（正常为 0）。
type Loader interface {
	Load() (entries []Entry, dropped int, err error)
}

// MemoryStore 仅保存在内存中，适用于测试和不需要重启恢复的进程。
type MemoryStore struct {
	mu      sync.Mutex
	entries []Entry
}

// NewMemoryStore 创建空的内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Append 实现 Store。
func (s *MemoryStore) Append(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry.clone())
	return nil
}

// Load 实现 Loader。
func (s *MemoryStore) Load() ([]Entry, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.entries))
	for i, entry := range s.entries {
		out[i] = entry.clone()
	}
	return out, 0, nil
}

// WALStore 以 JSON Lines 追加日志持久化分配记录。
//
// 每条 Entry 序列化为一行 JSON 并 fsync。进程重启时逐行回放；
// 末尾没有换行符的半行视为崩溃时被截断的写入，直接丢弃，
// 中间出现无法解析的行则返回 ErrCorrupt，避免静默丢失已提交数据。
type WALStore struct {
	mu sync.Mutex
	f  *os.File
}

// OpenWAL 打开（不存在则创建）追加日志并回放已有内容。
// 返回的 dropped 是因末尾截断而丢弃的半行记录数，正常情况下为 0。
func OpenWAL(path string) (*WALStore, []Entry, int, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, 0, err
	}
	store := &WALStore{f: f}
	entries, dropped, err := store.Load()
	if err != nil {
		f.Close()
		return nil, nil, 0, err
	}
	return store, entries, dropped, nil
}

// Load 实现 Loader：读取并回放当前日志中的全部已提交记录。
// 末尾缺换行的半行视为崩溃时未提交的写入，计入 dropped 并跳过。
func (s *WALStore) Load() ([]Entry, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}
	entries, dropped, err := readWAL(s.f)
	if err != nil {
		return nil, 0, err
	}
	if _, err := s.f.Seek(0, io.SeekEnd); err != nil {
		return nil, 0, err
	}
	return entries, dropped, nil
}

// Append 实现 Store：序列化、追加、fsync，保证记录在返回前持久化。
func (s *WALStore) Append(entry Entry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.f.Write(data); err != nil {
		return err
	}
	return s.f.Sync()
}

// Close 关闭底层文件。
func (s *WALStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}

func readWAL(r io.Reader) (entries []Entry, dropped int, err error) {
	// 只有以换行符结尾的行才算已提交记录：写入端保证每条记录都带换行，
	// 崩溃可能留下缺换行的半行，那部分一律按未提交处理。
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}

	lines := bytes.Split(data, []byte("\n"))
	// 最后一个元素是末尾换行之后的空串；若它非空说明最后一行缺换行。
	last := lines[len(lines)-1]
	committed := lines[:len(lines)-1]
	if len(last) > 0 {
		dropped = 1
	}

	for _, line := range committed {
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if jsonErr := json.Unmarshal(line, &entry); jsonErr != nil {
			return nil, 0, ErrCorrupt
		}
		entries = append(entries, entry)
	}
	return entries, dropped, nil
}

// ErrCorrupt 表示持久化日志中存在无法解析的已提交记录。
var ErrCorrupt = errors.New("pseudonym: corrupt write-ahead log")
