# Multi-Paxos 多数派共识日志引擎 + 零内存分配缓存/对象池 (TypeScript)

无单点故障的分布式共识日志协议实现，配合为高频调用路径设计的对象池与零分配
LRU 缓存，消除 GC 停顿。纯 TypeScript，零依赖，直接使用 Node.js (>= 22.6)
原生 TypeScript 类型擦除运行，测试基于内置 `node:test`。

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/paxos.ts` | Multi-Paxos 引擎：Prepare/Promise + Accept/Accepted 两阶段、领导者租约快速路径、Phase 1 空洞修补、落后追随者 catch-up |
| `src/simnet.ts` | 确定性模拟网络：虚拟时钟、定点延迟、链路切断/分区、消息丢弃统计；消息信封全部池化 |
| `src/pool.ts` | `ObjectPool<T>`：预分配自由栈 + WeakSet 检出跟踪；双重释放/异物释放抛错；`assertNoLeaks()` 严格泄漏断言 |
| `src/cache.ts` | `ZeroAllocCache<V>`：定容 LRU，Int32Array 侵入式双向链表，命中路径零对象分配；驱逐回调与对象池构成闭环回收 |

## 协议要点

- **Phase 1 (Prepare/Promise)**：候选者以更高 ballot（`term*64+nodeId`）发起
  Prepare；接受者承诺拒绝更低 ballot，并上报未提交日志尾部快照。
- **Phase 2 (Accept/Accepted)**：领导者逐槽位复制 `LogEntry`，多数派接受即提交，
  并广播 Commit。
- **领导者租约**：租约有效期内跳过 Phase 1，连续 `LogEntry` 直接进入 Phase 2；
  每次多数派接受自动续租。租约过期后 `propose` 返回 `lease-expired`，须重新选举。
- **空洞修补**：新领导者赢得 Phase 1 后，对 `[commitIndex+1 .. maxSlotSeen]`
  内每个槽位取多数派报告中最高 ballot 的值重新提案，真正的空洞填 `<noop>`，
  按序提交，保证日志无空洞。
- **脏日志覆写**：少数派上未达多数派的未提交条目，在分区恢复后被新领导者
  的 Accept/Commit（更高 ballot）原地覆写；已提交条目永不变更
  （`safetyViolations` 计数恒为 0）。
- **Catch-up**：`accepted` 回复携带 `firstUncommitted`，领导者据此向落后
  追随者补发缺失的已提交前缀。

## GC 停顿消除

- `LogEntry` 与消息信封全部来自共享 `ObjectPool`，覆写时原地修改或归还；
- 缓存命中路径不创建任何对象（整数槽位 + 预分配数组 + Map 索引）；
- 缓存驱逐通过 `onEvict` 回调把值直接归还对象池，形成零分配闭环；
- 测试断言：10 万次 acquire/release 仅构造 4 个预分配对象；所有测试 teardown
  执行 `assertNoLeaks()`，严格无泄漏。

## 运行

```bash
# 需要 Node >= 22.6（原生 TypeScript 类型擦除），无需 npm install
node --test 'test/*.test.ts'   # 或 npm test
node examples/demo.ts          # 或 npm run demo
```

## 测试覆盖（27 项，全部通过）

- **对象池**：按需创建/预热、身份复用、reset 语义、双重释放、异物释放、
  容量溢出丢弃、泄漏断言、8 路并发交错无泄漏且创建数有界、10 万次复用零新建。
- **零分配缓存**：基本读写删、非法容量、LRU 驱逐顺序、get 刷新热度、覆盖写
  不驱逐、capacity=1 边界、中间删除后链表/索引一致性、clear、缓存+池闭环
  零泄漏、命中路径引用不变零槽位扰动、5000 次并发交错容量界与结构一致性。
- **共识引擎**：选举与基本复制、租约绕过 Phase 1（10 连发仅 1 轮 Prepare）、
  租约过期阻塞与重选、非领导者拒绝、Phase 1 空洞 NOOP 修补与幸存值保留、
  **5 节点分区验收场景**（旧 Leader 少数派脏日志不提交 → 多数派新 Leader
  Phase 1 补全 → 恢复后安全覆写 → 全集群收敛）、连续换主下已提交值零丢失。
