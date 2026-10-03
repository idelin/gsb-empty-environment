# Multi-Paxos 共识日志 + 反向补偿事务编排 (TypeScript)

无单点故障的分布式共识日志协议与长生命周期事务的失败逆向补偿机制。

## 组件

- **`src/paxos.ts` — Multi-Paxos 多数派共识与无空洞日志提交引擎**
  - Prepare/Promise（Phase 1）+ Accept/Accepted（Phase 2）两阶段协议
  - 领导者租约：租约有效期内绕过 Phase 1，单轮提交连续 `LogEntry`；租约过期自动回到两阶段
  - 新 Leader 当选后立即执行空洞修补：对未提交槽位重放 Phase 1 学到的最高 ballot 值，空缺槽位补 no-op，保证 `commitIndex` 只连续推进
  - 少数派脏日志（已接受但未获多数派的条目）在愈合后被已选值安全覆写
- **`src/saga.ts` — 反向补偿与事务回滚状态编排**
  - Saga 状态机：`running → completed | compensating → compensated`
  - 每一步状态迁移都通过 Paxos 日志提交，编排状态可跨 Leader 故障恢复
  - 步骤失败时按严格逆序补偿已执行步骤；补偿失败自动重试，耗尽后挂起，`recover()` 驱动最终一致
  - `IdempotentEffects` 幂等日志：以确定性幂等键去重副作用，崩溃恢复后精确一次
- **`src/transport.ts` / `src/clock.ts`** — 确定性同步消息传输（支持网络分区）与逻辑时钟，测试零定时器、零泄漏

## 运行

需要 Node.js ≥ 22.6（直接以 `--experimental-strip-types` 运行 TypeScript，无需编译步骤）。

```bash
npm test    # Node.js 内置测试运行器 (node:test)，21 个测试
npm run demo
```

## 验收场景

`test/partition.test.ts`：5 节点集群分区 `{n1,n2}|{n3,n4,n5}`，旧 Leader n1 在少数派分区中
产生脏日志且无法提交；多数派选出新 Leader，经 Phase 1 补全未提交槽位、安全覆写脏日志；
愈合后全集群日志一致、无空洞。

`test/saga.test.ts`：反向补偿顺序、幂等精确一次、补偿重试与挂起恢复、编排器故障转移、
25 个并发 Saga 的资金守恒不变量，以及严格无泄漏断言（网络在途消息为 0、无挂起竞选、
无在途副作用、所有 Saga 达到终态）。
