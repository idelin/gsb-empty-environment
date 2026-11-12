package netsec

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Receipt 是一次成功签发后落入普通存储的回执。
// 存储本身是不可信的：它只是便于审计与排查的副本日志，
// 额度是否用尽只由可信单调计数器判定。
type Receipt struct {
	// Seq 是本次签发消耗到的计数值，从 1 开始、全局唯一且单调。
	Seq uint64 `json:"seq"`
	// Artifact 是被签发的产物摘要/载荷，采用标准 base64 编码。
	Artifact string `json:"artifact"`
	// Signature 是签名器对产物产出的签名，采用标准 base64 编码。
	Signature string `json:"signature"`
}

// Store 是保存在普通（可克隆）磁盘上的签发日志。
// 它不承担任何额度判定职责，因此被回滚或克隆都不会扩大签发额度。
type Store interface {
	// Append 追加一条成功签发的回执。
	Append(receipt Receipt) error
	// List 返回按追加顺序排列的全部回执。
	List() ([]Receipt, error)
	// Close 释放底层资源。
	Close() error
}

// FileStore 把回执以 JSON Lines（每行一条 JSON）的方式追加到普通文件。
// 整个目录可以像普通磁盘一样被快照、克隆与回滚，不包含任何受信状态。
type FileStore struct {
	mu sync.Mutex
	f  *os.File
	w  *bufio.Writer
	p  string
}

// NewFileStore 打开（必要时创建）dir 下的 append-only 回执日志。
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("netsec: create store dir: %w", err)
	}
	p := filepath.Join(dir, "receipts.jsonl")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("netsec: open store file: %w", err)
	}
	return &FileStore{
		f: f,
		w: bufio.NewWriter(f),
		p: p,
	}, nil
}

// Append 以 JSON Lines 形式追加一条回执并 fsync 落盘。
func (s *FileStore) Append(receipt Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("netsec: marshal receipt: %w", err)
	}
	if _, err := s.w.Write(data); err != nil {
		return fmt.Errorf("netsec: write receipt: %w", err)
	}
	if err := s.w.WriteByte('\n'); err != nil {
		return fmt.Errorf("netsec: write receipt: %w", err)
	}
	if err := s.w.Flush(); err != nil {
		return fmt.Errorf("netsec: flush receipt: %w", err)
	}
	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("netsec: sync receipt: %w", err)
	}
	return nil
}

// List 从头读取全部回执。
func (s *FileStore) List() ([]Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.p)
	if err != nil {
		return nil, fmt.Errorf("netsec: reopen store file: %w", err)
	}
	defer f.Close()

	var receipts []Receipt
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var receipt Receipt
		if err := json.Unmarshal(line, &receipt); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCorruptStore, err)
		}
		receipts = append(receipts, receipt)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("netsec: read store file: %w", err)
	}
	return receipts, nil
}

// Close 关闭底层文件。
func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}

// EncodeArtifact 将任意产物字节编码为回执中的 base64 字符串。
func EncodeArtifact(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeArtifact 解码回执中的 base64 产物字段。
func DecodeArtifact(s string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("netsec: decode artifact: %w", err)
	}
	return decoded, nil
}

// ErrCorruptStore 表示存储内容无法解析。
var ErrCorruptStore = errors.New("netsec: corrupt store")
