# swimlab — 离散事件网络模拟器 + SWIM 协议确定性测试驱动 (Go)

单进程内完整模拟虚拟交换机、丢包率、延迟抖动与时钟步进，为 SWIM 状态机
提供零随机性、百分百可复现的测试环境。仅使用 Go 标准库。

## 结构

- `sim/` — 离散事件模拟器：虚拟时钟、按 `(tick, seq)` 严格排序的事件队列、
  虚拟交换机（全局/逐链路丢包率 + 均匀延迟抖动，全部来自种子化 PRNG）。
- `swim/` — SWIM 风格故障检测状态机：Alive/Suspect/Dead、ping/ack、
  怀疑超时、死亡宣告，以及确定性仲裁（`arbitrateDead`/`refute`）。
- `scenario/` — 测试场景装配（竞态场景与混沌场景），测试与 demo 共用。
- `swim/swim_test.go` — `go test` 确定性测试。
- `main.go` — 运行样例 demo。

## 确定性保证

- 无 wall clock、无 goroutine、节点内无 PRNG；探测目标轮询选择。
- 所有 map 迭代先排序键；节点每 tick 按 ID 字典序驱动。
- 同 tick 并发事件按插入序号 `seq` 全序排列，消息乱序/同时到达完全可复现。
- 仲裁规则：存活节点永远反驳自己的死亡宣告（incarnation 提升并广播 Alive）；
  过期（低 incarnation）死亡宣告直接丢弃；同 incarnation 冲突由
  `(tick, seq)` 全序确定胜负。

## 竞态场景（固定种子 42，10000 个时钟步长）

A、B、C 三节点。tick 100 隔断链路 A<->B（双向 100% 丢包），tick 124 恢复。
对称的怀疑计时使 **A 与 B 在 tick 125 同时宣告对方死亡**，两条死亡宣告穿过
刚恢复的链路，各自触发对方节点上的仲裁代码：

```
tick=100 TEST: partition link A<->B (loss=1.0)
tick=105 node A suspects B (inc=0)
tick=105 node B suspects A (inc=0)
tick=106 node C suspects A via gossip (inc=0)
tick=107 node C suspects B via gossip (inc=0)
tick=113 node C clears suspicion of B (ack inc=0)
tick=124 TEST: heal link A<->B (loss=0.0)
tick=124 node C clears suspicion of A (ack inc=0)
tick=125 node A DECLARES B DEAD (inc=0)
tick=125 node B DECLARES A DEAD (inc=0)
tick=126 node C accepts death of B (inc=0)
tick=127 node B ARBITRATES own death claim (inc=0): refuting
tick=127 node B REFUTES dead claim, incarnation -> 1
tick=127 node A ARBITRATES own death claim (inc=0): refuting
tick=127 node A REFUTES dead claim, incarnation -> 1
tick=127 node C accepts death of A (inc=0)
tick=128 node C REVIVES B (inc=1) after refutation
tick=128 node B REVIVES A (inc=1) after refutation
tick=128 node C REVIVES A (inc=1) after refutation
tick=129 node A REVIVES B (inc=1) after refutation
```

终态：全员 alive，A、B incarnation=1，C incarnation=0，
事件日志 digest 恒为 `3f0f60683db75f63`。

## 运行

```console
$ go run .                      # demo：打印完整时间线、终态与 digest
$ go test ./... -v -count=1     # 确定性测试
```

## 测试结果

```console
=== RUN   TestMutualDeadDeclarationRace
--- PASS: TestMutualDeadDeclarationRace (0.01s)
=== RUN   TestDeterministicReplay
--- PASS: TestDeterministicReplay (0.13s)
=== RUN   TestChaosDeterminism
    swim_test.go:118: chaos: sent=19169 dropped=3766 digest=df8ba42068857055
--- PASS: TestChaosDeterminism (0.27s)
PASS
ok  	swimlab/swim	0.854s
```

- `TestMutualDeadDeclarationRace`：精确重现两节点同时宣告对方崩溃的竞态，
  验证仲裁后双方各反驳一次、集群收敛到 all-alive，并锁定黄金 digest。
- `TestDeterministicReplay`：同种子跑两遍 10000 步，事件日志逐字节一致。
- `TestChaosDeterminism`：5 节点、20% 丢包 + 抖动下跑两遍 10000 步，
  digest 与全部 incarnation 完全一致（共 19169 包、丢弃 3766 包）。
