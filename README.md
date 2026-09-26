# 带版本标签的多数派读写寄存器

这是一个只使用 Go 标准库实现的单进程事件模拟版 ABD 风格多数派读/写寄存器。所有网络延迟、消息丢弃/乱序、节点崩溃/重启和逻辑时间都由确定性事件循环控制，没有真实网络、goroutine 或墙上时钟。

## 容错数量

- 集群大小：`N = 2f + 1`
- 多数派：`Q = f + 1`
- 最多容忍 `f` 个副本同时崩溃或不可达；仍有 `f+1` 个可通信副本时读写可继续。
- 任意两个多数派至少相交一个节点：`2Q - N = 1`。
- 少于 `f+1` 个响应时，操作保持 `Running`；同版本标签冲突落败时为 `Aborted`。两者都是未完成，不会返回假成功。

## 协议

每个副本持久保存一个 `(Tag, Value)`，标签全序为：

1. 先比较 `Version`；
2. `Version` 相同时比较全局唯一 `Label` 的字典序。

写操作：

1. 向所有节点发送查询；
2. 收集多数派回复，取最高版本 `v`；
3. 生成新标签 `(v+1, write-label)`；
4. 将新标签和值写入多数派后才标记完成。

读操作：

1. 向所有节点发送查询；
2. 收集多数派回复，选择最高标签及其值；
3. 将该最高标签/值写回到多数派；
4. 写回多数派后才返回该值。

副本只接受比本地标签更高的写入，因此重复、延迟和乱序消息不能降低本地版本。节点崩溃不清空持久存储，重启后重新加载该状态。

## 事件模拟器

- `Simulation.Run(maxTicks)` 按逻辑时间推进最小堆中的消息和超时事件。
- `Config.Drop` 可建模分区、停节点和永久/临时丢消息。
- `Config.Delay` 可强制任意消息延迟，从而制造乱序。
- `Crash(id)` / `Restart(id)` 控制节点故障与恢复。
- 客户端用轮次号过滤旧回复；节点按 `Node ID` 去重同一轮回复，重复消息不产生额外票数。

## 安全性证据

- **副本单调性**：副本仅在 `incoming.Tag > local.Tag` 时更新；旧标签写入被拒绝，所以本地状态永不回退。
- **多数派相交**：任何两个多数派必有交集。若标签 `T` 已被某个完成操作写入多数派，则后续查询多数派至少包含一个拥有 `T` 或更高标签的副本。
- **读不回退**：读选择查询多数派中的最高标签，并把该标签写回多数派；少数旧副本的回复再早到达，也不可能大于该最高标签。
- **新写不覆盖更高版本**：写在查询多数派后用 `highest.Version+1` 生成候选；写入阶段若发现更高版本，会落败或重新查询，不会把低标签当作成功。
- **同版本并发写决胜**：并发写可能生成相同 `Version`，此时唯一 `Label` 建立全序。较低标签写入若在自己决策前看到较高标签，会保持未完成（`Aborted`）；即使较低标签先完成，较高标签之后仍可覆盖多数派，因此两个操作都结束后最终值由较高唯一标签决定。
- **无多数不成功**：读写都以收到 `f+1` 个当前轮次响应为完成门槛；响应不足只超时重试或保持 `Running`，不修改 `Completed` 状态。
- **故障恢复安全**：重启节点保留旧的持久标签，恢复后可通过后续多数派读/写修复到更高状态；旧状态不会覆盖多数派中的新状态。

## 活性条件与证据

安全性不依赖时序；活性需要公平运行条件：

- 至少 `f+1` 个副本可处理消息；
- 发给这些副本及由其发出的消息最终送达，或允许客户端超时重试；
- 冲突写数量有限。

在此条件下，每轮超时后客户端开启新轮次。可通信多数派最终能贡献 `f+1` 个查询响应；读随后向多数派写回当前最高标签，写则提交候选标签或在发现更高标签后重新查询。有限冲突下标签不再升高时，操作完成。

若只有少于多数派节点可用，或消息持续被 `Drop` 丢弃，操作会继续保持未完成；这是活性丧失而不是安全违背。

## 运行

```sh
go test -v ./...
go run ./cmd/demo
```

## 样例输出

```text
cluster: N=5, f=2, majority quorum=3
t=6 write v1 -> v1:w-v1
t=14 write v2 with nodes 3,4 stopped -> v2:w-v2
t=20 read -> v2:w-v2="v2"; stale nodes 3,4 are repaired
t=40 write with only 2/5 live nodes -> running (not completed)
t=46 recovered write -> v3:w-blocked="v3"
```

该样例展示：

- `f=2` 时两个节点停止仍能完成多数派写；
- 两个旧节点重启后，与一个新节点组成多数派，读返回新值并修复旧副本；
- 只有 2/5 节点存活、低于多数派 3 时，写保持未完成；
- 节点恢复后同一个未完成写继续完成。

## 本次测试结果

```text
=== RUN   TestQuorumSizesAndFaultTolerance
--- PASS: TestQuorumSizesAndFaultTolerance (0.00s)
=== RUN   TestMajorityWriteAndRead
--- PASS: TestMajorityWriteAndRead (0.00s)
=== RUN   TestReadRepairsStaleMinority
--- PASS: TestReadRepairsStaleMinority (0.00s)
=== RUN   TestRestartPreservesDurableStateAndRepairs
--- PASS: TestRestartPreservesDurableStateAndRepairs (0.00s)
=== RUN   TestNoMajorityRemainsUnfinished
--- PASS: TestNoMajorityRemainsUnfinished (0.00s)
=== RUN   TestOldRepliesFirstCannotRegressRead
--- PASS: TestOldRepliesFirstCannotRegressRead (0.00s)
=== RUN   TestRandomReorderingDoesNotRegressRead
--- PASS: TestRandomReorderingDoesNotRegressRead (0.00s)
=== RUN   TestUniqueLabelBreaksConcurrentWriteTie
--- PASS: TestUniqueLabelBreaksConcurrentWriteTie (0.00s)
=== RUN   TestTagOrder
--- PASS: TestTagOrder (0.00s)
=== RUN   TestDuplicateOperationIDRejected
--- PASS: TestDuplicateOperationIDRejected (0.00s)
PASS
ok  	majorityregister	0.581s
?   	majorityregister/cmd/demo	[no test files]
```
