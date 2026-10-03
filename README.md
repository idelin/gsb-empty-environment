# ARIES 崩溃恢复引擎 + 分层分片局部性感知路由引擎（TypeScript）

纯 TypeScript 实现，零第三方依赖，使用 Node.js 内置测试运行器（`node:test`）。
代码采用可擦除类型语法（erasable syntax），Node ≥ 22.6 可直接以
`--experimental-strip-types` 运行，无需编译步骤。

## 环境要求

- Node.js ≥ 22.6（推荐 ≥ 23.6，类型擦除默认开启）

## 快速开始

```bash
npm test     # 运行全部 18 个测试（node --test）
npm run demo # 运行演示样例
```

## 模块一：ARIES 预写日志与崩溃恢复（`src/aries/`）

经典 ARIES 协议三阶段恢复：

- **Analysis**（`recovery.ts`）：从最近 Checkpoint 起扫描 WAL，重建
  事务表（TT）与脏页表（DPT），确定 Redo 起点（DPT 最小 recLSN）与
  失败事务（loser）集合。
- **Redo**（历史重复）：从 Redo 起点正向扫描，对 UPDATE/CLR 记录按
  `页不在 DPT / lsn < recLSN / pageLSN >= lsn` 三重条件跳过，否则重放
  after 镜像并推进 pageLSN——已提交与未提交的修改一律重做。
- **Undo**：对失败事务按 lastLSN 全局降序反向扫描，逐条写 CLR 补偿日志
  （`undoNextLSN` 指向被撤销记录的 `prevLSN`），应用 before 镜像；
  事务撤销完毕后写 END 记录。CLR 保证恢复过程崩溃后再恢复幂等。

支撑组件：

- `wal.ts`：WAL 追加写 + JSON Lines 文件持久化 + 崩溃后重载；
- `storage.ts`：模拟磁盘的 `DiskStorage` 与易失 `BufferPool`（页级 pageLSN）；
- `transaction.ts`：事务管理器，产生 UPDATE/COMMIT/ABORT/END/CHECKPOINT 日志。

### 验收场景（`test/aries.test.ts` / `examples/demo.ts`）

构造 WAL 文件：T1、T2 已提交，T3、T4、T5 未提交，T3 跨越 Checkpoint，
Checkpoint 之后 T2 提交，随后崩溃（缓冲池丢失）。恢复后断言：

- P1=A1、P2=B1、P3=C2（已提交数据持久化），P4=""（未提交修改全部回滚）；
- Undo 严格按 LSN 全局逆序，每条被撤销的 UPDATE 对应一条 CLR；
- 二次恢复幂等，不重复撤销。

## 模块二：分层分片与局部性感知路由（`src/shard/`）

- `topology.ts`：DC → Rack → Node 三层拓扑，网络距离度量
  （同节点 0 / 同机架 1 / 同 DC 2 / 跨 DC 3）。
- `vnodes.ts`：固定数量虚拟分片（vnode）经 Rendezvous（HRW）哈希
  + 容量权重槽位映射到节点；节点增删仅引起最小分片迁移；
  FNV-1a + Murmur3 finalizer 保证相似键的散列均匀性。
- `router.ts`：key → 分片 → 候选副本集，按 `(网络距离, 负载)` 字典序
  选择目标节点，优先本地域，过载时向次近节点溢出，全部过载回退主节点；
  节点增删与重平衡在 FIFO 异步互斥锁内原子完成，路由读永不观察到中间态。

### 测试覆盖（`test/shard.test.ts`）

- 边界：非法分片数、空拓扑、重复/未知节点、未分配分片；
- 均衡性：分片分布偏差 < 2.5 倍均值，无空节点；
- 最小迁移：增删节点迁移量接近理论下界，删除节点仅迁移其持有分片；
- 局部性：存在本地候选时 100% 命中本地，跨 DC 流量远低于随机路由；
- 并发安全：重平衡与路由并发交错下始终可路由、互斥锁严格串行；
- 无泄漏：分片守恒、无悬挂引用、负载计数严格配对（下溢即抛错）。

## 目录结构

```
src/aries/    ARIES 日志与恢复引擎
src/shard/    分层拓扑 / 虚拟分片 / 局部性路由
test/         node:test 测试（18 个用例）
examples/     端到端演示
```
