# Multi-Paxos 多数派共识与无空洞日志提交引擎

多节点并发竞争与冲突仲裁器（TypeScript 版，零依赖，Node.js ≥ 23.6 内置类型擦除直接运行）。

## 架构

```
src/types.ts    选票(Ballot)、日志项(LogEntry)、消息协议类型
src/node.ts     PaxosNode：Acceptor + Proposer + Learner 三角色状态机
src/network.ts  确定性网络模拟器：逻辑时钟、分区、丢包、延迟、种子随机
src/cluster.ts  集群测试线束：逐提交校验安全不变量
tests/          node:test 内置测试（18 个用例）
examples/demo.ts 分区-换主-修复全流程演示
```

## 协议要点

- **两阶段共识**：Phase 1（Prepare/Promise）+ Phase 2（Accept/Accepted）。
  Promise 携带 `fromSlot` 之后全部已接受日志项，供新领导者恢复。
- **领导者租约**：当选即授予租约；租约内 `submit` 直接走 Phase 2，
  连续提交多条 LogEntry，完全绕过 Phase 1（测试断言 0 条 Prepare）。
  租约过期自动重新 Phase 1，期间客户端命令排队保序。安全性不依赖租约。
- **无空洞提交**：commitIndex 只连续推进；槽位仅在多数派以**当前选票**
  接受后才可提交（跨选票安全提交规则）。
- **空洞修补与脏日志覆写**：新领导者 Phase 1 恢复时，对法定多数可见的
  最高选票已接受值原样重提；多数派从未见过的槽位填 noop；少数派脏日志
  在 Commit 广播到达时被已提交值安全覆写。
- **冲突仲裁**：选票按 `(term, nodeId)` 全序比较；低选票的 Prepare/Accept
  被拒绝并回传 promisedFloor；观察到更高/同级领导者即退位。

## 并发与一致性保障

- **原子性**：actor 模型——所有消息处理同步执行到底，`send` 只入队不
  重入，节点状态转移天然原子；无锁，故无死锁、无竞态。
- **线性一致**：同一槽位永远不会提交两个不同值（集群线束在每次提交时
  全局校验）；任意两节点的已提交日志互为前缀；已提交前缀无空洞。
- **无泄漏**：引擎不使用任何定时器/句柄/Promise；确定性逻辑时钟，
  测试断言 0 残留定时器、0 在途消息；同种子运行结果完全一致。

## 运行

```bash
npm test     # node --test "tests/*.test.ts"（18 个用例）
npm run demo # 分区换主演示
```

## 测试覆盖

- `tests/basic.test.ts`：选举与租约、连续无空洞提交、状态机应用、
  租约绕过 Phase 1、租约过期重选并保序提交、跟随者拒写、跨空洞禁提交。
- `tests/conflict.test.ts`：高任期取代现任、五方同时竞选、双人决斗、
  过期 Accept 拒绝、低选票 Prepare/Accept 拒绝（携带 promisedFloor）。
- `tests/partition.test.ts`：**验收边界**——5 节点分区 {0,1}|{2,3,4}，
  旧领导者被多数派新领导者取代，Phase 1 补 noop 空洞、恢复多数派可见值、
  安全覆写少数派脏日志、已提交值保留、旧领导者退位；少数派不可选主不可
  提交；反复分区/愈合已提交前缀不变。
- `tests/fuzz.test.ts`：8 种子 × 150 轮随机选举/分区/并发提交，逐轮校验
  安全不变量；同种子确定性复现；无定时器/句柄/在途消息泄漏。
  （另以 40 种子 × 200 轮 + 5% 丢包压力验证通过。）

## 测试结果

```
ℹ tests 18
ℹ pass 18
ℹ fail 0
```
