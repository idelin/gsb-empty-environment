# quotasim — 副本配额/权益系统的单进程事件模拟器

仅用 Go 标准库。通信（延迟/丢包/重复）、故障（崩溃/重启）与时间（虚拟时钟）
全部由 `sim.Simulator` 的事件队列控制。

## 模型

- 全局配额 `Total` 被**不相交地划分**为各副本的本地权益 `Local`。
- **离线扣减** `Deduct` 无需任何通信：因为分片不相交，全局消耗永不超过 `Total`。
- **权益转移** `Transfer(from, to, amount, id)`：
  - 发送方立即扣减本地权益，金额进入 *in-flight* 状态（对任何人都不可用）；
  - 接收方按唯一 ID 幂等入账，**同一 ID 至多入账一次**，重复投递/重试只重发 ACK；
  - 同一 ID 的请求级重试在发送方去重，不会二次扣减；
  - 发送方未收到 ACK 前按 `RetryInterval` 重传，崩溃重启后恢复重传。
- 发送方已扣、接收方未收时，全局可用量 = ΣLocal **减少**（in-flight 不可用），
  消息丢失只会损失可用量，绝不会凭空造出额度。

## 证据

- **安全性**（`sim/sim.go` 的 `checkInvariants`，每个事件后检查）：
  1. 守恒：`ΣLocal + ΣInflight + Consumed == Total`；
  2. 不透支：任意副本 `Local >= 0`；
  3. 幂等：任意转移 ID 入账次数 `<= 1`。
  违反即记录到 `Violations`；测试断言其恒为空。
- **活性**：网络愈合（`Net.Healed=true`，保证一个 tick 内送达）这一公平性假设下，
  发送方持续重传直到收到 ACK，因此所有转移在有界虚拟时间内 *settled*
  （`Settled()==true`：无 pending、无 in-flight）。由 `TestLivenessAfterHeal` 验证。

## 运行

```sh
go run .        # 打印演示轨迹 + 安全/活性证据
go test ./...   # 全部性质测试（go test -v 查看逐项）
```
