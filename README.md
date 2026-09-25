# 带版本标签的多数派读写寄存器（ABD）

纯 Go 标准库实现的多数派（quorum）原子寄存器，算法为
Attiya–Bar-Noy–Dolev (ABD) 两阶段协议。所有通信、故障与时间都在
单进程离散事件模拟器中受控运行，无任何外部依赖。

## 结构

- `register/tag.go` — 版本标签 `(Ver, Writer)`，字典序全序；写者 ID 全局唯一，故标签唯一。
- `register/sim.go` — 事件模拟器：虚拟时钟、随机网络延迟（消息乱序）、崩溃/重启事件。
- `register/node.go` — 副本：状态仅存于模拟磁盘（write-through），重启后完整恢复；
  过期标签的 put 会被忽略（抗乱序/抗延迟重复）。
- `register/client.go` — 客户端两阶段协议；无多数派时返回"未完成"（`OK=false`），绝不假成功。
- `register/register_test.go` — 全部安全性/活性证据，跑在 `go test` 中。
- `cmd/demo/main.go` — 可运行的叙事化演示。

## 协议

- **写 v**：阶段 1 向全部副本取标签，等到多数派回应后取最大版本 `maxVer`；
  阶段 2 把 `(maxVer+1, clientID, v)` 写到多数派即完成。
- **读**：阶段 1 从多数派取最大 `(tag, value)`；阶段 2 把该二元组**写回多数派**
  （读修复），然后返回值。
- **容错数量**：`n = 2f+1` 个副本，quorum = `f+1 = n/2+1`，容忍 `f = (n-1)/2`
  个副本崩溃（`Sim.FaultTolerance()` 明确给出）。

## 安全性（safety）论证

任意两个多数派必有交集（`2(f+1) > 2f+1`）：

1. **读不到旧值**：写 W 完成时其标签 `t` 已存于某个多数派 Qw。之后的读 R
   的阶段 1 多数派 Qr 与 Qw 至少交于一个副本，该副本的标签 ≥ `t`，故 R 取到的
   最大标签 ≥ `t`，返回值不旧于 W。
2. **少数旧副本不能使读回退**：旧值副本最多占少数；任何读多数派必与最新写
   多数派相交，读到的一定是较新标签。且读的阶段 2 把最高标签写回多数派，
   使后续任何多数派都能见到它（读修复）。
3. **并发写决胜**：两个并发写若阶段 1 看到相同 `maxVer`，则产生相同版本号，
   由唯一写者 ID 决胜，标签 `(Ver, Writer)` 全局唯一且全序；副本只接受更高
   标签，所有副本最终收敛到同一胜者。
4. **抗乱序**：副本仅在 put 标签严格更高时覆盖磁盘，迟到/乱序的旧 put 被忽略。

测试证据：`TestStaleMinorityCannotRegressRead`、
`TestConcurrentWritesResolvedByUniqueTags`、
`TestNoMajorityReturnsNotCompleted`、
`TestRandomizedSafetyUnderFaultsAndReordering`（50 个种子 × 随机乱序/崩溃/重启）。

## 活性（liveness）论证

模拟器保证：发给存活副本的每条消息最终送达，发给崩溃副本的请求最终得到
否定应答，因此每个请求必然产生恰好一个响应，阶段不会永久阻塞。当崩溃数
≤ f 时，存活副本 ≥ `n−f = f+1` = quorum，客户端必能集齐多数派肯定应答而完成；
当崩溃数 > f 时，操作以 `OK=false` 终止（"未完成"），不会假成功也不会死锁。

测试证据：`TestLivenessWithMaxFailures`（f=2 崩溃下 20 轮读写全部完成）、
`TestNoMajorityReturnsNotCompleted`（f+1=3 崩溃时明确返回未完成）、
`TestRestartPreservesState`（全集群重启后数据不丢）。

## 运行

```sh
go test ./... -v     # 测试（安全性 + 活性证据）
go run ./cmd/demo    # 叙事化演示
```

## 运行样例（`go run ./cmd/demo`）

```
cluster: n=5 nodes, quorum=3, tolerates f=2 crashed nodes

[t=0] alice writes "v1"
[t=12] write completed: ok=true tag={Ver:1 Writer:1}

[t=now] bob and carol write concurrently ("v2-bob", "v2-carol")
[t=27] bob:   ok=true tag={Ver:2 Writer:2}
[t=27] carol: ok=true tag={Ver:2 Writer:3}
[t=41] read resolves concurrent writes by unique tag -> "v2-carol" (tag={Ver:2 Writer:3})

[t=now] crash 2 of 5 nodes (f failures tolerated)
[t=52] read with f=2 down: ok=true value="v2-carol"

[t=now] crash a 3rd node: no majority available
[t=59] write without majority: done=true ok=false (not completed, no false success)

[t=now] restart all nodes (state survives on simulated disk)
[t=71] read after restart: ok=true value="v2-carol" tag={Ver:2 Writer:3}

per-node durable state at the end:
  node 0: up=true stored=true tag={Ver:2 Writer:3} value="v2-carol"
  node 1: up=true stored=true tag={Ver:2 Writer:3} value="v2-carol"
  node 2: up=true stored=true tag={Ver:2 Writer:3} value="v2-carol"
  node 3: up=true stored=true tag={Ver:2 Writer:3} value="v2-carol"
  node 4: up=true stored=true tag={Ver:2 Writer:3} value="v2-carol"
```

## 测试结果（`go test ./... -v`）

```
=== RUN   TestQuorumAndFaultTolerance
--- PASS: TestQuorumAndFaultTolerance (0.00s)
=== RUN   TestWriteThenRead
--- PASS: TestWriteThenRead (0.00s)
=== RUN   TestStaleMinorityCannotRegressRead
--- PASS: TestStaleMinorityCannotRegressRead (0.00s)
=== RUN   TestConcurrentWritesResolvedByUniqueTags
--- PASS: TestConcurrentWritesResolvedByUniqueTags (0.00s)
=== RUN   TestNoMajorityReturnsNotCompleted
--- PASS: TestNoMajorityReturnsNotCompleted (0.00s)
=== RUN   TestRestartPreservesState
--- PASS: TestRestartPreservesState (0.00s)
=== RUN   TestLivenessWithMaxFailures
--- PASS: TestLivenessWithMaxFailures (0.00s)
=== RUN   TestRandomizedSafetyUnderFaultsAndReordering
--- PASS: TestRandomizedSafetyUnderFaultsAndReordering (0.30s)
PASS
ok  	majorityregister/register	0.842s
```
