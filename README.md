# byzguard — 拜占庭静默节点的局部驱逐与信任衰减评分器 (Go)

针对 SWIM 风格集群中的不可靠节点：**只接收 Ping 却不转发 PingReq**（静默丢弃）
或**伪造 Alive 状态**（为已死节点背书）。每个诚实节点在本地维护一份交互信用
评分（`trust.Scorer`），信誉跌破阈值时触发**强制隔离**：切断对该节点的直接
探查与 PingReq 转发，并向集群宣告其隔离。纯标准库，无第三方依赖。

## 结构

- `trust/scorer.go` — 信任评分器：违规衰减、良好行为缓慢恢复、隔离阈值、
  隔离公告的本地同步（`ForceQuarantine`）。隔离不可逆。
- `cluster/cluster.go` — 集群模拟：直接探测（Ping）、间接探测（PingReq 转发）、
  Alive  gossip 交叉验证、隔离宣告传播。
- `main.go` — 验收场景演示。
- `trust/scorer_test.go`、`cluster/cluster_test.go` — `go test` 测试。

## 评分策略（默认值）

| 参数 | 值 |
|---|---|
| 初始分 / 上限 | 100 |
| 隔离阈值 | 20（`score <= 20` 触发，不可逆） |
| 拒绝转发 PingReq | -20 |
| 伪造 Alive | -25 |
| 正确转发 PingReq | +4 |
| 应答直接 Ping | +2 |

良好行为会重置连续违规计数；分数被钳制在 `[0, 100]`。

## 运行样例

```console
$ go run .
policy: initial=100 threshold=20 penalty(PingReq refused)=20

== round 1 ==
  [M] PingReq via B DROPPED -> trust(B)=80
  => indirect probe of T via B acked=false
== round 2 ==
  [M] PingReq via B DROPPED -> trust(B)=60
  => indirect probe of T via B acked=false
== round 3 ==
  [M] PingReq via B DROPPED -> trust(B)=40
  => indirect probe of T via B acked=false
== round 4 ==
  [M] PingReq via B DROPPED -> trust(B)=20
  [M] QUARANTINE B: trust fell to 20 (threshold 20)
  [M] declares B ISOLATED: cutting direct probes and PingReq relays
  [H1] declares B ISOLATED: cutting direct probes and PingReq relays
  [H2] declares B ISOLATED: cutting direct probes and PingReq relays
  [T] declares B ISOLATED: cutting direct probes and PingReq relays
  => indirect probe of T via B acked=false
== round 5 ==
  [M] PingReq via H1 acked -> trust(H1)=100
  => indirect probe of T via H1 acked=true
== round 6 ==
  [M] PingReq via H1 acked -> trust(H1)=100
  => indirect probe of T via H1 acked=true

== final state ==
M: trust(B)=20 quarantined=true consecutive_violations=4
H1: isolated(B)=true trust(B)=20
H2: isolated(B)=true trust(B)=20
direct probes M sent to B after isolation: 0 (must stay 0)
post-isolation DirectProbe(M,B): sent=false (blocked locally), attempts=0
event: QUARANTINE node=B score=20 consecutive_violations=4 reason=PINGREQ_REFUSED
```

作恶节点 B 连续 4 轮丢弃 PingReq，信任分从 100 滑落至 20，触发强制隔离；
此后 M 不再向 B 发送任何直接探查，改用诚实中继 H1，探测恢复成功。

## 测试结果

```console
$ go test ./... -v
=== RUN   TestByzantineDropperIsEvicted
--- PASS: TestByzantineDropperIsEvicted (0.00s)
=== RUN   TestHonestRelayKeepsFullTrust
--- PASS: TestHonestRelayKeepsFullTrust (0.00s)
=== RUN   TestForgedAliveIsDetectedAndQuarantined
--- PASS: TestForgedAliveIsDetectedAndQuarantined (0.00s)
=== RUN   TestTruthfulGossipIsNotPunished
--- PASS: TestTruthfulGossipIsNotPunished (0.00s)
PASS
ok  	byzguard/cluster
=== RUN   TestDecaySlidesFrom100To20AndQuarantines
--- PASS: TestDecaySlidesFrom100To20AndQuarantines (0.00s)
=== RUN   TestRewardsRecoverScoreAndResetStreak
--- PASS: TestRewardsRecoverScoreAndResetStreak (0.00s)
=== RUN   TestForgeryPenalty
--- PASS: TestForgeryPenalty (0.00s)
=== RUN   TestQuarantineIsPermanent
--- PASS: TestQuarantineIsPermanent (0.00s)
=== RUN   TestForceQuarantine
--- PASS: TestForceQuarantine (0.00s)
PASS
ok  	byzguard/trust
```

9/9 通过。覆盖：100→20 衰减曲线与阈值边界、奖励恢复与连续违规重置、
伪造 Alive 检测、隔离不可逆、诚实节点不误伤、隔离后探查被本地阻断。
