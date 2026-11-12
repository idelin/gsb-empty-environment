package netsec

import (
	"bufio"
	"sync"
)

// ScriptStore 是服务端脚本存储的最小抽象。
// 实现方需保证 Put 对同名脚本是整体替换，且校验阶段不触发任何写入。
type ScriptStore interface {
	// Put 保存（或整体覆盖）一个已通过全部校验的脚本。
	Put(name string, body []byte) error
	// Get 返回当前已保存的脚本体；不存在时返回 (nil, false, nil)。
	Get(name string) ([]byte, bool, error)
}

// MemoryStore 是用于服务集成与测试的内存实现，自带互斥锁。
type MemoryStore struct {
	mu      sync.RWMutex
	scripts map[string][]byte
}

// NewMemoryStore 创建空的内存脚本存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{scripts: make(map[string][]byte)}
}

// Put 整体替换脚本；body 会被拷贝，避免调用方后续切片修改。
func (s *MemoryStore) Put(name string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := make([]byte, len(body))
	copy(stored, body)
	s.scripts[name] = stored
	return nil
}

// Get 返回脚本副本，不存在时 ok 为 false。
func (s *MemoryStore) Get(name string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	body, ok := s.scripts[name]
	if !ok {
		return nil, false, nil
	}
	out := make([]byte, len(body))
	copy(out, body)
	return out, true, nil
}

// HandlePutScript 是 PUTSCRIPT 处理的完整入口：
//
//  1. 从连接精确解析命令与字面量（ReadPutScript）；
//  2. 对脚本体做 Sieve 内容校验（ValidateScript）；
//  3. 只有两步全部成功才写入存储。
//
// 任何一步失败都直接返回错误，存储层完全不被触碰，因此同名旧脚本
// 不会被部分写入或空内容覆盖。
func HandlePutScript(r *bufio.Reader, store ScriptStore, rules Rules) (*ValidatedScript, error) {
	parsed, err := ReadPutScript(r, rules)
	if err != nil {
		return nil, err
	}
	// 先校验后落盘：校验失败时绝不调用 store.Put。
	if err := ValidateScript(parsed.Body, rules); err != nil {
		return nil, err
	}
	if err := store.Put(parsed.Name, parsed.Body); err != nil {
		return nil, err
	}
	return parsed, nil
}
