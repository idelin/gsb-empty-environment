# cacheaudit

带审计的版本化缓存 + 历史一致性审计器。纯 Go 标准库，无远程缓存依赖。

## 组成

- `event.go` — 事件模型（call/load/publish/evict）与**有界环形日志**。每个事件带全局
  单调序号；环形覆盖或显式丢失（`NoteLost`）都会计数，读者可据此判定证据不完整，
  而不是猜测完整历史。
- `cache.go` — 版本化缓存。数据源（`Loader`）、时钟（`Now`）、失败注入（`Fail`）
  全部是可注入函数。记录命中/失效/加载/淘汰/发布事件；**未发布的在途加载永远记为
  miss，不会记为命中**；命中率分母口径固定为全部 Get 调用（含出错调用）；每个加载
  资源的 `release` 在替换/淘汰/关闭时恰好调用一次。
- `audit.go` — 审计器。按公开一致性契约（调用区间内读到该区间某时刻的最新已发布
  版本，即版本化可线性化读）搜索合法线性化顺序；校验每个返回值的来源版本都能追溯
  到已记录的 publish。三种结论严格分开：
  - `CONSISTENT`：存在合法线性化；
  - `STALE`：日志完整且可证明违反契约（确定陈旧 / 返回值从未发布）；
  - `INCONCLUSIVE`：丢片或环形覆盖，证据不足，拒绝猜测。
  `Replay` 重放每 key 的发布时间线，定位陈旧返回的原因（返回值来源 publish 与
  调用开始前已发布的更新版本，均带序号证据）。
- `reference.go` — 独立的全排列参考核验器（小历史枚举所有排列），用于交叉验证
  审计器的记忆化搜索。
- `minimize.go` — 贪心 delta-debugging 缩减：自动给出仍违反同一契约的 1-最小事件
  子集；缩减谓词要求**保留来源版本证据**（每个返回值对应的 publish 不得被删掉）。

## 运行

```sh
go test ./...      # 全部测试
go test -race ./...# 含竞态检测
go run ./cmd/demo  # 三个场景的运行样例
```

## 运行样例（`go run ./cmd/demo` 摘要）

```
=== Scenario A: instrumented cache run (expect CONSISTENT) ===
   #1 load alpha [...,...] v1
   #2 publish alpha v1 @...
   #3 call alpha [...,...] -> v1 (miss)
   #4 call alpha [...,...] -> v1 (hit)
   #5 publish alpha v2 @...
   #6 call alpha [...,...] -> v2 (hit)
   ...
stats: {Gets:4 Hits:2 Misses:2 Errors:0 Loads:2 LoadErrors:0 Publishes:3 Evicts:1 EventsLost:0}
hit-rate: 0.50 (denominator = all 4 Get calls)
verdict: CONSISTENT

=== Scenario B: replay a captured stale history (expect STALE) ===
verdict: STALE
  stale read: call #4 [...] returned v1 (published by #1 at ...), but v2 was
              already published by #3 at ..., before the call began
  minimal violating subset (3 events, source evidence kept):
    #1 publish alpha v1 ...
    #3 publish alpha v2 ...
    #4 call alpha [...] -> v1 (hit)

=== Scenario C: tiny ring overwrites evidence (expect INCONCLUSIVE) ===
retained 3 of the events (capacity 3, dropped 13)
verdict: INCONCLUSIVE (insufficient evidence)
  evidence gap: audit log lost 13 event(s) (ring overwrite or dropped records); ...
```

## 测试结果

`go test ./...`：19 个测试全部通过（另经 `go test -race` 验证），覆盖：

- 命中/失效/加载/淘汰/发布事件记录与固定分母命中率；
- 未发布加载不计命中；日志追加失败计入 `EventsLost` 并判定证据不完整；
- 资源释放（替换/淘汰/关闭各恰好一次，`Close` 幂等）；
- 一致/并发可线性化/确定陈旧/丢片与序号空洞证据不足/未发布返回值违规；
- 审计器与全排列参考核验器在 2000 组随机小历史上结果一致；
- 缩减器产出 1-最小违规子集且保留来源版本证据。
