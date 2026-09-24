# ttlcache — 最小堆 + 代际校验的 TTL 缓存

纯标准库实现（`container/heap` + `sync`），无远程依赖。时钟、数据源、失败
全部通过 `Options` 注入，测试使用假时钟与失败注入函数。

## 机制

- **最小堆调度**：每个定时事件是堆节点 `{key, gen, expireAt}`，`Expire()` 弹出
  所有 `expireAt <= now` 的节点。
- **代际（generation）**：全局单调计数器，每次 `Set`/`Renew`/`RenewBatch` 使条目
  代际 +1 并追加新堆节点；旧节点不删（惰性失效）。
- **双向代际校验**：弹出节点时，仅当 `entry.gen == node.gen && entry.expireAt ==
  node.expireAt` 才删除条目，否则丢弃节点（`StaleDropped++`）。因此旧定时事件
  永远不会删除续期项；批量续期与全局时钟跳跃下，只删除当时确实过期的版本。
- **惰性失效 + 定期压缩**：陈旧节点等弹出时丢弃；当 `堆大小 > 2*(活条目+1)`
  时在 `Expire()` 末尾触发压缩重建。
- **并发压缩**：压缩开始时把主堆摘下、置 `compacting`；期间新定时器照常入队
  （进入 `pending` 缓冲，`Expire` 同时扫描两个堆，不阻塞、不遗漏）；过滤基于
  活版本快照在锁外完成，最后在锁内把 `kept + pending` 合并堆化并**原子换入**，
  不丢任何事件。gen 全局单调保证过滤方向安全：被丢的旧代际永不复活，误留的
  节点由惰性失效兜底。
- **半开语义**：键在 `[写入时刻, expireAt)` 内有效；`now == expireAt` 即失效。
- **时钟回拨策略（明确）**：所有过期判断都是对当前时钟的即时比较。回拨只推迟
  触发，绝不提前删除，也绝不复活已删除条目；前跳只触发 `expireAt <= now` 的
  事件。不要求时钟单调。

## 上界与工作量

设 `L` = 活条目数，`H` = 堆节点数，`S = H - L` = 陈旧节点数。

- **不变量**：每次 `Expire()` 返回后 `H <= 2L + 2`，即 `S <= L + 2`。
  压缩阈值 `H > 2(L+1)` 保证触发前峰值 `H <= 2L + 3`。
- **摊还分析**：两次压缩之间至少发生 `L+2` 次入队（每个陈旧节点对应一次
  `Set`/`Renew`），压缩成本 `O(H) = O(L)`，摊还到每次入队为 **O(1)**；
  堆操作本身 `O(log H)`。`Expire()` 成本 `O(k log H)`，`k` 为触发事件数。
- **长期续期**：同键续期 N 次，无压缩时堆涨到 N；有压缩时峰值 `<= 2L+3`
  （单键即 5）。实测 1000 次续期堆峰值 = 4，压缩 250 次。
- **内存**：`O(L)` 摊还；`Get` 命中过期键时就地删除，加速资源释放。

## 运行

```sh
go test -race -v ./...   # 测试（含逐键扫描参考对照）
go run ./cmd/demo        # 运行样例
```

## 测试覆盖

| 测试 | 验证点 |
|---|---|
| `TestHalfOpenSemantics` | 截止前 1ns 命中，恰好到点失效 |
| `TestRenewStaleTimerMustNotDelete` | 旧定时事件不得删续期项 |
| `TestDeleteThenResetOldTimerHarmless` | 删除重建后旧代际节点无害 |
| `TestBatchRenewAndClockJump` | 批量续期 + 时钟前跳，只删确实过期版本 |
| `TestClockBackwardJumpPolicy` | 回拨不删、不复活 |
| `TestThousandRenewsHeapBounded` | 1000 次续期堆峰值 ≤ 2L+3 |
| `TestCompactionConcurrentEnqueueNoLoss` | 压缩期间并发入队不丢事件、原子换入 |
| `TestReferenceCrossCheckAllTriggerPoints` | 3000 步随机操作，逐键扫描参考对照全部触发点 + 上界不变量 |
| `TestLoaderFailureLeavesNoState` | 数据源失败注入不留状态 |
| `TestResourceRelease` | 过期/删除/覆盖后条目与堆节点全部释放，回调可观测 |
| `TestJanitor` | 后台周期清理（真实时钟） |
