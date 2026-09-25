# causal — 向量时钟因果交付 + 单进程事件模拟器

纯 Go 标准库实现。消息携带向量时钟，节点仅在因果前驱全部满足后才交付；
通信、故障（延迟/重复/丢失）与虚拟时间全部由单进程事件模拟器控制，运行可复现。

## 结构

- `vectorclock.go` — 向量时钟与 happens-before 比较
- `node.go` — 因果交付节点：就绪判定、重复去重、缓冲、显式背压（`ErrBackpressure`）、
  固定成员校验（`ErrUnknownMember`）、缺前驱报告（`PendingReport`）。无超时强交付路径。
- `sim.go` — 事件模拟器：虚拟时间 + 确定性事件堆；故障接口 `Faults`
  （`Reliable` / `Chaos` / `Drop`）；背压显式重试；交付日志。
- `safety.go` — 证据校验：`VerifySafety`（因果序）、`VerifyExactlyOnce`（去重）、
  `StableOrder`（并发消息的确定性全序，Kahn + tie-break）。
- `causal_test.go` — 全部测试；`cmd/demo` — 运行样例。

## 运行

```sh
go test ./... -v
go run ./cmd/demo
```

## 安全性与活性证据

- 安全性：`TestSafetyUnderChaos` 在 200 个随机种子、延迟+重复故障下校验
  每个节点的交付日志均满足因果序且无重复交付；`TestMissingPredecessorNeverFaked`
  证明前驱永久丢失时后继永远等待，不会被超时冒充为满足。
- 活性：`TestLivenessUnderChaos` 证明在无永久丢失（仅延迟/重复/乱序）且
  背压被显式重试时，5 节点 × 6 消息全部恰好交付一次。
