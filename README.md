# Multi-Paxos 多数派共识与无空洞日志提交引擎（Go 标准库实现）

纯 Go 标准库实现的 Multi-Paxos 共识日志协议，配套高性能无锁 MPMC 队列。
无任何第三方依赖，无单点故障，提交日志严格无空洞。

## 组件

- `paxos/` — Multi-Paxos 共识引擎
  - **Phase 1 (Prepare/Promise)**：候选人向多数派收集 promise；每个 promise
    携带接受方完整已接受日志，新 Leader 据此修补未提交槽位（空洞填 no-op，
    最高 ballot 的值胜出），并安全覆写少数派脏日志。
  - **Phase 2 (Accept/Accepted)**：逐槽位多数派接受后推进 commitIndex 并广播。
  - **领导者租约**：租约有效期内绕过 Phase 1，每个 LogEntry 仅需一轮 Phase 2
    即可连续提交（见 `TestLeaseBypassesPhase1`）。
  - **无锁化**：无全局互斥锁。ballot / commitIndex / apply 水位线均为原子变量；
    日志是 CAS 可增长数组（`logStore`）；提案经由无锁 MPMC 队列由每节点唯一的
    dispatcher goroutine 串行分配槽位，天然保证提交无空洞；apply 临界区仅由
    一个 CAS 自旋锁保护。
- `queue/` — Vyukov 有界 MPMC 无锁队列：序号槽位环形缓冲，生产/消费全程 CAS，
  head/tail 缓存行填充消除伪共享。
- `cmd/demo/` — 端到端演示：选举 → 租约快速提交 → 网络分区 → 多数派换主 →
  分区恢复后脏日志覆写。

## 运行

```sh
go test -race ./...   # 全部测试（含竞态检测）
go test -run . -v ./paxos/
go run ./cmd/demo     # 运行样例
```

## 验收场景（TestPartitionLeaderReplacement）

5 节点集群，分区 `{0,1} | {2,3,4}`：

1. 旧 Leader(0) 在分区后提案只能到达 2/5 节点 → `ErrNoQuorum`，commitIndex 停滞，
   少数派上留下未提交的脏日志；
2. 多数派侧 Node 2 经 Phase 1 当选，补全未提交槽位并继续提交；
3. 分区恢复后，Commit 广播携带已提交前缀，少数派节点的脏槽位被已提交值
   安全覆写（ballot 占优才允许覆写），5 个节点日志逐字节一致、无空洞。

## 测试覆盖

- `queue/`：容量取整、空出队、满入队、FIFO、环形回绕、MPMC 并发 exactly-once、
  SPSC 并发 FIFO、goroutine 无泄漏、基准测试。
- `paxos/`：选举与基本提交、租约绕过 Phase 1、租约过期重回 Phase 1、
  分区换主与脏日志覆写、空洞 no-op 修补、32 路并发提案全副本顺序一致、
  goroutine 无泄漏、关闭后快速失败。
