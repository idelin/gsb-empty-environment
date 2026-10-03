package saga

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store 负责状态机与审计轨迹的磁盘持久化。
//
// 目录布局：
//
//	<dir>/instances/<id>.json   实例状态快照（tmp 写入 + fsync + rename 原子替换）
//	<dir>/audit/<id>.log        审计轨迹（JSONL，只追加，写后 fsync）
type Store struct {
	dir string
	mu  sync.Mutex // 串行化文件写入
}

// NewStore 创建（必要时初始化）一个持久化存储。
func NewStore(dir string) (*Store, error) {
	for _, sub := range []string{"instances", "audit"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir}, nil
}

// SaveInstance 原子地持久化实例状态：先写临时文件并 fsync，再 rename 替换，
// 保证崩溃时要么看到旧状态、要么看到新状态，不会出现半截文件。
func (s *Store) SaveInstance(in *Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, "instances", in.ID+".json")
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadInstances 加载全部已持久化的实例（按创建时间排序）。
func (s *Store) LoadInstances() ([]*Instance, error) {
	entries, err := os.ReadDir(filepath.Join(s.dir, "instances"))
	if err != nil {
		return nil, err
	}
	var out []*Instance
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, "instances", e.Name()))
		if err != nil {
			return nil, err
		}
		var in Instance
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, fmt.Errorf("load instance %s: %w", e.Name(), err)
		}
		out = append(out, &in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// AppendAudit 向实例的审计日志追加一条状态迁移事件（写后 fsync）。
func (s *Store) AppendAudit(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(s.dir, "audit", ev.InstanceID+".log"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// AuditTrail 读取实例的完整审计轨迹。
func (s *Store) AuditTrail(instanceID string) ([]Event, error) {
	f, err := os.Open(filepath.Join(s.dir, "audit", instanceID+".log"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, sc.Err()
}
