# Piggyback Gossip 成员变动消息分发器（Go）

SWIM 风格的高带宽利用率状态传播：节点加入 / 离开 / 疑似等变动事件进入
**传播计数队列**，在常规 Ping/Ack 网络包头部**捎带传输**，并限制每条消息的
最大扩散轮数为 `retransmitMult * ceil(log2(N+1))`。

## 结构

- `gossip/member.go` — 成员状态（Alive/Suspect/Dead/Left）、变动事件、基于 incarnation 的冲突裁决
- `gossip/disseminator.go` — 传播计数队列：最少传播者优先、每节点只保留最新事件、超轮数自动淘汰
- `gossip/node.go` — 成员视图表、事件合并、自身疑似时的 incarnation+1 反驳、Ping/Ack 捎带处理
- `gossip/cluster.go` — 周期化 Ping/Ack 模拟器（每周期每节点向 Fanout 个随机对等点发 Ping）
- `cmd/demo/main.go` — 100 节点 Join 收敛演示

## 运行样例

```
$ go run ./cmd/demo
node node-099 joined via seed node-000; disseminating via piggyback gossip
convergence budget: ceil(log2(100)) = 7 periods

period  1:  86/100 nodes aware of node-099
period  2: 100/100 nodes aware of node-099

converged: all 100 nodes updated their membership view in 2 periods
sample view entry on node-098: {ID:node-099 Addr:127.0.0.1:7099 State:alive Incarnation:0}
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestDisseminatorPrefersLeastPropagated
--- PASS: TestDisseminatorPrefersLeastPropagated (0.00s)
=== RUN   TestDisseminatorMaxRounds
--- PASS: TestDisseminatorMaxRounds (0.00s)
=== RUN   TestDisseminatorOverrideAndStale
--- PASS: TestDisseminatorOverrideAndStale (0.00s)
=== RUN   TestNodeRefutesSuspicion
--- PASS: TestNodeRefutesSuspicion (0.00s)
=== RUN   TestNodeIgnoresStaleEvents
--- PASS: TestNodeIgnoresStaleEvents (0.00s)
=== RUN   TestJoinConvergesWithinLogN
--- PASS: TestJoinConvergesWithinLogN (0.03s)
    gossip_test.go:118: all 100 nodes learned the join of node-099 after 2 periods (budget 7)
=== RUN   TestSuspectEventConverges
--- PASS: TestSuspectEventConverges (0.05s)
PASS
ok  	piggyback/gossip	1.228s
```

## 验收边界

`TestJoinConvergesWithinLogN`：99 节点集群中第 100 个节点经种子节点 Join，
其变动事件被捎带在后续 Ping/Ack 头部传播；断言全部 100 个节点在
`ceil(log2(100)) = 7` 个协议周期内感知并在成员视图表中记录该节点为 Alive
（实测 2 个周期收敛）。
