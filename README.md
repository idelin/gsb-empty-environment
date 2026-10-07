# reserve — Go 资源预约库

单资源池的并发安全预约存储。每个预约占据**半开时间区间** `[Start, End)`
和整数容量；任意瞬时的并发负载之和不得超过池容量，负载用**端点扫描**
（sweep-line，同一瞬时先处理结束事件再处理开始事件）核算。

## API

- `NewStore(capacity)` — 创建存储。
- `Book(expectedVersion, r)` — 新增预约。
- `Modify(expectedVersion, id, start, end, capacity)` — **原子替换**旧区间：
  在持锁状态下用旧预约仍占位的前提校验新区间，冲突则旧预约原样保留，
  绝不出现"先释放再失败丢失旧预约"。
- `Cancel(expectedVersion, id)` — 撤销，**幂等**：重复撤销（重试）成功且
  不推进版本。
- `Version()` / `Get(id)` / `MaxLoad()` — 版本、查询、负载审计。

## 语义约定

- 每次成功变更版本号 +1；请求携带 `expectedVersion`，不匹配返回
  `ErrVersionMismatch`，**任何失败都不改变状态和版本**。
- 零长预约（`Start == End`）合法且不占用任何容量；`Start > End` 或负容量
  返回参数错误。
- 半开区间相邻不重叠：`[0,10)` 与 `[10,20)` 可占满同一容量。

## 测试

```sh
go test ./reserve -race -count=1
```

覆盖：并发修改的原子性与终态合法性审计、并发预约/撤销的版本计数
一致性、零长预约、撤销重试幂等，以及端点扫描与逐点暴力模型的随机
对拍（`TestEndpointScanModel`）。
