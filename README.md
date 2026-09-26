# syncsim — 摘要树差异同步 + 单进程事件模拟器

纯 Go 标准库实现。节点对键空间构建摘要树（Merkle 风格），同步时只传输
差异叶记录；删除以墓碑形式传播；版本冲突使用显式规则；摘要碰撞有复核
（audit）路径。通信、故障与时间全部由单进程事件模拟器控制。

## 设计

- **摘要树**（`summary.go`）：键经 FNV-32 映射到 32 位空间，按 nibble 分层
  （arity=16, depth=8）。子树摘要是其下所有叶摘要有序折叠；叶摘要覆盖
  `(key, value, version, tombstone)`，因此墓碑同样参与摘要比较。
- **同步协议**（`node.go`）：交换子节点的摘要集合；摘要相等则跳过整棵
  子树（不传全量），不等则递归下钻，叶层只交换该前缀下的记录。
- **显式冲突规则**（`store.go`）：版本为 `(Clock, Node)` 字典序，高者胜；
  墓碑是普通记录，按同一规则竞争。规则是全序且确定的，因此 `Apply`
  幂等、可交换，天然免疫重复与乱序投递。
- **复核路径**：摘要匹配时以 `AuditRate` 概率交换该子树全部真实叶记录，
  用冲突规则修复被（强制）碰撞掩盖的分歧。摘要哈希可注入，测试用弱
  哈希人为制造碰撞。
- **事件模拟器**（`sim.go`）：虚拟时钟 + 优先队列；网络故障模型含延迟、
  丢失、重复（乱序由随机延迟产生）；节点可崩溃/重启（存储持久）。同一
  种子产生逐位一致的运行。

## 证据（go test）

安全性（safety_test.go）：
- `TestExplicitConflictRule` — 同时钟并发写/删，按节点 ID 定胜负，各节点一致。
- `TestDeletePropagatesAndNeverResurrects` — 删除传播为墓碑；迟到的旧值、
  反向同步均无法复活已删键。
- `TestIdenticalSubtreeNotTransmitted` — 5001 条记录中仅 1 条差异，全网
  仅传输 3 条记录。
- `TestForcedCollisionReviewPath` — 弱哈希强制根碰撞：关闭复核时分歧被
  掩盖（零流量、保持分叉）；开启复核后按规则收敛到最高版本。
- `TestTombstoneVisibleToSummary` — 墓碑参与摘要，空节点也能收到删除。

活性（liveness_test.go）：
- `TestFixpointUnderFaults` — 20% 丢失、15% 重复、随机延迟下，重复同步
  在有限轮内达到不动点（一轮全量交换零变更），四副本状态完全一致。
- `TestCrashRestart` — 节点崩溃错过更新，重启后追平，删除不丢失。
- `TestDeterministicSimulation` — 同种子两次运行的故障注入计数与最终
  状态逐位一致（通信/故障/时间均受控）。

## 运行

```sh
go test -v ./...   # 证据测试
go run ./cmd/demo  # 运行样例
```

## 运行样例输出

```
=== 1. Convergence under faults (drop=20% dup=15% delay=1..20) ===
fixpoint reached after 6 rounds (sim time 675 ticks)
messages: sent=5742 dropped=1196 duplicated=681, records applied=215
converged: true (all replicas identical, tombstones included)

=== 2. Identical subtrees are not transmitted in full ===
state size: 5001 records; records transmitted: 3; messages: 20
converged: true

=== 3. Deletes propagate and never resurrect ===
after put+sync:    B sees k="v1"
after delete+sync: B live=false (tombstone replicated)
stale v1 arrives late: applied=false, B live=false (old value loses to tombstone)

=== 4. Forced summary collision and the review path ===
audit off: roots collide -> no traffic; A="alpha" B="gamma" (diverged)
audit on:  matched digests re-checked -> A="gamma" B="gamma" (converged)
```

## 测试结果

```
=== RUN   TestFixpointUnderFaults
    liveness_test.go:36: fixpoint after 6 rounds; sent=5742 dropped=1196 duplicated=681 applied=215
--- PASS: TestFixpointUnderFaults (0.07s)
=== RUN   TestCrashRestart
--- PASS: TestCrashRestart (0.00s)
=== RUN   TestDeterministicSimulation
--- PASS: TestDeterministicSimulation (0.02s)
=== RUN   TestExplicitConflictRule
--- PASS: TestExplicitConflictRule (0.00s)
=== RUN   TestDeletePropagatesAndNeverResurrects
--- PASS: TestDeletePropagatesAndNeverResurrects (0.00s)
=== RUN   TestIdenticalSubtreeNotTransmitted
    safety_test.go:102: records transmitted: 3 (full state would be 5001), messages: 20
--- PASS: TestIdenticalSubtreeNotTransmitted (0.06s)
=== RUN   TestForcedCollisionReviewPath
--- PASS: TestForcedCollisionReviewPath (0.00s)
=== RUN   TestTombstoneVisibleToSummary
--- PASS: TestTombstoneVisibleToSummary (0.00s)
PASS
ok  	syncsim	0.264s
```

## 已知边界

- 墓碑暂不做垃圾回收（安全做法：需所有副本确认后才能压缩，留作扩展）。
- 复核路径按概率触发；生产部署应对根摘要定期全量复核。
