package rulecache

import "fmt"

// EvalContext 规则求值上下文：规则通过它读取维度，
// 每次读取都会被记录为依赖，用于构建精确反向索引。
type EvalContext struct {
	Tenant string
	store  *DimensionStore
	deps   map[DimKey]struct{}
}

// Get 读取维度属性，并把该维度键登记为本次结果的依赖。
func (c *EvalContext) Get(key DimKey, attr string) (string, bool) {
	c.deps[key] = struct{}{}
	return c.store.Get(key, attr)
}

// Rule 业务规则接口。
type Rule interface {
	Name() string
	Evaluate(ctx *EvalContext, subject string) (any, error)
}

// Engine 规则结果计算器：缓存 + 失效传播控制器。
type Engine struct {
	store *DimensionStore
	cache *Cache
}

func NewEngine(store *DimensionStore, cache *Cache) *Engine {
	e := &Engine{store: store, cache: cache}
	// 维度更新 -> 沿反向索引精准剔除受影响缓存项
	store.OnUpdate(func(d DimKey) { cache.InvalidateByDim(d) })
	return e
}

// Evaluate 查询规则结果：命中缓存直接返回；未命中则求值并记录依赖。
func (e *Engine) Evaluate(rule Rule, tenant, subject string) (any, error) {
	ck := CacheKey(fmt.Sprintf("%s|%s|%s", tenant, rule.Name(), subject))
	if v, ok := e.cache.Get(ck); ok {
		return v, nil
	}
	ctx := &EvalContext{Tenant: tenant, store: e.store, deps: make(map[DimKey]struct{})}
	v, err := rule.Evaluate(ctx, subject)
	if err != nil {
		return nil, err
	}
	e.cache.Put(ck, v, ctx.deps)
	return v, nil
}

func (e *Engine) Stats() Stats { return e.cache.Stats() }
