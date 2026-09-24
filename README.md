# negcache

单节点、纯标准库的 Go 负缓存：把"不存在 / 临时错误 / 永久错误"分类缓存，
用同一条代际（generation）规则裁决过期、失效与成功返回之间的所有竞态。

## 设计要点

- **错误分类**（`errors.go`）：`not-found` / `transient` / `permanent` / `canceled`。
  `Classify` 把网络式错误（连接重置、DNS 超时等未知错误）归为 `transient`，
  绝不按"不存在"处理。
- **分类 TTL**（`cache.go`）：每类错误独立 TTL；临时错误随连续失败指数退避
  （`TransientTTL << failures`，封顶 `BackoffMax`），成功或显式 `Invalidate` 复位；
  所有条目受 `HardTTL` 硬上限约束。
- **负结果元数据**：负条目携带源版本号与错误分类（`Inspect` 可查），并占用容量
  （LRU 驱逐对正负条目一视同仁）。
- **代际规则**：每个 (tenant, key) 一个代际计数。成功提交、失效、过期、驱逐都会
  推进代际；在途负结果仅在代际未变时才能落盘——迟到的 not-found / 旧错误无法
  覆盖已提交的成功值；真实成功则总是覆盖负结果（版本不更旧时也覆盖旧成功）。
  在途调用同样按代际决定可否被新调用方加入。
- **取消语义**：共享拉取在分离的 context 上执行，单个调用方取消不会中断它；
  取消结果永不缓存、永不共享；日志使用独立事件名（`caller_canceled` /
  `fetch_canceled`），不会伪报为源错误。
- **租户隔离**：缓存键为 (tenant, key)，负结果不跨租户共享。
- **可注入**：数据源（`SourceFunc`）、时钟（`Clock`/`FakeClock`）、日志（`Logger`）
  全部注入；无远程缓存依赖。`Close` 等待所有在途拉取结束，无 goroutine 泄漏。

## 运行

```sh
go test -race ./...   # 单元测试 + 独立状态机/随机模型测试
go run ./cmd/demo     # 运行样例
```
