# privacy — 隐私预算账本与查询服务

多个研究组查询重叠人群的计数，每次查询按所涉及的分组消耗各自的隐私预算（epsilon）。

## 设计

- **原子预留**：`Ledger.Reserve` 先检查全部分组余额，全部足够才一次性扣减；
  任一组不足则整体失败，任何分组预算都不受影响。
- **发布才结算**：预留仅是暂扣。`Ledger.Settle` 在结果写入日志（发布点）时把预留转为
  永久消耗；查询失败或取消时 `Ledger.Release` 退还预留。已发布的预算绝不退还——
  `Release` 对已结算的键是 no-op。
- **单次飞行**：相同查询键的并发请求共享一个 flight——一次计算、一次预留、
  一次 Laplace 噪声抽取、一份结果。
- **崩溃恢复**：所有状态变更先写 WAL（`journal.go`，逐条 fsync）再改内存。
  `result` 记录即发布点。重启重放日志后：已发布的结果原样返回（同一噪声、不再扣费），
  崩溃时悬空的预留被退还，撕裂的日志尾部被截断。

## 故障注入

`Service.AfterPublish` 钩子在结果落盘之后、响应返回之前执行；返回 `true` 模拟崩溃
（结果已发布但响应永不送达）。`TestCrashBetweenPublishAndResponseRecoversIdempotently`
验证：换用不同随机种子重建服务后，同一查询键幂等返回相同带噪结果，预算恰好扣一次。

```sh
go test ./privacy/ -race
```
