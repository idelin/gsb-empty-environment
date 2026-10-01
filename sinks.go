package splitter

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ColdStorage 冷存储（物理归档库）的内存实现：只追加，接收归档 Insert。
type ColdStorage struct {
	mu       sync.Mutex
	Packages []Event // 已接收的归档包
}

func (c *ColdStorage) Name() string { return "cold-storage" }

func (c *ColdStorage) Apply(_ context.Context, e Event) error {
	if e.Type != EventInsert {
		return fmt.Errorf("cold-storage: 只接受归档 Insert，收到 %s", e.Type)
	}
	c.mu.Lock()
	c.Packages = append(c.Packages, e)
	c.mu.Unlock()
	return nil
}

// SearchIndex 搜索索引的内存实现：按 ID 存文档，接收 Delete 指令。
type SearchIndex struct {
	mu   sync.Mutex
	docs map[string]map[int64]Row // table -> id -> doc
}

func NewSearchIndex() *SearchIndex {
	return &SearchIndex{docs: map[string]map[int64]Row{}}
}

func (s *SearchIndex) Name() string { return "search-index" }

// Index 预置索引文档（模拟业务正常运行时写入的索引）。
func (s *SearchIndex) Index(table string, id int64, doc Row) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.docs[table] == nil {
		s.docs[table] = map[int64]Row{}
	}
	s.docs[table][id] = doc
}

// Has 查询索引中是否还存在该文档。
func (s *SearchIndex) Has(table string, id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.docs[table][id]
	return ok
}

func (s *SearchIndex) Apply(_ context.Context, e Event) error {
	if e.Type != EventDelete {
		return fmt.Errorf("search-index: 只接受 Delete 指令，收到 %s", e.Type)
	}
	s.mu.Lock()
	delete(s.docs[e.Table], e.ID)
	s.mu.Unlock()
	return nil
}

// FormatRow 以稳定顺序格式化一行数据，便于日志与测试断言。
func FormatRow(r Row) string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, r[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
