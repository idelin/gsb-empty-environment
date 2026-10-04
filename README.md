# Multi-Paxos 多数派共识与无空洞日志提交引擎

TypeScript 实现的 Multi-Paxos 分布式共识日志协议：多节点并发竞争与冲突仲裁、
领导者租约（Lease）下的 Phase 1 旁路、空洞修补与少数派脏日志安全覆写。
无单点故障、无死锁、无竞态；测试基于 Node.js 内置测试运行器（`node:test`）。

## 运行

需要 Node.js ≥ 22.7（原生 TypeScript type-stripping；≥ 23.6 无需任何标志）。
`run.sh` 会自动探测可用的运行时。

```bash
./run.sh test    # 运行全部 15 个测试（npm test 亦可）
./run.sh demo    # 运行 5 节点网络分区验收场景样例
```

## 架构

```
src/
  mutex.ts    异步互斥锁：临界区不跨 await，结构性杜绝持锁 I/O 导致的死锁
  network.ts  内存异步网络：分区/愈合、延迟注入、在途消息与定时器全量计数（泄漏断言）
  node.ts     PaxosNode：Prepare/Promise + Accept/Accepted 两阶段、租约、
              空洞修补、提交水位线推进与 Commit 广播
  cluster.ts  测试/演示工具：Cluster、重试去重 Client、可复现 PRNG
tests/        node:test 测试（basic / concurrency / partition）
examples/     5 节点分区验收场景演示
```

### 协议要点

- **两阶段共识**：`campaign()` 执行 Phase 1（Prepare/Promise）赢得多数派后，
  先 `repair()` 补全日志再服务写请求；`acceptAndCommit()` 执行 Phase 2，
  多数派 Accepted 后推进提交水位线并广播 Commit 通知。
- **领导者租约**：租约有效期内 `propose()` 直接走 Phase 2 提交连续 LogEntry，
  跳过 Phase 1（测试断言 `prepareSent` 不增长）；租约过期自动重新选举。
- **空洞修补**：新 Leader 的 Phase 1 收集多数派日志，逐槽位取最高 ballot 的
  已选值重提；空槽以 no-op 填充，保证提交前缀连续无空洞。
- **脏日志覆写**：Commit 通知携带权威已提交值，少数派上从未提交的脏条目
  被安全替换；已提交条目不可变（冲突覆写计入 `safetyViolations`，测试断言为空）。
- **并发安全**：每个节点一把 `Mutex`，所有状态变更在不跨 `await` 的临界区内
  完成；并发竞选按 `(round, nodeId)` 字典序仲裁，同一节点的并发竞选合并为
  一次选举，避免自我选票风暴。
- **线性一致**：客户端写只在多数派提交后返回；命令按 `(clientId, seq)` 幂等
  去重，重试不会重复提交。

## 测试覆盖（15/15 通过）

- **basic**：单 Leader 连续提交与水线连续性；租约旁路 Phase 1；租约过期重选；
  幂等去重；过期 ballot 拒绝；高 Prepare 罢黜在任 Leader。
- **concurrency**：8 客户端 × 5 并发写（随机延迟，种子可复现）下 40 条命令
  恰好一次、无空洞、全集群收敛；双 Leader 十轮对抗一致性；5 节点同时竞选
  每槽位至多一个决议；过期 Leader 无法提交；并发负载下水线单调不回退。
- **partition（验收边界）**：5 节点分区 `{1,2}|{3,4,5}`，旧 Leader 被多数派
  新 Leader 取代，新 Leader Phase 1 补全未提交槽位，愈合后少数派脏日志
  （X-dirty）被安全覆写且从未提交；空洞 no-op 修补；少数派不可选举不可提交；
  分区翻转下协议一致性保持。
- **严格无泄漏**：每个测试结束断言网络在途消息为 0、定时器为 0、
  节点 `safetyViolations` 为空、无未处理的 Promise rejection。

## 测试结果

```
ℹ tests 15
ℹ pass 15
ℹ fail 0
```

（连续 5 次运行全部通过；含随机延迟的用例使用固定种子 PRNG，结果可复现。）
