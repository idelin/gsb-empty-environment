# 缓存键未指定首部污染

网关用选定首部生成缓存键，而上游可能额外依赖另一首部。写供Go代理调用的缓存准入包，源站响应声明参与身份的首部集合，只有该集合被当前键完整覆盖才准入；每个变体保存原始请求首部实例，集合变更原子失效旧变体。用同路径不同认证上下文及迟到重验证测试不能把未区分响应发给其他请求。

## varycache

`varycache` 子包实现上述缓存准入与变体选择：

- `New(keyHeaders)`：声明网关用于生成缓存键的请求首部集合。
- `Admit`：解析源站响应的 `Vary`，仅当该身份首部集合被键首部完整覆盖时准入；`Vary: *` 或存在未覆盖首部时返回 `ErrNotCovered`。
- 每个准入变体通过 `http.Header.Clone()` 保存原始请求首部的独立快照，调用方后续修改其 map 不会污染缓存。
- 同一缓存键上身份集合变更时，旧变体被整体替换并产生新的 `Generation`；`Revalidate` 携带换代前的世代时返回 `ErrStaleGeneration`，迟到重验证响应被丢弃，不会覆盖或混入新世代。
- `Lookup` 按身份首部值匹配变体并返回副本；身份集合不再被键覆盖时按未命中处理（fail-closed）。

### 代理侧用法

```go
cache := varycache.New([]string{"Authorization"})

// 收到源站响应后
gen, err := cache.Admit(r.URL.Path, r.Header, resp.Header, body)
if errors.Is(err, varycache.ErrNotCovered) {
    // 源站依赖了缓存键之外的首部，禁止缓存，直接透传
    return
}

// 后续请求
if stored := cache.Lookup(r.URL.Path, r.Header); stored != nil {
    // 仅当请求首部与某一变体完全一致才命中
    writeResponse(stored)
    return
}

// 条件请求重验证（gen 为当初准入时的世代）
if err := cache.Revalidate(r.URL.Path, gen, r.Header, revalidated.Header, body);
errors.Is(err, varycache.ErrStaleGeneration) {
    // 身份集合已变更，本次重验证迟到，丢弃响应
}
```

### 测试

`varycache/varycache_test.go` 覆盖：同路径不同 `Authorization` 上下文不得串响应、未覆盖首部与 `Vary: *` 拒入、身份集合变更后旧变体原子失效、迟到重验证不得覆盖新世代、请求首部快照隔离、多值首部顺序敏感，以及并发准入/查询/重验证的竞态检测（`go test -race`）。
