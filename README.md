# negcache — 带错误分类的负缓存（Go 标准库）

区分**不存在 / 临时错误 / 永久错误 / 取消**四类结果，各自有独立的 TTL 与可缓存策略；
所有竞态（过期、失效、成功返回、迟到错误）由同一条**代际（generation）规则**裁决。
仅依赖 Go 标准库；数据源、时钟、错误分类器、日志全部为可注入函数，无远程缓存依赖。

## 错误分类与缓存策略

| 分类 | 判定（`DefaultClassifier`） | TTL | 可缓存 |
|---|---|---|---|
| `not-found` | `ErrNotFound` | `NotFoundTTL` | 是（负条目，携带源版本+分类） |
| `temporary` | `net.Error` 且超时（如 `TempError`） | `TempBaseTTL` 起指数退避，封顶 `TempMaxTTL` | 是（退避 TTL） |
| `permanent` | 其他错误 | `PermanentTTL` | 是 |
| `canceled` | `context.Canceled` | — | **否**，绝不进入共享缓存 |

所有条目另受 `HardTTL` 硬上限约束（`ExpiresAt ≤ StoredAt + HardTTL`）。
临时网络式错误（实现 `net.Error`）**不会**被当作不存在处理。

## 代际规则（统一竞态裁决）

- 每个 `tenant+key` 维护单调递增的代际 `gen`；`Invalidate` 与成功写入都会推进 `gen`。
- 回填（fill）启动时记录 `gen`；源调用返回时若 `gen` 已变化，则结果**只投递给本次等待者，绝不写缓存**——因此迟到的 not-found/旧错误无法覆盖已缓存的成功值。
- 双保险防线：负条目写入时若其源版本低于已缓存成功值的版本，直接丢弃（`storeNegativeLocked`）。
- 真实成功永远覆盖负结果。

## 取消语义

- 同一 `tenant+key` 的并发 `Get` 共享一次源调用（singleflight）。
- 某个调用方取消只会让自己退出；**最后一个等待者**取消才会取消源调用。
- 其他等待者照常拿到结果，结果照常入缓存——取消不污染共享结果。
- 取消在日志中标记为 `Cancellation`，永不伪报为源错误（`SourceErr`）。

## 隔离、容量与资源

- 缓存键含租户：负结果不在租户间共享。
- 负条目同样占用 `Capacity`，LRU 淘汰。
- `Close()` 取消所有在途调用并等待其结束，无 goroutine 泄漏。

## 独立状态机检查器（`Checker`）

通过 `Config.OnStore` / `Config.Log` 接入，校验四条不变式：

1. 成功值不被迟到 not-found（版本 ≤）覆盖；
2. 连续临时故障的退避 TTL 单调不减且不超过封顶；
3. 所有条目遵守硬 TTL；
4. 取消不被日志伪报为源错误。

## 文件

- `cache.go` — 缓存本体（singleflight、代际、退避、LRU、Close）
- `errors.go` — 错误分类与默认分类器
- `clock.go` / `source.go` / `logger.go` — 可注入的时钟 / 数据源 / 日志
- `checker.go` — 独立状态机检查器
- `cache_test.go` / `checker_test.go` — `go test` 测试
- `cmd/demo/main.go` — 可运行样例

## 运行

```sh
go test -race ./...
go run ./cmd/demo
```

## 测试结果（go test -race -v，18 个测试全部通过）

```
--- PASS: TestNotFoundCachedWithVersionAndClass      负结果携带源版本+分类并命中缓存
--- PASS: TestSuccessOverwritesNegative              真实成功覆盖负结果
--- PASS: TestTemporaryBackoff                       临时故障退避 1s→2s→4s→4s(封顶)
--- PASS: TestPermanentErrorTTL                      永久错误按自身 TTL 缓存/过期
--- PASS: TestCancellationNotCachedNorLoggedAsSourceError  取消不缓存、不伪报源错误
--- PASS: TestOneCallerCancelDoesNotPolluteOthers    单方取消不污染其他等待者
--- PASS: TestStaleErrorCannotOverwriteSuccess       迟到 not-found 无法压过成功值
--- PASS: TestStaleNegativeVersionGuard              旧版本负结果不能压新版本成功
--- PASS: TestTenantIsolation                        负结果租户隔离
--- PASS: TestCapacityCountsNegatives                负条目占容量、LRU 淘汰
--- PASS: TestTempNetErrorNotTreatedAsNotFound       网络式临时错不当作不存在
--- PASS: TestHardTTL                                硬 TTL 截断与强制过期
--- PASS: TestResourceReleaseOnClose                 Close 释放全部在途资源
--- PASS: TestCheckerFlagsLateNotFoundOverSuccess    状态机：成功被迟到not-found覆盖
--- PASS: TestCheckerFlagsBackoffRegression          状态机：退避回退/超封顶
--- PASS: TestCheckerFlagsHardTTLBreach              状态机：硬 TTL 违例
--- PASS: TestCheckerFlagsCancelLoggedAsSourceError  状态机：取消伪报源错误
--- PASS: TestCheckerCleanRun                        状态机：混合负载零违例
ok  negcache
```

## 样例输出（go run ./cmd/demo）

```
== 1. not-found is cached as a negative entry ==
get alice/missing (miss)           -> NEGATIVE class=not-found srcVer=1 fromCache=false (source calls=1)
get alice/missing (hit)            -> NEGATIVE class=not-found srcVer=1 fromCache=true  (source calls=1)
== 2. negative results are tenant-isolated ==
get bob/missing (own fill)         -> value="hello" (source calls=1)
== 3. temporary errors back off, never cached as not-found ==
get alice/flaky (temp #1..#3)      -> NEGATIVE class=temporary ... (source calls=1..3)
get alice/flaky (recovered)        -> value="recovered"
== 4. a real success overwrites a negative entry ==
get alice/ghost (not-found)        -> NEGATIVE class=not-found
get alice/ghost (after write)      -> value="now-exists"
== 5. one caller canceling does not pollute others ==
  waiter1 (canceled)          -> context canceled
  waiter2 (shared result)     -> value="shared-value"
  follow-up get               -> value="shared-value" fromCache=true
```
