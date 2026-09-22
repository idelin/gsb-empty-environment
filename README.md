# ReadIndex 单进程事件模拟器

这是一个只使用 Go 标准库的最小 Raft ReadIndex 模拟器。所有通信、故障和时间都由同一个逻辑事件队列控制，不读取墙钟，也不使用租约。

## 协议流程

领导收到读请求后执行：

1. 记录当时的 `commitIndex` 作为 `readIndex`，并把自己计入确认集合。
2. 给其他节点发送携带本次读 ID 的 `AppendEntries` 确认消息。
3. 收到同任期、同领导的多数确认后，确认自己仍是该任期领导。
4. 等待本地状态机 `appliedIndex >= readIndex`。
5. 返回键值和读证据：领导、任期、读索引、确认节点、已应用索引。

新任期不会因为“曾经赢得选举”而直接读旧状态；每个读请求都必须获得自己的多数确认。日志已经提交但尚未应用时，读请求保持等待。

## 模拟器

- `simulator.go`：确定性逻辑时钟、事件堆、固定网络延迟、隔离和应用暂停注入。
- `raft.go`：选举、日志复制、提交推进、ReadIndex 确认和读返回。
- `cmd/readindex-demo`：可运行的故障切换示例。
- `raft_test.go`：三个核心场景测试。

## 安全性证明

设一次成功读发生在领导 `L`、任期 `T`、读索引 `R`。

1. 读证据要求任期 `T` 的多数集合 `Q1` 对本次读确认，因此 `L` 在确认时仍是任期 `T` 的领导。
2. Raft 的任期 `T` 选举多数集合 `Q2` 与 `Q1` 相交。若之后存在另一个领导，交集中的节点只可能把票投给日志至少和 `L` 一样新的候选。
3. 因而之后的领导不能丢失索引 `R` 对应的已提交日志，也不能在该位置形成不同的已提交历史。
4. 本地状态机必须先应用到 `R` 才返回；返回值正是索引 `R` 对应状态的结果。
5. 被隔离的旧领导不能与多数集合通信。它的读确认集合只包含自己，因此无法产生证据，也不会返回本地旧值。

所以任何返回结果都对应当时任期多数确认过的提交前缀，旧领导不能凭本地角色或时间租约读到旧状态。

## 活性证明

在存在稳定多数、消息最终可达、事件循环持续推进的条件下：

1. 多数派中的候选可在选举超时后赢得多数投票。
2. 领导的周期性追加消息能复制日志并推进提交索引。
3. 读确认消息经固定但有限的逻辑延迟到达多数派，确认集合最终达到多数。
4. 应用暂停恢复后，状态机会把已提交日志追到读索引，等待中的读立即完成。
5. 被隔离的领导故意不满足稳定多数前提，因此其读保持挂起是安全行为；重新连回多数后，更高任期会使它退下。

## 运行

```sh
go test -v ./...
go run ./cmd/readindex-demo
```

如果环境默认 Go 缓存目录不可写，可指定临时缓存：

```sh
GOCACHE=/tmp/readindexsim-gocache go test -v ./...
GOCACHE=/tmp/readindexsim-gocache go run ./cmd/readindex-demo
```

当前实现的样例输出：

```text
tick=10 leader=n0 term=1
normal read=1 value="v1" term=1 readIndex=2 acks=[0 1] applied=2
tick=55 isolated old leader=n0 read=2 result=waiting for quorum
new leader=n1 read=3 value="v2" term=2 readIndex=4 acks=[1 2] applied=4
tick=90 old isolated read=2 still=waiting; no wall-clock lease used
```

测试结果：

```text
=== RUN   TestReadWaitsForCurrentTermMajorityConfirmation
--- PASS: TestReadWaitsForCurrentTermMajorityConfirmation (0.00s)
=== RUN   TestReadWaitsForCommittedEntryToBeApplied
--- PASS: TestReadWaitsForCommittedEntryToBeApplied (0.00s)
=== RUN   TestIsolatedOldLeaderCannotObtainReadEvidence
--- PASS: TestIsolatedOldLeaderCannotObtainReadEvidence (0.00s)
PASS
ok  	readindexsim	0.450s
```
