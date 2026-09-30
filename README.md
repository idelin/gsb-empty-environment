# lspsim — 链路状态路由离散事件模拟

纯 Go 标准库实现，不发任何真实网络流量。所有包、链路队列和链路都运行在
整数 tick 的离散事件调度器上。

## 模型

- **离散事件调度器**（`sim.go`）：最小堆事件队列，时间为整数 tick，同 tick
  事件按入队顺序处理，保证确定性。
- **链路与队列**（`network.go`）：每条链路每个方向一个 FIFO 传输队列，
  每个包占用 `TxTime` tick 串行化时间 + `Delay` tick 传播时延。
- **LSA**（`lsa.go`）：`{Origin, Seq, Links}`，序列号单调递增；路由器在
  链路变化或刷新定时器（寿命的一半）触发时重新发起。
- **重复/旧通告丢弃**（`router.go` `receiveLSA`）：`Seq <= 已存 Seq` 直接丢弃，
  因此旧通告永远不会复活已删除的链路。
- **寿命与清除**：LSA 安装 `LSALifetime` tick 后过期，从 LSDB 删除并向邻居
  泛洪 Purge；失效通告（不再包含某邻居的新 LSA）即删除对应边。
- **SPF 去抖**（`router.go` `scheduleSPF`）：一个 `SPFDelay` 窗口内任意多次
  LSDB 变化只触发一次 Dijkstra 重算。
- **等价路径下一跳稳定**（`spf.go`）：Dijkstra 中成本相同时按较小的首跳
  路由器 ID 决胜，拓扑不变时重算结果确定不变。

## 运行

```sh
go test ./...      # 全部测试
go run ./cmd/demo  # 运行样例
```

## 测试（go test）

```
=== RUN   TestOldAndDuplicateLSADiscarded
--- PASS: TestOldAndDuplicateLSADiscarded (0.00s)
=== RUN   TestStaleLSADoesNotResurrectLink
--- PASS: TestStaleLSADoesNotResurrectLink (0.00s)
=== RUN   TestSPFDebounce
--- PASS: TestSPFDebounce (0.00s)
=== RUN   TestEqualCostNextHopStable
--- PASS: TestEqualCostNextHopStable (0.00s)
=== RUN   TestConvergenceAfterFailureAndRecovery
--- PASS: TestConvergenceAfterFailureAndRecovery (0.00s)
=== RUN   TestExpiryRemovesDeadRouter
--- PASS: TestExpiryRemovesDeadRouter (0.00s)
PASS
ok  	example.com/lspsim
```

- `TestOldAndDuplicateLSADiscarded`：旧序号与重复序号 LSA 均被丢弃，LSDB 不被覆盖。
- `TestStaleLSADoesNotResurrectLink`：链路 1-2 拆除后，经慢速中继迟到的旧 LSA
  被丢弃，已删链路不复活，路由不恢复。
- `TestSPFDebounce`：去抖窗口内 3 次 LSDB 变化只触发 1 次 SPF。
- `TestEqualCostNextHopStable`：菱形拓扑两条等价路径，下一跳恒为较小 ID 邻居，
  无关链路翻动后仍稳定。
- `TestConvergenceAfterFailureAndRecovery`：环形+弦拓扑，链路故障/恢复后全网
  LSDB 一致、逐跳路径无环且成本与转发一致。
- `TestExpiryRemovesDeadRouter`：路由器宕机停止刷新，其 LSA 在全网老化清除，
  其余路由器最终一致。

## 运行样例（`go run ./cmd/demo`，节选）

6 台路由器组成环 + R0-R3 弦，tick 100 链路 R1-R2 故障，tick 300 恢复，
tick 500 路由器 R4 宕机：

```
== initial convergence @ tick 80 ==
R0:  R1 via R1 (cost 1)  R2 via R1 (cost 2)  R3 via R3 (cost 1)  R4 via R3 (cost 2)  R5 via R5 (cost 1)
R1:  R0 via R0 (cost 1)  R2 via R2 (cost 1)  R3 via R0 (cost 2)  R4 via R0 (cost 3)  R5 via R0 (cost 2)
...

-- tick 100: link R1-R2 fails --
== after link R1-R2 failure @ tick 200 ==
R1:  R0 via R0 (cost 1)  R2 via R0 (cost 3)  ...   # 改道 R1-R0-R3-R2

-- tick 300: link R1-R2 recovers --
== after recovery @ tick 400 ==
R1:  R0 via R0 (cost 1)  R2 via R2 (cost 1)  ...   # 恢复直连

-- tick 500: router R4 goes down --
== after R4 down (its LSA expired everywhere) @ tick 700 ==
R0:  R1 via R1 (cost 1)  R2 via R1 (cost 2)  R3 via R3 (cost 1)  R5 via R5 (cost 1)
R4: DOWN                                              # R4 的 LSA 已全网过期清除

== statistics ==
ran until tick 9998, 12025 events processed
R0: SPF runs=330, LSAs originated=167, stale LSAs dropped=364
...
link R0-R1: packets=1015, max backlog=2 ticks
...
```

完整输出还包含每台路由器的 SPF 次数、发起的 LSA 数、丢弃的旧 LSA 数，
以及每条链路的包数和最大队列积压（tick）。
