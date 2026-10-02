// Package rulecache 提供带缓存失效传播的规则结果计算器。
//
// 规则输入来自外部维度表；引擎在规则求值期间记录其读取过的维度键，
// 维护"维度键 -> 缓存项"的精确反向索引。维度数据更新时，仅定向剔除
// 受影响的缓存项，其余缓存保持热态。
package rulecache

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// DimRef 唯一标识一个维度键（维度表 + 主键）。
type DimRef struct {
	Table string
	Key   string
}

// RuleFunc 是规则计算函数。通过 tx 读取维度数据，读取行为会被记录，
// 用于建立缓存项与维度键之间的依赖关系。
type RuleFunc func(tx *ReadTx, params map[string]string) (interface{}, error)

// ReadTx 是规则求值期间的维度读取上下文，自动记录读依赖。
type ReadTx struct {
	engine *Engine
	reads  map[DimRef]struct{}
}

// Get 读取维度表 table 中主键 key 的字段 field，并记录该依赖。
func (tx *ReadTx) Get(table, key, field string) (string, bool) {
	ref := DimRef{Table: table, Key: key}
	tx.reads[ref] = struct{}{}
	row, ok := tx.engine.dims[ref]
	if !ok {
		return "", false
	}
	v, ok := row[field]
	return v, ok
}

type cacheEntry struct {
	result interface{}
	err    error
}

// Engine 是规则结果计算器 + 缓存失效控制器。
type Engine struct {
	mu    sync.RWMutex
	dims  map[DimRef]map[string]string // 维度数据
	rules map[string]RuleFunc          // 已注册规则

	cache map[string]*cacheEntry // cacheKey -> 结果
	deps  map[string][]DimRef    // cacheKey -> 依赖的维度键（正排）
	index map[DimRef]map[string]struct{}

	hits   int64
	misses int64
}

// New 创建引擎。
func New() *Engine {
	return &Engine{
		dims:  make(map[DimRef]map[string]string),
		rules: make(map[string]RuleFunc),
		cache: make(map[string]*cacheEntry),
		deps:  make(map[string][]DimRef),
		index: make(map[DimRef]map[string]struct{}),
	}
}

// RegisterRule 注册规则。
func (e *Engine) RegisterRule(id string, fn RuleFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules[id] = fn
}

// cacheKey 由规则 ID 与排序后的参数构成，保证同一租户/用户的同一规则
// 稳定映射到唯一缓存项。
func cacheKey(ruleID string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(ruleID)
	for _, k := range keys {
		b.WriteString("|")
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(params[k])
	}
	return b.String()
}

// Evaluate 计算规则结果：命中缓存直接返回；未命中则执行规则、
// 记录维度读依赖、写入缓存并建立反向索引。
func (e *Engine) Evaluate(ruleID string, params map[string]string) (interface{}, error) {
	key := cacheKey(ruleID, params)

	e.mu.RLock()
	if ent, ok := e.cache[key]; ok {
		e.mu.RUnlock()
		e.mu.Lock()
		e.hits++
		e.mu.Unlock()
		return ent.result, ent.err
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()
	// 双检，避免并发重复计算。
	if ent, ok := e.cache[key]; ok {
		e.hits++
		return ent.result, ent.err
	}
	e.misses++

	fn, ok := e.rules[ruleID]
	if !ok {
		return nil, fmt.Errorf("rulecache: unknown rule %q", ruleID)
	}
	tx := &ReadTx{engine: e, reads: make(map[DimRef]struct{})}
	result, err := fn(tx, params)

	e.cache[key] = &cacheEntry{result: result, err: err}
	refs := make([]DimRef, 0, len(tx.reads))
	for ref := range tx.reads {
		refs = append(refs, ref)
		set, ok := e.index[ref]
		if !ok {
			set = make(map[string]struct{})
			e.index[ref] = set
		}
		set[key] = struct{}{}
	}
	e.deps[key] = refs
	return result, err
}

// evict 删除单个缓存项，并同步清理正排依赖与反向索引。
// 调用方须持有写锁。
func (e *Engine) evict(key string) {
	if _, ok := e.cache[key]; !ok {
		return
	}
	delete(e.cache, key)
	for _, ref := range e.deps[key] {
		if set, ok := e.index[ref]; ok {
			delete(set, key)
			if len(set) == 0 {
				delete(e.index, ref)
			}
		}
	}
	delete(e.deps, key)
}

// SetDimension 更新维度数据，并精准定向剔除所有读取过该维度键的
// 缓存项；未依赖该键的缓存零失效。返回被剔除的缓存项数量。
func (e *Engine) SetDimension(table, key string, fields map[string]string) int {
	e.mu.Lock()
	defer e.mu.Unlock()

	ref := DimRef{Table: table, Key: key}
	row, ok := e.dims[ref]
	if !ok {
		row = make(map[string]string)
		e.dims[ref] = row
	}
	for f, v := range fields {
		row[f] = v
	}

	affected := e.index[ref] // 精确反向索引：只命中依赖此键的缓存项
	n := 0
	for ck := range affected {
		e.evict(ck)
		n++
	}
	return n
}

// Stats 返回缓存命中/未命中次数与当前缓存项数量。
func (e *Engine) Stats() (hits, misses int64, size int) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.hits, e.misses, len(e.cache)
}

// Cached 报告某个 (规则, 参数) 组合当前是否处于缓存热态。
func (e *Engine) Cached(ruleID string, params map[string]string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.cache[cacheKey(ruleID, params)]
	return ok
}
