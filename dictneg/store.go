package dictneg

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Store 抽象协商状态的持久化。
//
// 一次协议角色只需要其中一类状态：发送端用 ProposerState，接收端用
// AcceptorState；同一进程同时承担两个方向时，两个角色应使用不同的
// name 空间，避免互相覆盖。所有写方法都必须在返回前落盘（崩溃安全），
// 这样重连与掉电恢复才能沿用“已持久确认”的版本。
type Store interface {
	// PutDictionary 持久化字典内容。相同摘要重复写入必须幂等。
	PutDictionary(digest Digest, content []byte) error
	// GetDictionary 读取字典内容；不存在返回 ErrUnknownDictionary。
	GetDictionary(digest Digest) ([]byte, error)
	// HasDictionary 报告字典是否已持久化。
	HasDictionary(digest Digest) bool

	// SaveAcceptorState 原子覆盖接收端状态；nil 表示清空。
	SaveAcceptorState(name string, state *AcceptorState) error
	// LoadAcceptorState 读取接收端状态；从未保存时返回 nil, nil。
	LoadAcceptorState(name string) (*AcceptorState, error)

	// SaveProposerState 原子覆盖发送端状态；nil 表示清空。
	SaveProposerState(name string, state *ProposerState) error
	// LoadProposerState 读取发送端状态；从未保存时返回 nil, nil。
	LoadProposerState(name string) (*ProposerState, error)
}

// AcceptorState 是接收端需要持久化的全部协商状态。
type AcceptorState struct {
	// Epoch 是已处理过的最大事件 Epoch，用于丢弃过期重放。
	Epoch uint64 `json:"epoch"`
	// EnabledEpoch 是当前活动字典被启用时的事件 Epoch；同 Epoch 出现
	// 不同摘要的第二个 Enable 属于冲突重放，必须拒绝。
	EnabledEpoch uint64 `json:"enabled_epoch,omitempty"`
	// Active 是当前启用（用于解码）的字典摘要；零值表示尚无字典。
	Active Digest `json:"active"`
	// Installed 是已落盘但未必启用的字典摘要集合。
	Installed []Digest `json:"installed,omitempty"`
	// NextSeq 按摘要记录每个流期望交付的下一个序号，重连后保持顺序。
	NextSeq map[string]uint64 `json:"next_seq,omitempty"`
}

// ProposerState 是发送端需要持久化的全部协商状态。
type ProposerState struct {
	// Epoch 是最近一次安装尝试的 Epoch。
	Epoch uint64 `json:"epoch"`
	// Active 是已确认启用、当前用于编码的字典摘要。
	Active Digest `json:"active"`
	// ActiveConfirmed 记录接收端是否已确认启用事件；未确认时重连
	// 需要重放安装与启用链，防止启用事件丢失导致两端错位。
	ActiveConfirmed bool `json:"active_confirmed,omitempty"`
	// Pending 是已发起安装但尚未完成启用的字典；零值表示没有在途安装。
	Pending PendingInstall `json:"pending"`
}

// PendingInstall 描述一次在途安装。
type PendingInstall struct {
	Digest    Digest `json:"digest"`
	Confirmed bool   `json:"confirmed"` // 是否已收到并持久化 Ack
}

// IsZero 报告是否没有在途安装。
func (p PendingInstall) IsZero() bool {
	return p.Digest.IsZero()
}

// FileStore 把状态写入目录，所有更新采用“临时文件 + 原子改名”，
// 进程在任意写入点掉电后，目录里只可能出现旧版本或新版本，不会出现
// 半写状态。
type FileStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileStore 打开（必要时创建）一个基于目录的 Store。
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) dictPath(d Digest) string {
	return filepath.Join(s.dir, "dict-"+d.String()+".bin")
}

func (s *FileStore) statePath(kind, name string) string {
	return filepath.Join(s.dir, kind+"-"+sanitizeName(name)+".json")
}

// PutDictionary 实现 Store。
func (s *FileStore) PutDictionary(d Digest, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	final := s.dictPath(d)
	if _, err := os.Stat(final); err == nil {
		return nil // 幂等：相同摘要的内容必然相同
	}
	return atomicWriteFile(final, content, 0o600)
}

// GetDictionary 实现 Store。
func (s *FileStore) GetDictionary(d Digest) ([]byte, error) {
	data, err := os.ReadFile(s.dictPath(d))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrUnknownDictionary
	}
	return data, err
}

// HasDictionary 实现 Store。
func (s *FileStore) HasDictionary(d Digest) bool {
	_, err := os.Stat(s.dictPath(d))
	return err == nil
}

// SaveAcceptorState 实现 Store。
func (s *FileStore) SaveAcceptorState(name string, state *AcceptorState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return atomicWriteJSON(s.statePath("acceptor", name), state)
}

// LoadAcceptorState 实现 Store。
func (s *FileStore) LoadAcceptorState(name string) (*AcceptorState, error) {
	var state AcceptorState
	found, err := readJSON(s.statePath("acceptor", name), &state)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	if state.NextSeq == nil {
		state.NextSeq = map[string]uint64{}
	}
	return &state, nil
}

// SaveProposerState 实现 Store。
func (s *FileStore) SaveProposerState(name string, state *ProposerState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return atomicWriteJSON(s.statePath("proposer", name), state)
}

// LoadProposerState 实现 Store。
func (s *FileStore) LoadProposerState(name string) (*ProposerState, error) {
	var state ProposerState
	found, err := readJSON(s.statePath("proposer", name), &state)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &state, nil
}

func atomicWriteJSON(path string, v any) error {
	if v == nil {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data, 0o600)
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, err
	}
	return true, nil
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// sanitizeName 把角色名压成安全的文件名片段。
func sanitizeName(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-' || c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}
