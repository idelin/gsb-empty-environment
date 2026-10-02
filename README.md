# Multi-Paxos 共识日志 + 分层分片局部性感知路由 (TypeScript)

纯 TypeScript 实现，零依赖，使用 Node.js 内置类型擦除（Node >= 23.6）与内置测试运行器。

## 组件

**Multi-Paxos 多数派共识与无空洞日志提交引擎** (`src/paxos/`)
- `node.ts` — 两阶段协议：Phase 1 (Prepare/Promise) 用于选举与空洞修补；Phase 2 (Accept/Accepted) 用于日志复制。领导者持有租约时对连续 `LogEntry` 直接走 Phase 2 提交，跳过 Phase 1。
- 新领导者当选后对 commitIndex 之上的所有槽位执行修复：取 Promise 中最高 ballot 的值重新提案，缺失槽位填 `NOOP`，保证提交日志无空洞。
- 少数派脏日志安全清理：节点接受更高 ballot 时，截断所有未提交的旧 ballot 条目（它们不可能再被选中），并通过 RequestSync/Sync 从领导者补回已选中的值。
- `network.ts` — 确定性内存网络模拟器，支持分区/愈合与消息计数（用于断言租约绕过 Phase 1）。

**分层分片与局部性感知路由引擎** (`src/sharding/`)
- `shardmap.ts` — 虚拟分片（vshard），Rendezvous (HRW) 哈希放置副本，节点增删时迁移量最小；副本优先跨 zone 分散。
- `topology.ts` — region → zone → node 两层层级距离模型。
- `router.ts` — 局部性感知路由：同 zone > 同 region > 跨 region；路由表为不可变快照，原子切换，并发读一致。
- `engine.ts` — 成员管理、再平衡、不变量校验（副本数、去重、zone 多样性、无泄漏）。

## 运行

```bash
npm test    # node --test test/*.test.ts
npm run demo
```

## 验收场景

`test/paxos.test.ts` 中的 "5-node partition" 测试：5 节点集群分区为 {n1,n2} | {n3,n4,n5}，旧领导者 n1 在少数派分区中无法提交；多数派选出新领导者 n3，通过 Phase 1 补全未提交槽位；分区愈合后少数派的脏日志被安全覆写/截断，全集群日志收敛一致、无空洞、无状态泄漏。
