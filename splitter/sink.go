package splitter

import (
	"context"
	"fmt"
	"sync"
)

// Sink 抽象一个异构下游存储系统（归档库、只读查询库、搜索索引、冷存储等）。
type Sink interface {
	// Name 返回存储系统的标识，用于路由与日志。
	Name() string
	// Apply 将一条事件应用到该存储系统。
	Apply(ctx context.Context, e Event) error
}

// Router 按路由名把事件分发到不同的异构存储系统。
type Router struct {
	mu    sync.RWMutex
	sinks map[string]Sink
}

// NewRouter 创建空路由器。
func NewRouter() *Router {
	return &Router{sinks: make(map[string]Sink)}
}

// Register 将存储系统注册到指定路由名下。
func (r *Router) Register(route string, s Sink) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sinks[route] = s
}

// Dispatch 把事件发送到指定路由对应的存储系统。
func (r *Router) Dispatch(ctx context.Context, route string, e Event) error {
	r.mu.RLock()
	s, ok := r.sinks[route]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("splitter: 未注册的路由 %q", route)
	}
	return s.Apply(ctx, e)
}

// 预定义路由名。
const (
	RouteArchive     = "archive"     // 物理归档库 / 冷存储
	RouteQuery       = "query"       // 只读查询库 / 搜索索引
	RoutePassthrough = "passthrough" // 非逻辑删除事件的透传下游
)

// ---- 以下为基于标准库的示例异构存储实现 ----

// ColdStorage 冷存储 / 物理归档库：只追加保存归档包（完整历史数据）。
type ColdStorage struct {
	mu       sync.Mutex
	Packages []map[string]any
}

func (c *ColdStorage) Name() string { return "cold-storage" }

func (c *ColdStorage) Apply(_ context.Context, e Event) error {
	if e.Op != OpInsert {
		return fmt.Errorf("cold-storage: 仅接受归档 Insert 事件, 收到 %s", e.Op)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Packages = append(c.Packages, e.FullImage())
	return nil
}

// SearchIndex 搜索索引 / 只读查询库：维护可查询的存活主键集合。
type SearchIndex struct {
	mu    sync.Mutex
	alive map[any]map[string]any
}

// NewSearchIndex 创建搜索索引，可选地预置一批文档。
func NewSearchIndex(docs ...map[string]any) *SearchIndex {
	s := &SearchIndex{alive: make(map[any]map[string]any)}
	for _, d := range docs {
		s.alive[d["id"]] = d
	}
	return s
}

func (s *SearchIndex) Name() string { return "search-index" }

func (s *SearchIndex) Apply(_ context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e.Op {
	case OpDelete:
		delete(s.alive, e.Key)
	case OpInsert, OpUpdate:
		s.alive[e.Key] = e.FullImage()
	}
	return nil
}

// Contains 查询主键是否仍存在于索引中。
func (s *SearchIndex) Contains(key any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.alive[key]
	return ok
}

// RecordingSink 透传下游：记录所有收到的事件，便于测试与审计。
type RecordingSink struct {
	mu     sync.Mutex
	Events []Event
}

func (r *RecordingSink) Name() string { return "passthrough" }

func (r *RecordingSink) Apply(_ context.Context, e Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, e)
	return nil
}
