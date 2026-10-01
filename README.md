# fwlab — 防火墙规则遮蔽/冲突分析 + 离散事件仿真

纯 Go 标准库实现，不产生任何真实网络流量：所有包都是合成包头，
所有包、队列和链路都在整数 tick 的离散事件模型中运行。

## 功能

- 规则按 **前缀 + 协议 + 目的端口范围** 匹配，按 **优先级** 求值（数值小者先匹配，ID 打破平局）。
- **遮蔽分析**：后规则被前规则（单条或多条并集）完全覆盖 → 不可达；只被部分覆盖 → 仅报重叠。
- **冲突检测**：重叠区域上动作（allow/deny）不一致即报冲突。
- **反例生成**：每条遮蔽/冲突结论都附带一个合成反例包（witness），只存在于模型内。
- **IPv4/IPv6 不混配**：两个地址族空间互不相交，跨族规则永不互相遮蔽或匹配。
- **离散事件仿真**：分类器 → FIFO 队列（有容量上限）→ 链路（每包固定串行化 tick + 传播时延 tick），
  事件堆按 (tick, 序号) 排序，结果完全确定。

## 文件

- `model.go` — 规则、包、分类器（首匹配，默认 deny）
- `space.go` — 头空间矩形代数：交、减、并集覆盖判定
- `analyze.go` — 遮蔽/冲突分析与反例包生成
- `sim.go` — 离散事件仿真器（事件堆、队列、链路）
- `main.go` — 演示规则集与仿真运行

## 运行

```sh
go run .     # 演示
go test ./...  # 测试
```

## 运行样例（节选）

```
== unreachable rules (fully shadowed by earlier rules) ==
  rule #3 is unreachable; counterexample pkt#2 ipv4 dst=10.0.0.0 proto=6 dport=80
  rule #5 is unreachable; counterexample pkt#5 ipv4 dst=192.168.0.0 proto=17 dport=53
  rule #9 is unreachable; counterexample pkt#7 ipv4 dst=10.9.0.0 proto=6 dport=100

== pairwise overlaps / conflicts ==
  rule #2 vs earlier rule #1: overlap CONFLICT(actions differ); witness pkt#1 ipv4 dst=10.1.0.0 proto=6 dport=80
  rule #3 vs earlier rule #1: shadowed CONFLICT(actions differ); witness pkt#3 ipv4 dst=10.0.0.0 proto=6 dport=80
  ...

== discrete-event run (integer ticks, synthetic packets only) ==
  tick=0   pkt#2 ipv4 dst=10.0.0.0 proto=6 dport=80 allowed by rule #1, enqueued (qlen=1)
  tick=0   pkt#2 ipv4 dst=10.0.0.0 proto=6 dport=80 dequeued, link busy until tick=2
  tick=3   pkt#2 ipv4 dst=10.0.0.0 proto=6 dport=80 delivered
  tick=4   pkt#3 ipv4 dst=10.0.0.0 proto=6 dport=80 dropped (queue full, cap=2)
  tick=9   pkt#900 ipv4 dst=203.0.113.7 proto=6 dport=22 dropped (no rule matched, default deny)
  ...
delivered=7 dropped=7
```

说明：规则 #9（deny 10.9.0.0/16:100-300）被 #7、#8 两条 allow 的**并集**完全覆盖，
单看任何一条都只是部分重叠 —— 分析同时给出「不可达」结论和两条 pairwise 重叠记录。

## 测试结果

```
$ go test -v ./...
=== RUN   TestFullyShadowedIsUnreachable      --- PASS
=== RUN   TestPartialOverlapStaysReachable    --- PASS
=== RUN   TestUnionShadowing                  --- PASS
=== RUN   TestNoCrossFamilyMixing             --- PASS
=== RUN   TestWitnessIsCounterexample         --- PASS
=== RUN   TestPriorityOrder                   --- PASS
=== RUN   TestDeliverOrderAndTiming           --- PASS
=== RUN   TestDenyAndDefaultDeny              --- PASS
=== RUN   TestQueueFullDrop                   --- PASS
=== RUN   TestDeterministic                   --- PASS
=== RUN   TestIPv6PacketThroughSim            --- PASS
=== RUN   TestLastAddr                        --- PASS
=== RUN   TestIntersectCrossFamily            --- PASS
=== RUN   TestSubtractDisjoint                --- PASS
=== RUN   TestSubtractFullCover               --- PASS
=== RUN   TestSubtractPartial                 --- PASS
=== RUN   TestRemainderUnionCoverage          --- PASS
PASS
ok  	fwlab
```
