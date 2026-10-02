# Multi-Paxos 共识日志引擎 + 海量稀疏数据内存紧凑优化器

TypeScript 实现，零依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 运行要求

- Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，推荐 ≥ 23.6 免flag）

```bash
npm test    # node --experimental-strip-types --test "test/**/*.test.ts"
npm run demo
```

## 组件一：Multi-Paxos 多数派共识与无空洞日志提交引擎

`src/paxos/`

- **两阶段协议**：Phase 1 `Prepare/Promise` 竞选与日志收集；Phase 2 `Accept/Accepted` 提交日志槽位。Ballot 为 `(round, nodeId)` 字典序全序。
- **Leader 租约快速路径**：当选 Leader 在租约有效期内跳过 Phase 1，对连续下标直接发起 Phase 2（`node.ts` 中 `propose` → `phase2`）。租约仅是**活性**优化，安全性完全由 ballot 保证：分区旧 Leader 的 `Accept` 会被已承诺更高 ballot 的多数派拒绝并触发退位。
- **无空洞提交**：`commitIndex` 只沿连续前缀推进；新 Leader 在 Phase 1 后对 `[commitIndex+1, maxIndex]` 逐槽位取最高 ballot 的已接受值重新提案（已提交值必然被多数派交集选中），无人持有的槽位填 `NOOP`。
- **安全覆写少数派脏日志**：`CommitMsg` 携带已提交前缀的 FNV-1a 滚动哈希，follower 一致性检查失败（缺槽或脏值）时向 Leader 请求同步，Leader 以更高 ballot 的 `Accept` 重发已提交槽位，安全覆写少数派脏日志；已提交槽位幂等确认、绝不覆写。
- **无单点故障**：任意节点可通过竞选成为 Leader；多数派存活即可继续提交。

## 组件二：海量稀疏数据与内存紧凑优化器

`src/sparse/`

- **分块稀疏数组**：下标空间达 2^53，每块 4096 槽位，空块零分配。
- **容器自适应转换**：块内按密度与游程结构在 `Map`（散点）/ `RLE`（行程）/ `Dense`（稠密）三种表示间自动转换，写入时采样评估、`optimize()` 强制全量评估。
- **压缩**：有序下标 delta + varint 编码；值序列字典编码 + RLE。千万级下标空间、万级非空元素场景压缩率约 0.05%。
- **内存紧凑**：`memoryBytes()` 块级内存估算；`LeakRegistry` 追踪所有活块，`dispose()` 后断言零泄漏。
- **并发安全**：`AsyncMutex` 公平 FIFO 互斥锁保护 `transaction` / `increment` 的读-改-写原子性。

## 测试

`test/paxos.test.ts`（10 项）：选举与提交、租约快速路径跳过 Phase 1、租约过期重选、少数派不可提交、**5 节点分区验收场景**（旧 Leader 被多数派新 Leader 取代、Phase 1 补全未提交槽位、安全覆写少数派脏日志）、旧 Leader 提案被拒退位、NOOP 空洞修补、双 Leader 竞选、无空洞提交不变量、高 ballot 退位。

`test/sparse.test.ts`（22 项）：空/单元素/最大安全下标/非法下标等边界，三种容器自适应转换与降级回收，RLE 分裂合并，压缩往返与损坏数据，内存估算与 optimize 幂等，全路径零泄漏断言，1000 次并发事务自增精确性、事务互斥无重入、多键并发一致性、互斥锁 FIFO 与异常安全。
