// Package store 实现带 fencing token 校验的 KV 存储。
//
// 存储层记录所见过的最大 Epoch,任何携带更小(或无效)Epoch 的写入
// 都会被拒绝。这样即使旧主机房在失联期间仍以为自己可写,其写请求
// 到达存储层时也会因 fencing token 过时被拒,从机制上杜绝双写。
package store

import (
	"errors"
	"fmt"
	"sync"
)

// ErrStaleFencingToken 表示写请求携带的 fencing token 已过时。
var ErrStaleFencingToken = errors.New("fencing token 已过时,拒绝写入")

// Store 是线程安全的 KV 存储,写入必须携带单调递增的 fencing token。
type Store struct {
	mu        sync.Mutex
	data      map[string]string
	lastEpoch uint64
}

// New 创建空存储。
func New() *Store {
	return &Store{data: make(map[string]string)}
}

// Put 以 epoch 作为 fencing token 写入 key=value。
// epoch 必须大于等于存储层已见过的最大 epoch 且非 0,否则返回 ErrStaleFencingToken。
func (s *Store) Put(epoch uint64, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch == 0 || epoch < s.lastEpoch {
		return fmt.Errorf("%w: epoch=%d, 存储层已见 epoch=%d", ErrStaleFencingToken, epoch, s.lastEpoch)
	}
	s.lastEpoch = epoch
	s.data[key] = value
	return nil
}

// Get 读取 key;不存在时 ok=false。
func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return v, ok
}

// LastEpoch 返回存储层已见过的最大 fencing token。
func (s *Store) LastEpoch() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEpoch
}

// Len 返回键值对数量。
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}
