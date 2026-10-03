# Multi-Paxos 共识日志 + 无锁并发队列（TypeScript）

零依赖 TypeScript 实现，直接使用 Node.js（>= 22.6）原生类型擦除运行，无需编译步骤、无 `node_modules`。

## 组件

**高性能无锁并发队列与 CAS 加速** — `src/lockfree-queue.ts`, `src/atomic.ts`
- Vyukov 有界 MPMC 队列：每个槽位带单调序列号，头尾游标仅通过
  `Atomics.compareExchange`（CAS）推进，全程无任何互斥锁。
- 整体驻留 `SharedArrayBuffer`，可跨 `worker_threads` 共享，零序列化。
- `src/atomic.ts` 提供 CAS / fetch-add 原语与 `AtomicCounter`。

**Multi-Paxos 多数派共识与无空洞日志提交引擎** — `src/paxos/`
- 两阶段协议：Phase 1 `Prepare/Promise` 选举 + 日志恢复；Phase 2
  `Accept/Accepted` 多数派持久化后提交。
- 领导者租约：租约有效期内跳过 Phase 1，连续 `LogEntry` 直接走 Phase 2。
- 空洞修补：新 Leader 在 Phase 1 合并多数派日志，空洞槽位填 no-op，
  每个槽位取最高 ballot 的值，安全覆写少数派脏日志。
- 无空洞提交：已提交条目严格按槽位顺序应用；commit 消息携带已选定值，
  跟随者绝不会应用本地脏条目。
- 每个节点的收件箱就是上面的无锁 MPMC 队列（消息池索引入队），
  协议处理路径无全局锁。

## 运行

```bash
npm test     # Node.js 内置测试运行器（test.sh 自动选择 >=22.6 的 Node）
npm run demo # 运行样例：队列压测 + 分区共识场景
```

## 验收场景（test/paxos.test.ts）

5 节点集群，node0 为旧 Leader；网络分区 `{0,1} | {2,3,4}` 后：
1. 旧 Leader 在少数派分区继续接受写，但无法提交（`commitUpTo` 停滞）。
2. 多数派侧 node2 通过 Phase 1 成为新 Leader，补全未提交槽位（空洞填
   no-op、保留最高 ballot 的值）。
3. 分区愈合后新 Leader 复制日志，少数派脏条目被安全覆写；旧 Leader
   的过期 ballot 写被拒绝并退位。
4. 全部 5 节点应用日志逐字节一致，无空洞、无泄漏
   （`sent == delivered + dropped`，所有收件箱排空）。

## 测试覆盖（19 项，全部通过）

- 队列边界：非法容量、空出队、满入队、回绕、int32 极值、FIFO、attach 语义。
- CAS 原语：成功/失败路径、计数器语义。
- 并发安全：4 个 worker 线程混合生产/消费 100,000 条，位图断言每条恰好
  被消费一次（无丢失、无重复、无泄漏）。
- 共识：选举、租约快进、租约过期重选、分区验收场景、空洞修补、
  提交唯一性、收件箱无泄漏。
