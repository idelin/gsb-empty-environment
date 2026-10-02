# swimtrust — 拜占庭静默节点的局部驱逐与信任衰减评分器

针对 SWIM 风格 gossip 集群中的两类不可靠节点：

- **静默节点**：接收 `PingReq`（间接探测）请求却从不代为转发、也不应答；
- **伪造节点**：为实际已死亡的节点伪造 `Alive` 状态。

每个节点维护一份**本地** `TrustScorer`（无共享状态）：所有 peer 初始信用
100 分，每次确认的违规按类型扣分，跌破隔离阈值（≤ 20）即触发**强制隔离**
——停止对其直接探测与 `PingReq` 请求，并对外宣告隔离。

## 评分规则

| 违规类型             | 判定方式                                   | 扣分 |
|----------------------|--------------------------------------------|------|
| `REFUSE_PINGREQ`     | helper 收到 PingReq 后整轮无任何中继应答   | -10  |
| `FORGE_ALIVE`        | helper 声称目标存活，但应答多数派证明其死亡 | -20  |

- 初始分 `InitialScore = 100`，下限 `MinScore = 0`；
- `QuarantineThreshold = 20`：分数 ≤ 20 时触发一次（且仅一次）隔离回调；
- 目标存活性由 helper 应答的**多数票（quorum）**裁决，单票伪造无法翻案。

## 文件结构

- `score.go` — 信任评分器：违规记录、分数衰减、阈值隔离
- `node.go` — 节点与行为模型（honest / silent-relay / forge-alive）
- `cluster.go` — 确定性内存仿真：Ping → PingReq → quorum 裁决 → 违规确认
- `swimtrust_test.go` — `go test` 验收测试
- `cmd/demo/main.go` — 可运行演示

## 运行

```sh
go test ./... -v     # 验收测试
go run ./cmd/demo    # 演示：静默节点 B 被驱逐全过程
```

## 运行样例（`go run ./cmd/demo`，节选）

```
== Phase 1: A probes T; B silently drops every PingReq ==
round  0: A's trust in B =  90  quarantined=false
round  1: A's trust in B =  80  quarantined=false
...
round  7: A's trust in B =  20  quarantined=true
round  8: A's trust in B =  20  quarantined=true

== Isolation announcements ==
  A declares B ISOLATED
  C declares B ISOLATED
  D declares B ISOLATED

== Post-quarantine: A refuses to direct-probe B ==
  direct probe attempted: false

== A's trust ledger ==
  peer=B score= 20 quarantined=true  violations=8
  peer=C score=100 quarantined=false violations=0
  peer=D score=100 quarantined=false violations=0
  peer=T score=100 quarantined=false violations=0
```

事件日志（节选）：

```
[round 00] node=A ping T -> no ack, escalating to PingReq
[round 00] node=A VIOLATION REFUSE_PINGREQ helper=B score=90
[round 00] node=A learned T is alive via indirect probe (quorum 2 alive vs 0 dead)
...
[round 07] node=A VIOLATION REFUSE_PINGREQ helper=B score=20
[round 07] node=A QUARANTINE peer=B score=20 -> cut direct probes, announce isolation
```

## 测试结果

```
=== RUN   TestSilentNodeEvictedAfterTrustDecay
--- PASS: TestSilentNodeEvictedAfterTrustDecay (0.00s)
=== RUN   TestForgeAliveDetectedAndQuarantined
--- PASS: TestForgeAliveDetectedAndQuarantined (0.00s)
=== RUN   TestHonestNodesKeepFullTrust
--- PASS: TestHonestNodesKeepFullTrust (0.00s)
=== RUN   TestTrustScorerBoundaries
--- PASS: TestTrustScorerBoundaries (0.00s)
PASS
ok  	swimtrust
```

## 验收边界覆盖

- `TestSilentNodeEvictedAfterTrustDecay`：作恶节点 B 连续 8 轮拒绝代发
  PingReq，信用 100 → 90 → … → 20 触发隔离；此后 A 不再对 B 发起直接
  探测；C、D 独立观测后同样宣告 B 隔离（共 3 份隔离宣告）。
- `TestForgeAliveDetectedAndQuarantined`：F 为已崩溃的 T 伪造 Alive，
  被诚实多数派（2 票 dead vs 1 票 alive）揭穿，每轮 -20，4 轮后隔离。
- `TestHonestNodesKeepFullTrust`：诚实节点 20 轮后仍满分、零误判。
- `TestTrustScorerBoundaries`：扣分边界、恰好触及阈值即隔离、分数下限
  为 0、隔离回调恰好触发一次。
