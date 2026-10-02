# quorumlease — 多节点租约竞争与脑裂预防仲裁器

基于多数派（Quorum）租约授权的主从选举仲裁协议，纯 Go 标准库实现。

## 协议规则

- **多数派授权**：候选者必须获得集群超过半数节点（`Quorum = N/2 + 1`）的租约授权，才能成为有效 Leader。
- **排他授权**：每个节点在同一任期（term）内只把带 TTL 的租约授予一个候选者；租约未过期前拒绝授予他人。
- **租约续订**：Leader 必须在租约到期前重新收集 Quorum 授权续租，否则租约过期自动剥离主权限。
- **脑裂预防**：对称/非对称网络分区下，少数派分区节点最多只能拿到本分区内的票数（< Quorum），
  无法获取或续订租约；租约过期后自动退位并拒绝一切写请求（`ErrNotLeader`）。
- **写安全**：`Write` 在执行时校验“持有未过期的多数派租约”，过期 Leader 立即拒绝写。

## 代码结构

| 文件 | 说明 |
| --- | --- |
| `lease.go` | 核心实现：`Cluster`（拓扑/分区模拟）、`Node`（竞选、授权、续租、退位、写门禁） |
| `lease_test.go` | `go test` 测试：多数派选举、少数派竞选失败、2/3 分区脑裂预防、非对称分区、租约自动回收 |
| `cmd/demo/main.go` | 可运行样例：5 节点集群 2/3 分区全过程演示 |

## 运行测试

```sh
go test -race -v ./...
```

测试结果（Go 1.11, darwin/amd64）：

```
=== RUN   TestElectLeaderRequiresQuorum
--- PASS: TestElectLeaderRequiresQuorum (0.00s)
=== RUN   TestMinorityPartitionCannotElect
--- PASS: TestMinorityPartitionCannotElect (0.00s)
=== RUN   TestSplitBrainPrevention
--- PASS: TestSplitBrainPrevention (1.31s)
=== RUN   TestAsymmetricPartition
--- PASS: TestAsymmetricPartition (0.32s)
=== RUN   TestLeaseExpiryAutoRevoke
--- PASS: TestLeaseExpiryAutoRevoke (0.36s)
PASS
ok  	quorumlease	4.084s
```

## 运行样例

```sh
go run ./cmd/demo
```

输出节选（5 节点，Quorum=3，TTL=300ms，分区 `{0,1} | {2,3,4}`，旧 Leader node-0 在少数派）：

```
== 阶段 2: 对称分区 {0,1} | {2,3,4} (旧 Leader 在少数派) ==
t= 150ms 状态: [0:L] [1:-] [2:-] [3:-] [4:-]  | node-0 写: accepted | node-3 写: REJECTED
t= 300ms 状态: [0:-] [1:-] [2:-] [3:-] [4:-]  | node-0 写: REJECTED | node-3 写: REJECTED
         --> node-3 在多数派分区赢得选举 (3/5 授权)
t= 600ms 状态: [0:-] [1:-] [2:-] [3:L] [4:-]  | node-0 写: REJECTED | node-3 写: accepted
...
== 阶段 3: 分区恢复 ==
node-0 写请求: REJECTED (旧 Leader 不复活)
node-3 写请求: accepted (多数派 Leader 持续服务)
```

可见：少数派的 node-0 在租约到期（约 300ms）后自动剥离主权限并拒绝写；
多数派的 node-3 凑齐 3/5 授权成为新 Leader 并持续续租服务；全程不存在双主。
