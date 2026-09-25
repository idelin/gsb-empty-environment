# idedup — 持久化操作去重（幂等执行）层

纯 Go 标准库实现。调用方提供**操作 ID + 内容**，存储层保证：

- 同 ID 同内容的重复提交（含超时重试、并发竞争、崩溃后重试）返回**原始结果**，业务函数只执行一次，业务日志只追加**一条**记录；
- 同 ID **不同字节**返回 `ErrConflict`，已持久化的记录**永不被覆盖**；
- 去重状态与业务记录在**同一次原子提交**（一条 WAL 记录 + 一次 fsync）中落盘，重启只重放、**不重复追加**；
- 去重记录有**有限保留期**：只有当**调用方确认的水位**和**日志保留规则**（`MinRetain`）同时允许时才回收，不靠永久保存全部 ID；
- 过期 ID 规则**显式**：ID ≤ 回收水位 → `ErrExpired`，拒绝执行，绝不静默重放。

## 文件

| 文件 | 说明 |
|---|---|
| `store.go` | `Store`：`Open / Execute / ConfirmWatermark / Reclaim / BusinessLog`，故障注入点 |
| `wal.go` | WAL 帧（CRC 校验）、快照编解码 |
| `store_test.go` | 单元测试：去重、冲突、并发、崩溃点、回收门控、撕裂写恢复 |
| `explore_test.go` | 状态探索：有界穷尽 BFS + 固定种子随机探索，验证联合不变量 |
| `cmd/demo/main.go` | 可运行样例（目录由调用方指定） |

数据文件全部放在调用方指定的目录下：`op.log`（WAL）、`snapshot`（压缩快照）、`snapshot.tmp`（回收中临时文件，恢复时丢弃）。

## API 契约（显式规则）

```go
s, _ := idedup.Open(dir, idedup.Options{MinRetain: 100})
r, err := s.Execute(opID, content, func() []byte { return doBusiness() })
```

- **ID 单调性**：新操作 ID 必须字典序递增（如时间戳/序列号前缀），否则 `ErrOutOfOrder`。水位语义依赖该约定。
- **重复提交**：同 ID 同内容 → 返回原结果，业务函数不重跑；同 ID 不同内容 → `ErrConflict`。
- **过期规则**：ID ≤ 回收水位 → `ErrExpired`。回收后无法区分“新 ID”与“已回收 ID 的重试”，因此一律显式拒绝，绝不重新执行。
- **回收双门控**：`Reclaim()` 只回收同时满足 (a) ID ≤ 调用方 `ConfirmWatermark` 确认的水位，且 (b) 不在最新 `MinRetain` 条保留范围内的记录。

## 崩溃恢复与断电保证边界

**已保证（假设文件系统/fsync 语义正确）：**

- `Execute` 成功返回 ⇒ 记录已 fsync 到 WAL，进程崩溃与断电均不丢失；重试返回原结果。
- 崩溃发生在“记录写入”与“结果返回”之间 ⇒ 恢复后重试返回**原位置的结果**，业务记录仍只有一条。
- 未 fsync 的 WAL 尾部（断电时可能撕裂）通过 CRC 帧校验在恢复时截断；已确认的数据不受影响。
- 回收（快照）原子生效：`snapshot.tmp` 写盘 + fsync → rename → 目录 fsync → 截断 WAL。崩溃于 rename 前 = 回收未发生；崩溃于 rename 后 = 回收已生效，WAL 重放幂等（跳过快照已覆盖的 ID），不会重复追加。
- 恢复只读不重写：重启本身不向 WAL 追加任何记录。

**边界（不保证）：**

- `Execute` 未返回成功（报错/超时/崩溃）⇒ 该操作**可能已提交也可能未提交**；调用方用同 ID 同内容重试收敛到恰好一条记录。这是 at-least-once 调用 + 去重 = 效果上的 exactly-once。
- 依赖硬件/fsync 诚实：磁盘掉电后仍谎报已刷盘、misdirected write 等不在保证范围内。
- 多进程同时打开同一目录不做互斥（单写者假设）；并发安全限单进程内多 goroutine。
- 故障注入点（`SetFaultHook` + `Point*` 常量）模拟的是“崩溃 + 断电丢失未刷盘数据”的最坏情况：注入崩溃时 WAL 被截断到最近一次 fsync 的位置。

## 状态探索验证（`explore_test.go`）

不靠“永久保存全部 ID”：回收动作就在探索字母表内。对**去重记录、业务日志、回收水位**的联合不变量做两层验证：

1. `TestExploreJointInvariants`：以 12 个动作（执行/重试/冲突/确认水位/回收/4 类注入崩溃）为字母表，做深度 6 的穷尽 BFS，按可观察状态签名去重，每个状态对照参考模型检查不变量：
   - I1 每个操作 ID 至多一条业务记录；
   - I2 业务日志 == 模型幸存记录（顺序、内容、结果一致）；
   - I3 去重表与业务日志是同一提交集合；
   - I4 回收水位 ≤ 调用方确认水位（回收永不越界）；
   - I5 最新 `MinRetain` 条记录必存活（保留规则）；
   - I6 幸存记录全部严格大于回收水位。
2. `TestExploreRandomized`：200 个固定种子 × 40 步随机序列（含崩溃恢复），检查同一组不变量，覆盖比穷尽界更深的轨迹。

## 运行样例

```
$ go run ./cmd/demo /tmp/idedup-demo
store directory: /tmp/idedup-demo

1) first submit of op-001
  [business] executing op-001
   result: result-of-op-001
2) retry op-001 with identical content (timeout retry)
   result: result-of-op-001 (business function did NOT re-run)
3) same ID, different bytes -> conflict
   err: idedup: operation ID already exists with different content: id "op-001"
4) two clients race the same ID concurrently
  [business] executing op-002
   client 1 got: result-of-op-002
   client 0 got: result-of-op-002
   business records so far: 2
5) inject crash after commit of op-003, before the response
  [business] executing op-003
   caller saw: power loss
6) recover (reopen) and retry op-003 -> original result
   result: result-of-op-003, business records: 3 (still exactly one per ID)
7) more traffic, then caller confirms a watermark
  [business] executing op-004
  [business] executing op-005
  [business] executing op-006
   confirmed watermark: op-004
8) reclaim: needs BOTH the watermark and the retention rule
   reclaimed 4 entries (MinRetain=2 protects the newest 2)
   reclaimed watermark: op-004, survivors: 2
9) retry of an expired ID -> explicit ErrExpired, never re-executed
   err: idedup: operation ID expired (dedup entry reclaimed): id "op-001", reclaimed watermark is "op-004"
10) restart: recovery replays without re-appending
   business log after restart (2 records):
     op-005 -> result-of-op-005
     op-006 -> result-of-op-006
   reclaimed watermark: op-004, reclaimed total: 4
```

## 测试结果

```
$ go test -count=1 -v ./...
=== RUN   TestExploreJointInvariants
    explore_test.go:377: explored 45 paths, 45 distinct states, depth 6
--- PASS: TestExploreJointInvariants (16.86s)
--- PASS: TestExploreRandomized (9.13s)
--- PASS: TestDuplicateSameContentReturnsOriginal
--- PASS: TestConflictDifferentContent
--- PASS: TestConcurrentSameIDPublishesOneRecord
--- PASS: TestCrashAtEachCommitPoint/{before-write,after-write,after-sync,before-respond}
--- PASS: TestRestartDoesNotReappend
--- PASS: TestReclaimRequiresBothWatermarkAndRetention
--- PASS: TestExpiredIDRuleIsExplicit
--- PASS: TestOutOfOrderIDRejected
--- PASS: TestReclaimCrashRecovery/{snap-before-rename,snap-after-rename,snap-after-truncate}
--- PASS: TestTornTailTruncated
--- PASS: TestReclaimStateSurvivesRestart
PASS
ok  	idedup	39.692s
```
