# Multi-Paxos 多数派共识与无空洞日志提交引擎 + 零分配缓存/对象池 (Go)

纯标准库实现，无第三方依赖。包含两大组件：

- **`paxos`** — Multi-Paxos 共识：Prepare/Promise + Accept/Accepted 两阶段、
  领导者租约下跳过 Phase 1 的连续日志快速提交路径、空洞修补（no-op 填充）、
  少数派脏日志安全覆写。
- **`pool`** — 低延迟零内存分配缓存与对象池：固定容量/固定槽位的 `Cache`
  （热路径 0 次堆分配）、带泄漏追踪的 `EntryPool`（确定性自由链表复用）、
  `Buffer` 池。

## 设计要点

### 共识安全性
- 选票 `Ballot{N, ID}` 全序；Acceptor 拒绝一切低于已承诺选票的 Prepare/Accept，
  **安全性从不依赖租约**——租约只是延迟优化。
- Phase 1 的 Promise 携带各 Acceptor 已接受值与已决值；新 Leader 对每个未决槽位
  取最高选票值（已决值不可变），空洞填 no-op，保证日志连续无空洞提交。
- 少数派 Leader 无法凑齐多数派即 `ErrNotLeader`；分区愈合后其低选票写入被拒绝，
  脏日志被新 Leader 的已决值覆写。

### GC 停顿消除
- `Cache`：预分配槽位数组 + 预分配索引 map + 自由链表；Put/Get/Delete 均为
  拷贝进/出预分配内存，稳态 **0 堆分配**（`testing.AllocsPerRun` 断言为 0）。
- `EntryPool`：确定性自由链表复用（不受 GC 清空影响），`Outstanding()` 计数器
  支撑严格无泄漏断言。

## 运行

```sh
go test ./... -race -count=1   # 全部测试（含竞态检测）
go run ./cmd/demo              # 5 节点分区演示
```

## 运行样例输出（go run ./cmd/demo）

```
== Phase A: node 0 leads, commits slots 1-3 ==
== Phase B: partition {0,1} | {2,3,4}; old leader writes dirty slot 4 ==
  propose "dirty-4" failed: paxos: lost majority, not leader
== Phase C: node 2 elected by majority, repairs log via Phase 1 ==
== Phase D: heal; stale leader rejected; new leader overwrites dirty logs ==
  propose "zombie" failed: paxos: lost majority, not leader

== Final committed log (node 1, formerly partitioned minority) ==
  slot 1 -> stable-1
  slot 2 -> stable-2
  slot 3 -> stable-3
  slot 4 -> real-4
  slot 5 -> real-5
  slot 6 -> real-6

transport: 81 delivered, 19 dropped (partition)
cache: 0 hits, 0 misses; pool outstanding=0 (0 = no leaks)
```

## 测试结果（go test ./... -race -count=1）

```
ok  multipaxos/paxos   1.517s   # 7 个测试：基本提交/租约快速路径/租约过期/
                                # 空洞修补/分区换主验收/已决值不可变/低选票拒绝/
                                # 200 槽连续无空洞
ok  multipaxos/pool    2.461s   # 11 个测试：池复用/并发无泄漏(64×5000)/边界
                                # (满/驱逐/超大值/双删)/并发安全/热路径 0 分配
```

验收场景 `TestPartitionOldLeaderReplaced`：5 节点 2|3 分区 → 旧 Leader 在少数派
写入脏槽位无法提交 → 多数派选新 Leader 以更高选票 Phase 1 补全未提交槽位 →
愈合后旧 Leader 被拒绝，少数派脏日志被已决值安全覆写，全集群日志收敛一致。
