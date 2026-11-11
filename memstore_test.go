package cursorpage_test

import (
	"context"
	"sync"

	cp "example.org/cursorpage"
)

// memStore 是测试用的内存断点存储。Save 拷贝后整体替换，
// 语义上与 FileStore 的原子写入等价：Load 永远读到完整的某一份状态。
type memStore struct {
	mu sync.Mutex
	cp cp.Checkpoint
	ok bool
}

func (m *memStore) Save(_ context.Context, in cp.Checkpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clone := in
	if in.Delivered != nil {
		clone.Delivered = make(map[string]int64, len(in.Delivered))
		for k, v := range in.Delivered {
			clone.Delivered[k] = v
		}
	}
	m.cp = clone
	m.ok = true
	return nil
}

func (m *memStore) Load(_ context.Context) (cp.Checkpoint, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ok {
		return cp.Checkpoint{}, false, nil
	}
	out := m.cp
	if m.cp.Delivered != nil {
		out.Delivered = make(map[string]int64, len(m.cp.Delivered))
		for k, v := range m.cp.Delivered {
			out.Delivered[k] = v
		}
	}
	return out, true, nil
}
