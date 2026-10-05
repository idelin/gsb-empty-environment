# leasesim — 租约与 fencing token 可重放模拟器

纯 Go 标准库实现，复现并验证主备故障：旧主被暂停、租约过期后恢复，
仍把排队写请求发往存储，存储通过 fencing token 拒绝它。

## 模型

- **时钟**：唯一的时钟是整数 tick，由 harness 用 `Tick(n)` 显式推进；无真实网络、无 goroutine、无等待。
- **投票者（5 个）**：持久化（重启不丢）已见最大 epoch 与已授予租约的半开区间 `[start, end)`；
  同一时刻不会向不同主授予重叠的有效租约（`conflicting lease`），低 epoch 请求拒绝（`stale epoch`）。
- **候选主**：收齐同 epoch、同 owner 的 3 份不同投票者授予才形成证书，
  授权截止取三者截止的最小值；续约只拿到部分授予时证书截止不变。
- **存储**：校验调用 tick、证书与已见最大 epoch —— 拒绝过期证书（`tick >= expiry`，
  半开区间边界恰好拒绝）与低 epoch 请求（`fenced`）；重试按请求 ID 去重，同 ID 不同内容报错。

## 故障注入

`Pause` / `Resume` / `Restart`（重启保留磁盘承诺）、`Drop` / `Dup` / `DeliverOne`
（乱序、丢弃、重复）、`Flush`（送达所有到期消息）。

## 回放

每个 harness 操作记录为 `Op`，`MarshalOps` + `ReplayJSON` 在新世界上重放，
产生逐字节一致的事件日志与已提交写序列（见 `TestReplayProducesIdenticalResults`）。

## 运行

```sh
go test ./... -v   # 8 个场景测试
go run ./cmd/demo  # 打印旧主恢复故障的完整事件日志
```
